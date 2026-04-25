// Package oteljsonl provides file exporters for the OpenTelemetry Go SDK
// that persist trace, log, and metric telemetry as JSONL.
//
// The package is compatible with:
//   - go.opentelemetry.io/otel/sdk/trace
//   - go.opentelemetry.io/otel/sdk/log
//   - go.opentelemetry.io/otel/sdk/metric
//
// Exporters can share a single buffered file sink, support append mode,
// optionally gzip-compress payloads, and optionally encrypt payloads while
// keeping the outer storage format as JSONL.
//
// Encryption supports:
//   - symmetric AES-256-GCM keys
//   - recipient-based hybrid encryption using X25519 + HKDF-SHA256 to wrap
//     a random AES-256-GCM data key
//
// The recipient-based mode lets exporters encrypt using only public keys,
// while decryption requires the matching private key.
package oteljsonl
