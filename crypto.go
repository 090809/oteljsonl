package oteljsonl

import (
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
)

const (
	payloadEncryptionAESGCM     = "aes-256-gcm"
	keyWrappingX25519HKDFAESGCM = "x25519-hkdf-sha256+a256gcm"
	hkdfWrapInfo                = "oteljsonl/x25519-wrap/v1"
)

type wrappedKey struct {
	KeyID              string `json:"keyId,omitempty"`
	EphemeralPublicKey string `json:"ephemeralPublicKey"`
	Nonce              string `json:"nonce"`
	EncryptedKey       string `json:"encryptedKey"`
}

type recipientPublicKey struct {
	keyID  string
	public *ecdh.PublicKey
}

type recipientPrivateKey struct {
	keyID   string
	private *ecdh.PrivateKey
}

func GenerateX25519KeyPair() (publicKey []byte, privateKey []byte, err error) {
	private, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("oteljsonl: generate X25519 key pair: %w", err)
	}

	return append([]byte(nil), private.PublicKey().Bytes()...), append([]byte(nil), private.Bytes()...), nil
}

func DecodeLine(line []byte, cfg DecryptConfig) ([]byte, error) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return nil, fmt.Errorf("oteljsonl: line is empty")
	}

	encoded, payload, err := decodeEncodedLine(line)
	if err != nil {
		return nil, err
	}

	payload, err = decryptPayload(payload, encoded, cfg)
	if err != nil {
		return nil, err
	}

	return decompressPayload(payload, encoded.Compression)
}

func decodeEncodedLine(line []byte) (encodedLine, []byte, error) {
	var encoded encodedLine

	if err := json.Unmarshal(line, &encoded); err != nil {
		return encodedLine{}, nil, fmt.Errorf("oteljsonl: decode line JSON: %w", err)
	}

	if encoded.Encoding == "" {
		return encoded, append([]byte(nil), line...), nil
	}

	if encoded.Encoding != "base64" {
		return encodedLine{}, nil, fmt.Errorf("oteljsonl: unsupported encoding %q", encoded.Encoding)
	}

	payload, err := base64.StdEncoding.DecodeString(encoded.Payload)
	if err != nil {
		return encodedLine{}, nil, fmt.Errorf("oteljsonl: decode payload: %w", err)
	}

	return encoded, payload, nil
}

func decryptPayload(payload []byte, encoded encodedLine, cfg DecryptConfig) ([]byte, error) {
	switch {
	case encoded.Encryption == "":
		return payload, nil
	case len(encoded.Recipients) > 0:
		return decryptForRecipients(payload, encoded, cfg)
	default:
		return decryptSymmetricPayload(payload, encoded, cfg)
	}
}

func decryptSymmetricPayload(payload []byte, encoded encodedLine, cfg DecryptConfig) ([]byte, error) {
	if len(cfg.Key) != 32 {
		return nil, fmt.Errorf("oteljsonl: decryption key must be 32 bytes for AES-256-GCM")
	}

	nonce, err := base64.StdEncoding.DecodeString(encoded.Nonce)
	if err != nil {
		return nil, fmt.Errorf("oteljsonl: decode nonce: %w", err)
	}

	return decryptAESGCM(cfg.Key, nonce, payload, cfg.AAD)
}

func decompressPayload(payload []byte, compression Compression) ([]byte, error) {
	switch compression {
	case CompressionNone:
		return payload, nil
	case CompressionGzip:
		reader, err := gzip.NewReader(bytes.NewReader(payload))
		if err != nil {
			return nil, fmt.Errorf("oteljsonl: open gzip payload: %w", err)
		}
		defer reader.Close()

		raw, err := io.ReadAll(reader)
		if err != nil {
			return nil, fmt.Errorf("oteljsonl: read gzip payload: %w", err)
		}

		return raw, nil
	default:
		return nil, fmt.Errorf("oteljsonl: unsupported compression %q", compression)
	}
}

func compileRecipientPublicKeys(recipients []RecipientPublicKey) ([]recipientPublicKey, error) {
	if len(recipients) == 0 {
		return nil, nil
	}

	curve := ecdh.X25519()
	seen := make(map[string]struct{}, len(recipients))
	out := make([]recipientPublicKey, 0, len(recipients))

	for _, recipient := range recipients {
		public, err := curve.NewPublicKey(recipient.PublicKey)
		if err != nil {
			return nil, fmt.Errorf("oteljsonl: invalid recipient public key: %w", err)
		}

		keyID := normalizedKeyID(recipient.KeyID, public.Bytes())
		if _, ok := seen[keyID]; ok {
			return nil, fmt.Errorf("oteljsonl: duplicate recipient key id %q", keyID)
		}

		seen[keyID] = struct{}{}
		out = append(out, recipientPublicKey{
			keyID:  keyID,
			public: public,
		})
	}

	return out, nil
}

func compileRecipientPrivateKeys(recipients []RecipientPrivateKey) ([]recipientPrivateKey, error) {
	if len(recipients) == 0 {
		return nil, nil
	}

	curve := ecdh.X25519()
	seen := make(map[string]struct{}, len(recipients))
	out := make([]recipientPrivateKey, 0, len(recipients))

	for _, recipient := range recipients {
		private, err := curve.NewPrivateKey(recipient.PrivateKey)
		if err != nil {
			return nil, fmt.Errorf("oteljsonl: invalid recipient private key: %w", err)
		}

		keyID := normalizedKeyID(recipient.KeyID, private.PublicKey().Bytes())
		if _, ok := seen[keyID]; ok {
			return nil, fmt.Errorf("oteljsonl: duplicate recipient private key id %q", keyID)
		}

		seen[keyID] = struct{}{}
		out = append(out, recipientPrivateKey{
			keyID:   keyID,
			private: private,
		})
	}

	return out, nil
}

func encryptForRecipients(plaintext, aad []byte, recipients []recipientPublicKey) ([]byte, []byte, []wrappedKey, error) {
	dek := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, dek); err != nil {
		return nil, nil, nil, fmt.Errorf("oteljsonl: generate data key: %w", err)
	}

	payload, nonce, err := encryptAESGCM(dek, plaintext, aad)
	if err != nil {
		return nil, nil, nil, err
	}

	curve := ecdh.X25519()

	wrapped := make([]wrappedKey, 0, len(recipients))

	for _, recipient := range recipients {
		ephemeral, err := curve.GenerateKey(rand.Reader)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("oteljsonl: generate ephemeral key: %w", err)
		}

		wrapKey, err := deriveWrapKey(ephemeral, recipient.public)
		if err != nil {
			return nil, nil, nil, err
		}

		encryptedKey, wrapNonce, err := encryptAESGCM(wrapKey, dek, aad)
		if err != nil {
			return nil, nil, nil, err
		}

		wrapped = append(wrapped, wrappedKey{
			KeyID:              recipient.keyID,
			EphemeralPublicKey: base64.StdEncoding.EncodeToString(ephemeral.PublicKey().Bytes()),
			Nonce:              base64.StdEncoding.EncodeToString(wrapNonce),
			EncryptedKey:       base64.StdEncoding.EncodeToString(encryptedKey),
		})
	}

	return payload, nonce, wrapped, nil
}

func decryptForRecipients(payload []byte, encoded encodedLine, cfg DecryptConfig) ([]byte, error) {
	if encoded.Encryption != payloadEncryptionAESGCM {
		return nil, fmt.Errorf("oteljsonl: unsupported payload encryption %q", encoded.Encryption)
	}

	if encoded.KeyWrapping != keyWrappingX25519HKDFAESGCM {
		return nil, fmt.Errorf("oteljsonl: unsupported key wrapping %q", encoded.KeyWrapping)
	}

	recipients, err := compileRecipientPrivateKeys(cfg.RecipientKeys)
	if err != nil {
		return nil, err
	}

	if len(recipients) == 0 {
		return nil, fmt.Errorf("oteljsonl: no recipient private keys configured")
	}

	nonce, err := base64.StdEncoding.DecodeString(encoded.Nonce)
	if err != nil {
		return nil, fmt.Errorf("oteljsonl: decode payload nonce: %w", err)
	}

	var dek []byte

	for _, stanza := range encoded.Recipients {
		for _, recipient := range recipients {
			if stanza.KeyID != "" && stanza.KeyID != recipient.keyID {
				continue
			}

			dek, err = tryUnwrapDEK(stanza, recipient, cfg.AAD)
			if err == nil {
				return decryptAESGCM(dek, nonce, payload, cfg.AAD)
			}
		}
	}

	return nil, fmt.Errorf("oteljsonl: no recipient key could decrypt the payload")
}

func tryUnwrapDEK(stanza wrappedKey, recipient recipientPrivateKey, aad []byte) ([]byte, error) {
	ephemeralBytes, err := base64.StdEncoding.DecodeString(stanza.EphemeralPublicKey)
	if err != nil {
		return nil, fmt.Errorf("oteljsonl: decode ephemeral public key: %w", err)
	}

	ephemeral, err := ecdh.X25519().NewPublicKey(ephemeralBytes)
	if err != nil {
		return nil, fmt.Errorf("oteljsonl: parse ephemeral public key: %w", err)
	}

	wrapKey, err := deriveWrapKey(recipient.private, ephemeral)
	if err != nil {
		return nil, err
	}

	nonce, err := base64.StdEncoding.DecodeString(stanza.Nonce)
	if err != nil {
		return nil, fmt.Errorf("oteljsonl: decode wrapped key nonce: %w", err)
	}

	encryptedKey, err := base64.StdEncoding.DecodeString(stanza.EncryptedKey)
	if err != nil {
		return nil, fmt.Errorf("oteljsonl: decode wrapped key: %w", err)
	}

	return decryptAESGCM(wrapKey, nonce, encryptedKey, aad)
}

func deriveWrapKey(private interface {
	ECDH(*ecdh.PublicKey) ([]byte, error)
	PublicKey() *ecdh.PublicKey
}, peer *ecdh.PublicKey) ([]byte, error) {
	shared, err := private.ECDH(peer)
	if err != nil {
		return nil, fmt.Errorf("oteljsonl: derive shared secret: %w", err)
	}

	selfPublic := private.PublicKey().Bytes()
	peerPublic := peer.Bytes()
	saltInput := make([]byte, 0, len(selfPublic)+len(peerPublic))

	if bytes.Compare(selfPublic, peerPublic) < 0 {
		saltInput = append(saltInput, selfPublic...)
		saltInput = append(saltInput, peerPublic...)
	} else {
		saltInput = append(saltInput, peerPublic...)
		saltInput = append(saltInput, selfPublic...)
	}

	wrapKey, err := hkdf.Key(sha256.New, shared, saltInput, hkdfWrapInfo, 32)
	if err != nil {
		return nil, fmt.Errorf("oteljsonl: derive wrap key: %w", err)
	}

	return wrapKey, nil
}

func encryptAESGCM(key, plaintext, aad []byte) ([]byte, []byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, fmt.Errorf("oteljsonl: create cipher: %w", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, fmt.Errorf("oteljsonl: create AEAD: %w", err)
	}

	nonce := make([]byte, aead.NonceSize())

	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, fmt.Errorf("oteljsonl: read nonce: %w", err)
	}

	return aead.Seal(nil, nonce, plaintext, aad), nonce, nil
}

func decryptAESGCM(key, nonce, ciphertext, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("oteljsonl: create cipher: %w", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("oteljsonl: create AEAD: %w", err)
	}

	plaintext, err := aead.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, fmt.Errorf("oteljsonl: decrypt payload: %w", err)
	}

	return plaintext, nil
}

func normalizedKeyID(keyID string, publicKey []byte) string {
	if keyID != "" {
		return keyID
	}

	sum := sha256.Sum256(publicKey)

	return hex.EncodeToString(sum[:8])
}
