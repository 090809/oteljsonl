package oteljsonl

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const schemaVersion = 1

type encodedLine struct {
	SchemaVersion int          `json:"schemaVersion"`
	Signal        string       `json:"signal"`
	Encoding      string       `json:"encoding"`
	Compression   Compression  `json:"compression,omitempty"`
	Encryption    string       `json:"encryption,omitempty"`
	KeyWrapping   string       `json:"keyWrapping,omitempty"`
	Nonce         string       `json:"nonce,omitempty"`
	Recipients    []wrappedKey `json:"recipients,omitempty"`
	Payload       string       `json:"payload"`
}

type sink struct {
	cfg           Config
	file          *os.File
	writer        *bufio.Writer
	recipientKeys []recipientPublicKey

	mu      sync.Mutex
	pending bytes.Buffer
	refs    int
	closed  bool
}

func newSink(cfg Config, refs int) (*sink, error) {
	if refs < 1 {
		return nil, fmt.Errorf("oteljsonl: sink refs must be positive")
	}

	cfg = cfg.withDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	if cfg.CreateDirs {
		if err := os.MkdirAll(filepath.Dir(cfg.Path), cfg.DirMode); err != nil {
			return nil, fmt.Errorf("oteljsonl: create dir: %w", err)
		}
	}

	flags := os.O_CREATE | os.O_WRONLY
	if cfg.Append {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}

	file, err := os.OpenFile(cfg.Path, flags, cfg.FileMode)
	if err != nil {
		return nil, fmt.Errorf("oteljsonl: open output file: %w", err)
	}

	recipients, err := compileRecipientPublicKeys(cfg.Encryption.Recipients)
	if err != nil {
		_ = file.Close()
		return nil, err
	}

	return &sink{
		cfg:           cfg,
		file:          file,
		writer:        bufio.NewWriterSize(file, cfg.BufferSize),
		recipientKeys: recipients,
		refs:          refs,
	}, nil
}

func (s *sink) writeRawLine(ctx context.Context, signal string, raw []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	finalLine, err := s.transformLine(raw, signal)
	if err != nil {
		return err
	}

	return s.writeBufferedLine(ctx, finalLine)
}

func (s *sink) writeBufferedLine(ctx context.Context, finalLine []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return errSinkClosed
	}

	if _, err := s.pending.Write(finalLine); err != nil {
		return fmt.Errorf("oteljsonl: buffer line: %w", err)
	}

	if err := s.pending.WriteByte('\n'); err != nil {
		return fmt.Errorf("oteljsonl: buffer newline: %w", err)
	}

	if s.pending.Len() >= s.cfg.FlushThresholdBytes {
		return s.flushLocked(ctx)
	}

	return nil
}

func (s *sink) flush(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.flushLocked(ctx)
}

func (s *sink) flushLocked(ctx context.Context) error {
	if s.closed {
		return errSinkClosed
	}

	if s.pending.Len() == 0 {
		return nil
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	data := append([]byte(nil), s.pending.Bytes()...)

	if _, err := s.writer.Write(data); err != nil {
		return fmt.Errorf("oteljsonl: write buffer: %w", err)
	}

	if err := s.writer.Flush(); err != nil {
		return fmt.Errorf("oteljsonl: flush writer: %w", err)
	}

	if s.cfg.SyncOnFlush {
		if err := s.file.Sync(); err != nil {
			return fmt.Errorf("oteljsonl: sync file: %w", err)
		}
	}

	s.pending.Reset()

	return nil
}

func (s *sink) closeRef(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}

	if err := s.flushLocked(ctx); err != nil {
		return err
	}

	s.refs--

	if s.refs > 0 {
		return nil
	}

	s.closed = true

	if err := s.file.Close(); err != nil {
		return fmt.Errorf("oteljsonl: close file: %w", err)
	}

	return nil
}

func (s *sink) transformLine(raw []byte, signal string) ([]byte, error) {
	compressed, err := s.compress(raw)
	if err != nil {
		return nil, err
	}

	if len(s.cfg.Encryption.Key) == 0 && s.cfg.Compression == CompressionNone {
		return raw, nil
	}

	payload := compressed

	line := encodedLine{
		SchemaVersion: schemaVersion,
		Signal:        signal,
		Encoding:      "base64",
		Compression:   s.cfg.Compression,
	}
	if len(s.recipientKeys) > 0 {
		var recipients []wrappedKey

		payload, nonce, recipients, err := encryptForRecipients(compressed, s.cfg.Encryption.AAD, s.recipientKeys)
		if err != nil {
			return nil, err
		}

		line.Encryption = payloadEncryptionAESGCM
		line.KeyWrapping = keyWrappingX25519HKDFAESGCM
		line.Nonce = base64.StdEncoding.EncodeToString(nonce)
		line.Recipients = recipients
		line.Payload = base64.StdEncoding.EncodeToString(payload)

		out, err := json.Marshal(line)
		if err != nil {
			return nil, fmt.Errorf("oteljsonl: marshal encoded line: %w", err)
		}

		return out, nil
	}

	if len(s.cfg.Encryption.Key) > 0 {
		var nonce []byte

		payload, nonce, err = encryptAESGCM(s.cfg.Encryption.Key, compressed, s.cfg.Encryption.AAD)
		if err != nil {
			return nil, err
		}

		line.Encryption = payloadEncryptionAESGCM
		line.Nonce = base64.StdEncoding.EncodeToString(nonce)
	}

	line.Payload = base64.StdEncoding.EncodeToString(payload)

	out, err := json.Marshal(line)
	if err != nil {
		return nil, fmt.Errorf("oteljsonl: marshal encoded line: %w", err)
	}

	return out, nil
}

func (s *sink) compress(raw []byte) ([]byte, error) {
	switch s.cfg.Compression {
	case CompressionNone:
		return raw, nil
	case CompressionGzip:
		var buf bytes.Buffer

		zw, err := gzip.NewWriterLevel(&buf, s.cfg.CompressionLevel)
		if err != nil {
			return nil, fmt.Errorf("oteljsonl: create gzip writer: %w", err)
		}

		if _, err := zw.Write(raw); err != nil {
			_ = zw.Close()
			return nil, fmt.Errorf("oteljsonl: gzip line: %w", err)
		}

		if err := zw.Close(); err != nil {
			return nil, fmt.Errorf("oteljsonl: close gzip writer: %w", err)
		}

		return buf.Bytes(), nil
	default:
		return nil, fmt.Errorf("oteljsonl: unsupported compression %q", s.cfg.Compression)
	}
}

var errSinkClosed = fmt.Errorf("oteljsonl: sink is closed")
