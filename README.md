# oteljsonl

`oteljsonl` is a standalone Go module that provides JSONL exporters compatible with:

- `go.opentelemetry.io/otel/sdk/trace`
- `go.opentelemetry.io/otel/sdk/log`
- `go.opentelemetry.io/otel/sdk/metric`

The module writes OpenTelemetry data into a JSONL file, supports append mode, buffered writes, gzip compression, symmetric encryption, and recipient-based asymmetric encryption.

## Module layout

```text
oteljsonl/
  go.mod
  config.go    configuration, constructors, exporter factory
  sink.go      shared buffered file sink and JSONL envelope writing
  trace.go     sdk/trace exporter
  logs.go      sdk/log exporter
  metrics.go   sdk/metric exporter
  crypto.go    symmetric and asymmetric encryption helpers, line decoding
  common.go    shared JSON conversion helpers
  doc.go       package overview
```

## Installation

From another module:

```bash
go get github.com/090809/oteljsonl
```

Inside this repository:

```bash
cd oteljsonl
go test ./...
```

## What the module does

`oteljsonl` converts OTel SDK data into JSON-friendly envelopes and writes one JSON object per line.

Without compression or encryption, each line is plain JSON.

With compression and/or encryption enabled, each line is still valid JSONL, but the payload is wrapped into an encoded envelope so the file remains append-friendly.

## Public API

### Constructors

- `NewConfig(opts ...ConfigOption) (Config, error)`
- `NewTraceExporter(cfg Config) (*TraceExporter, error)`
- `NewLogExporter(cfg Config) (*LogExporter, error)`
- `NewMetricExporter(cfg Config, opts ...MetricExporterOption) (*MetricExporter, error)`
- `NewExporters(cfg Config) (*Exporters, error)` creates trace/log/metric exporters sharing the same sink and target file

### Encryption helpers

- `GenerateX25519KeyPair() (publicKey []byte, privateKey []byte, err error)`
- `DecodeLine(line []byte, cfg DecryptConfig) ([]byte, error)`

### Metric options

- `WithMetricTemporalitySelector(selector sdkmetric.TemporalitySelector)`
- `WithMetricAggregationSelector(selector sdkmetric.AggregationSelector)`

### Config options

- `WithPath(path string)`
- `WithAppend(enabled bool)`
- `WithCreateDirs(enabled bool)`
- `WithDirMode(mode os.FileMode)`
- `WithFileMode(mode os.FileMode)`
- `WithBufferSize(size int)`
- `WithFlushThresholdBytes(size int)`
- `WithSyncOnFlush(enabled bool)`
- `WithCompression(compression Compression)`
- `WithCompressionLevel(level int)`
- `WithAAD(aad []byte)`
- `WithSymmetricEncryption(key []byte, aad []byte)`
- `WithAsymmetricRecipients(aad []byte, recipients ...RecipientPublicKey)`
- `WithRecipient(keyID string, publicKey []byte)`

## Configuration

`Config` controls file handling, buffering, compression, and encryption.

If you prefer not to fill the struct manually, use `NewConfig(...)` with functional options.

| Field | Meaning |
|---|---|
| `Path` | Output file path. Required. |
| `Append` | Open file in append mode instead of truncating. |
| `CreateDirs` | Create parent directories automatically. |
| `DirMode` | Permissions for created directories. |
| `FileMode` | Permissions for created file. |
| `BufferSize` | Size of the buffered writer. |
| `FlushThresholdBytes` | Pending bytes threshold that triggers a flush. |
| `SyncOnFlush` | Call `fsync` after flushing buffered data. |
| `Compression` | `CompressionNone` or `CompressionGzip`. |
| `CompressionLevel` | gzip level; `0` means default. |
| `Encryption` | Encryption settings. |

### Encryption configuration

`EncryptionConfig` supports two mutually exclusive modes:

| Mode | Fields |
|---|---|
| Symmetric | `Key`, optional `AAD` |
| Asymmetric recipient-based | `Recipients`, optional `AAD` |

If both `Key` and `Recipients` are set, constructor validation fails.

## Option-based config construction

The recommended ergonomic API is:

```go
cfg, err := oteljsonl.NewConfig(
	oteljsonl.WithPath("telemetry.jsonl"),
	oteljsonl.WithCreateDirs(true),
	oteljsonl.WithAppend(true),
	oteljsonl.WithCompression(oteljsonl.CompressionGzip),
)
if err != nil {
	panic(err)
}
```

This is especially useful when encryption is involved.

## Buffering and flushing

All exporters write through the same buffered sink implementation.

Behavior:

1. A JSON line is built in memory.
2. Optional compression and encryption are applied.
3. The resulting line is appended to an internal pending buffer.
4. Once `FlushThresholdBytes` is reached, the sink flushes to disk.
5. `Shutdown` flushes remaining data before closing the file.

Notes:

- `LogExporter` implements `ForceFlush`.
- `MetricExporter` implements `ForceFlush`.
- `TraceExporter` follows the trace exporter interface and flushes on shutdown through the sink lifecycle.
- `NewExporters` shares one sink between all three exporters, so all signals land in the same file.

## Supported encryption modes

## 1. Symmetric mode

When `Encryption.Key` is set, each encoded line uses:

- payload encryption: `AES-256-GCM`
- optional `AAD`

This is suitable when the writer is also allowed to decrypt.

## 2. Recipient-based asymmetric mode

When `Encryption.Recipients` is set, the writer only needs recipient public keys.

For each line:

1. a random 32-byte data key (DEK) is generated
2. the payload is encrypted with `AES-256-GCM`
3. the DEK is wrapped separately for each recipient using:
   - `X25519`
   - `HKDF-SHA256`
   - `AES-256-GCM` for DEK wrapping

This allows:

- **writer**: encrypt using public keys only
- **reader**: decrypt only with matching private key

This is the recommended mode for sensitive environments where the exporter process must not retain decryption capability.

## Additional authenticated data

Both symmetric and asymmetric modes support `AAD`.

Use it when decryption should only succeed in the presence of stable external context, for example:

- deployment identity
- stream identifier
- tenant identifier
- file class / policy tag

`AAD` is not stored separately in decrypted plaintext; it must be provided again during decryption.

## File format

## Plain JSONL

When compression and encryption are disabled, a line looks conceptually like this:

```json
{"schemaVersion":1,"signal":"trace","resourceSpans":[...]}
```

Signals:

- `trace`
- `log`
- `metric`

## Encoded JSONL envelope

When compression or encryption is enabled, the line becomes an envelope like:

```json
{
  "schemaVersion": 1,
  "signal": "trace",
  "encoding": "base64",
  "compression": "gzip",
  "encryption": "aes-256-gcm",
  "nonce": "...",
  "payload": "..."
}
```

For recipient-based asymmetric mode, recipient metadata is added:

```json
{
  "schemaVersion": 1,
  "signal": "trace",
  "encoding": "base64",
  "compression": "gzip",
  "encryption": "aes-256-gcm",
  "keyWrapping": "x25519-hkdf-sha256+a256gcm",
  "nonce": "...",
  "recipients": [
    {
      "keyId": "primary",
      "ephemeralPublicKey": "...",
      "nonce": "...",
      "encryptedKey": "..."
    }
  ],
  "payload": "..."
}
```

## Trace exporter

`TraceExporter` implements `sdktrace.SpanExporter`.

Each exported line contains:

- resource data
- instrumentation scope
- spans
- attributes
- events
- links
- status

The exporter returns an error after shutdown.

### Trace example

```go
package main

import (
	"context"

	"github.com/090809/oteljsonl"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func main() {
	cfg, err := oteljsonl.NewConfig(
		oteljsonl.WithPath("trace.jsonl"),
		oteljsonl.WithCreateDirs(true),
	)
	if err != nil {
		panic(err)
	}

	exp, err := oteljsonl.NewTraceExporter(cfg)
	if err != nil {
		panic(err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
	)
	defer tp.Shutdown(context.Background())
}
```

## Log exporter

`LogExporter` implements `sdklog.Exporter`.

Each exported line contains:

- resource data
- instrumentation scope
- log records
- severity and severity text
- body
- event name
- trace/span correlation IDs when present
- log attributes

### Log example

```go
package main

import (
	"context"

	"github.com/090809/oteljsonl"

	sdklog "go.opentelemetry.io/otel/sdk/log"
)

func main() {
	cfg, err := oteljsonl.NewConfig(
		oteljsonl.WithPath("logs.jsonl"),
		oteljsonl.WithCreateDirs(true),
	)
	if err != nil {
		panic(err)
	}

	exp, err := oteljsonl.NewLogExporter(cfg)
	if err != nil {
		panic(err)
	}

	provider := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(exp)),
	)
	defer provider.Shutdown(context.Background())
}
```

## Metric exporter

`MetricExporter` implements `sdkmetric.Exporter`.

It serializes:

- gauges
- sums
- histograms
- exponential histograms
- summaries
- exemplars

Metric exporter configuration can override:

- temporality selection
- aggregation selection

### Metric example

```go
package main

import (
	"context"
	"time"

	"github.com/090809/oteljsonl"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

func main() {
	cfg, err := oteljsonl.NewConfig(
		oteljsonl.WithPath("metrics.jsonl"),
		oteljsonl.WithCreateDirs(true),
	)
	if err != nil {
		panic(err)
	}

	exp, err := oteljsonl.NewMetricExporter(
		cfg,
	)
	if err != nil {
		panic(err)
	}

	reader := sdkmetric.NewPeriodicReader(exp, sdkmetric.WithInterval(5*time.Second))
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	defer mp.Shutdown(context.Background())
}
```

## Shared sink example

If you want traces, logs, and metrics in the same file:

```go
cfg, err := oteljsonl.NewConfig(
	oteljsonl.WithPath("telemetry.jsonl"),
	oteljsonl.WithCreateDirs(true),
	oteljsonl.WithAppend(true),
)
if err != nil {
	panic(err)
}

exporters, err := oteljsonl.NewExporters(cfg)
if err != nil {
	panic(err)
}

_ = exporters.Trace
_ = exporters.Log
_ = exporters.Metric
```

All three exporters share one file handle and one buffered sink.

## Symmetric encryption example

```go
cfg, err := oteljsonl.NewConfig(
	oteljsonl.WithPath("secure.jsonl"),
	oteljsonl.WithCreateDirs(true),
	oteljsonl.WithCompression(oteljsonl.CompressionGzip),
	oteljsonl.WithSymmetricEncryption(bytes.Repeat([]byte{7}, 32), []byte("prod/telemetry")),
)
if err != nil {
	panic(err)
}
```

## Asymmetric recipient-based example

Generate a key pair:

```go
pub, priv, err := oteljsonl.GenerateX25519KeyPair()
if err != nil {
	panic(err)
}

_ = priv // store outside the sensitive writer environment
```

Configure exporter with public key only:

```go
cfg, err := oteljsonl.NewConfig(
	oteljsonl.WithPath("secure.jsonl"),
	oteljsonl.WithCreateDirs(true),
	oteljsonl.WithCompression(oteljsonl.CompressionGzip),
	oteljsonl.WithAsymmetricRecipients([]byte("prod/telemetry"), oteljsonl.RecipientPublicKey{
		KeyID:     "primary",
		PublicKey: pub,
	}),
)
if err != nil {
	panic(err)
}
```

## Offline decryption example

Use `DecodeLine` to recover plaintext JSON from one stored line:

```go
plaintext, err := oteljsonl.DecodeLine(lineBytes, oteljsonl.DecryptConfig{
	AAD: []byte("prod/telemetry"),
	RecipientKeys: []oteljsonl.RecipientPrivateKey{
		{
			KeyID:      "primary",
			PrivateKey: priv,
		},
	},
})
if err != nil {
	panic(err)
}
```

For symmetric mode:

```go
plaintext, err := oteljsonl.DecodeLine(lineBytes, oteljsonl.DecryptConfig{
	Key: key,
	AAD: []byte("prod/telemetry"),
})
```

## Security notes

- A stolen file is not readable without the matching decryption key.
- In recipient mode, the exporter can operate with public keys only.
- This protects data **at rest**.
- It does **not** protect against attackers who can read process memory, intercept telemetry before encryption, or alter exporter code at runtime.
- `AAD` must match exactly at decrypt time.
- Each line is encrypted independently; append remains safe and simple.

## Operational guidance

Recommended defaults for sensitive environments:

- `CompressionGzip`
- recipient-based asymmetric encryption
- `CreateDirs: true`
- explicit `KeyID`
- separate offline decrypt tool or service
- private keys stored outside the environment that performs writes

If durability matters more than throughput, also enable:

- `SyncOnFlush: true`

## Testing status

The module includes tests for:

- append mode
- buffered flushing
- symmetric compression + encryption
- asymmetric recipient encryption
- invalid mixed encryption config
- end-to-end SDK integration for trace/log/metric

Run them with:

```bash
cd oteljsonl
go test ./...
```
