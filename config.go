package oteljsonl

import (
	"fmt"
	"os"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

type ConfigOption func(*Config) error

type Compression string

const (
	CompressionNone Compression = ""
	CompressionGzip Compression = "gzip"
)

type EncryptionConfig struct {
	Key        []byte
	AAD        []byte
	Recipients []RecipientPublicKey
}

type RecipientPublicKey struct {
	KeyID     string
	PublicKey []byte
}

type RecipientPrivateKey struct {
	KeyID      string
	PrivateKey []byte
}

type DecryptConfig struct {
	Key           []byte
	AAD           []byte
	RecipientKeys []RecipientPrivateKey
}

type Config struct {
	Path                string
	Append              bool
	CreateDirs          bool
	DirMode             os.FileMode
	FileMode            os.FileMode
	BufferSize          int
	FlushThresholdBytes int
	SyncOnFlush         bool
	Compression         Compression
	CompressionLevel    int
	Encryption          EncryptionConfig
}

func NewConfig(opts ...ConfigOption) (Config, error) {
	var cfg Config

	for _, opt := range opts {
		if opt == nil {
			continue
		}

		if err := opt(&cfg); err != nil {
			return Config{}, err
		}
	}

	return cfg, nil
}

func WithPath(path string) ConfigOption {
	return func(cfg *Config) error {
		cfg.Path = path
		return nil
	}
}

func WithAppend(enabled bool) ConfigOption {
	return func(cfg *Config) error {
		cfg.Append = enabled
		return nil
	}
}

func WithCreateDirs(enabled bool) ConfigOption {
	return func(cfg *Config) error {
		cfg.CreateDirs = enabled
		return nil
	}
}

func WithDirMode(mode os.FileMode) ConfigOption {
	return func(cfg *Config) error {
		cfg.DirMode = mode
		return nil
	}
}

func WithFileMode(mode os.FileMode) ConfigOption {
	return func(cfg *Config) error {
		cfg.FileMode = mode
		return nil
	}
}

func WithBufferSize(size int) ConfigOption {
	return func(cfg *Config) error {
		cfg.BufferSize = size
		return nil
	}
}

func WithFlushThresholdBytes(size int) ConfigOption {
	return func(cfg *Config) error {
		cfg.FlushThresholdBytes = size
		return nil
	}
}

func WithSyncOnFlush(enabled bool) ConfigOption {
	return func(cfg *Config) error {
		cfg.SyncOnFlush = enabled
		return nil
	}
}

func WithCompression(compression Compression) ConfigOption {
	return func(cfg *Config) error {
		cfg.Compression = compression
		return nil
	}
}

func WithCompressionLevel(level int) ConfigOption {
	return func(cfg *Config) error {
		cfg.CompressionLevel = level
		return nil
	}
}

func WithAAD(aad []byte) ConfigOption {
	return func(cfg *Config) error {
		cfg.Encryption.AAD = append([]byte(nil), aad...)
		return nil
	}
}

func WithSymmetricEncryption(key []byte, aad []byte) ConfigOption {
	return func(cfg *Config) error {
		if len(cfg.Encryption.Recipients) > 0 {
			return fmt.Errorf("oteljsonl: cannot add symmetric encryption when asymmetric recipients are already configured")
		}

		cfg.Encryption.Key = append([]byte(nil), key...)
		cfg.Encryption.AAD = append([]byte(nil), aad...)

		return nil
	}
}

func WithAsymmetricRecipients(aad []byte, recipients ...RecipientPublicKey) ConfigOption {
	return func(cfg *Config) error {
		if len(cfg.Encryption.Key) > 0 {
			return fmt.Errorf("oteljsonl: cannot add asymmetric recipients when symmetric encryption is already configured")
		}

		cfg.Encryption.AAD = append([]byte(nil), aad...)
		cfg.Encryption.Recipients = cloneRecipients(recipients)

		return nil
	}
}

func WithRecipient(keyID string, publicKey []byte) ConfigOption {
	return func(cfg *Config) error {
		if len(cfg.Encryption.Key) > 0 {
			return fmt.Errorf("oteljsonl: cannot add asymmetric recipient when symmetric encryption is already configured")
		}

		cfg.Encryption.Recipients = append(cfg.Encryption.Recipients, RecipientPublicKey{
			KeyID:     keyID,
			PublicKey: append([]byte(nil), publicKey...),
		})

		return nil
	}
}

func cloneRecipients(recipients []RecipientPublicKey) []RecipientPublicKey {
	if len(recipients) == 0 {
		return nil
	}

	out := make([]RecipientPublicKey, 0, len(recipients))

	for _, recipient := range recipients {
		out = append(out, RecipientPublicKey{
			KeyID:     recipient.KeyID,
			PublicKey: append([]byte(nil), recipient.PublicKey...),
		})
	}

	return out
}

func (c Config) withDefaults() Config {
	if c.DirMode == 0 {
		c.DirMode = 0o755
	}

	if c.FileMode == 0 {
		c.FileMode = 0o644
	}

	if c.BufferSize <= 0 {
		c.BufferSize = 64 * 1024
	}

	if c.FlushThresholdBytes <= 0 {
		c.FlushThresholdBytes = 256 * 1024
	}

	if c.CompressionLevel == 0 {
		c.CompressionLevel = -1
	}

	return c
}

func (c Config) validate() error {
	if c.Path == "" {
		return fmt.Errorf("oteljsonl: path is required")
	}

	switch c.Compression {
	case CompressionNone, CompressionGzip:
	default:
		return fmt.Errorf("oteljsonl: unsupported compression %q", c.Compression)
	}

	if len(c.Encryption.Key) != 0 && len(c.Encryption.Key) != 32 {
		return fmt.Errorf("oteljsonl: encryption key must be 32 bytes for AES-256-GCM")
	}

	if len(c.Encryption.Key) != 0 && len(c.Encryption.Recipients) != 0 {
		return fmt.Errorf("oteljsonl: configure either a symmetric key or asymmetric recipients, not both")
	}

	return nil
}

type MetricExporterOption func(*MetricExporter)

func WithMetricTemporalitySelector(selector sdkmetric.TemporalitySelector) MetricExporterOption {
	return func(exporter *MetricExporter) {
		if selector != nil {
			exporter.temporalitySelector = selector
		}
	}
}

func WithMetricAggregationSelector(selector sdkmetric.AggregationSelector) MetricExporterOption {
	return func(exporter *MetricExporter) {
		if selector != nil {
			exporter.aggregationSelector = selector
		}
	}
}

type Exporters struct {
	Trace  *TraceExporter
	Log    *LogExporter
	Metric *MetricExporter
}

func NewExporters(cfg Config) (*Exporters, error) {
	s, err := newSink(cfg, 3)
	if err != nil {
		return nil, err
	}

	return &Exporters{
		Trace:  &TraceExporter{sink: s},
		Log:    &LogExporter{sink: s},
		Metric: newMetricExporter(s),
	}, nil
}

func NewTraceExporter(cfg Config) (*TraceExporter, error) {
	s, err := newSink(cfg, 1)
	if err != nil {
		return nil, err
	}

	return &TraceExporter{sink: s}, nil
}

func NewLogExporter(cfg Config) (*LogExporter, error) {
	s, err := newSink(cfg, 1)
	if err != nil {
		return nil, err
	}

	return &LogExporter{sink: s}, nil
}

func NewMetricExporter(cfg Config, opts ...MetricExporterOption) (*MetricExporter, error) {
	s, err := newSink(cfg, 1)
	if err != nil {
		return nil, err
	}

	exporter := newMetricExporter(s)

	for _, opt := range opts {
		if opt != nil {
			opt(exporter)
		}
	}

	return exporter, nil
}

func newMetricExporter(s *sink) *MetricExporter {
	return &MetricExporter{
		sink:                s,
		temporalitySelector: sdkmetric.DefaultTemporalitySelector,
		aggregationSelector: sdkmetric.DefaultAggregationSelector,
	}
}
