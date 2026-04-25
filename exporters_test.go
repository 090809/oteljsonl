package oteljsonl

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	apilog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/metric"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

//nolint:wsl_v5
func TestNewConfigWithSymmetricOptions(t *testing.T) {
	t.Parallel()

	key := bytes.Repeat([]byte{9}, 32)

	cfg, err := NewConfig(
		WithPath("out.jsonl"),
		WithAppend(true),
		WithCreateDirs(true),
		WithBufferSize(128*1024),
		WithFlushThresholdBytes(512*1024),
		WithSyncOnFlush(true),
		WithCompression(CompressionGzip),
		WithCompressionLevel(1),
		WithSymmetricEncryption(key, []byte("aad")),
	)
	if err != nil {
		t.Fatalf("NewConfig() error = %v", err)
	}
	if cfg.Path != "out.jsonl" || !cfg.Append || !cfg.CreateDirs || !cfg.SyncOnFlush {
		t.Fatalf("unexpected config flags: %+v", cfg)
	}
	if cfg.BufferSize != 128*1024 || cfg.FlushThresholdBytes != 512*1024 || cfg.Compression != CompressionGzip || cfg.CompressionLevel != 1 {
		t.Fatalf("unexpected config values: %+v", cfg)
	}
	if !bytes.Equal(cfg.Encryption.Key, key) || !bytes.Equal(cfg.Encryption.AAD, []byte("aad")) {
		t.Fatalf("unexpected encryption config: %+v", cfg.Encryption)
	}

	key[0] = 1

	if cfg.Encryption.Key[0] == 1 {
		t.Fatal("expected symmetric key to be copied")
	}
}

//nolint:wsl_v5
func TestNewConfigWithAsymmetricOptions(t *testing.T) {
	t.Parallel()

	publicKey, _, err := GenerateX25519KeyPair()
	if err != nil {
		t.Fatalf("GenerateX25519KeyPair() error = %v", err)
	}
	cfg, err := NewConfig(
		WithPath("secure.jsonl"),
		WithCompression(CompressionGzip),
		WithAAD([]byte("base-aad")),
		WithRecipient("primary", publicKey),
	)
	if err != nil {
		t.Fatalf("NewConfig() error = %v", err)
	}
	if cfg.Path != "secure.jsonl" || cfg.Compression != CompressionGzip {
		t.Fatalf("unexpected config values: %+v", cfg)
	}

	if len(cfg.Encryption.Recipients) != 1 || cfg.Encryption.Recipients[0].KeyID != "primary" {
		t.Fatalf("unexpected recipients: %+v", cfg.Encryption.Recipients)
	}

	if !bytes.Equal(cfg.Encryption.AAD, []byte("base-aad")) {
		t.Fatalf("unexpected aad: %q", cfg.Encryption.AAD)
	}

	publicKey[0] ^= 0xff
	if bytes.Equal(cfg.Encryption.Recipients[0].PublicKey, publicKey) {
		t.Fatal("expected public key to be copied")
	}
}

//nolint:wsl_v5
func TestNewConfigRejectsMixedEncryptionOptions(t *testing.T) {
	t.Parallel()

	publicKey, _, err := GenerateX25519KeyPair()
	if err != nil {
		t.Fatalf("GenerateX25519KeyPair() error = %v", err)
	}
	_, err = NewConfig(
		WithSymmetricEncryption(bytes.Repeat([]byte{3}, 32), nil),
		WithRecipient("primary", publicKey),
	)
	if err == nil || !strings.Contains(err.Error(), "symmetric encryption is already configured") {
		t.Fatalf("expected mixed encryption options to fail, got %v", err)
	}
}

func TestBufferedForceFlush(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "trace.jsonl")

	exporter, err := NewTraceExporter(Config{
		Path:                path,
		FlushThresholdBytes: 1 << 20,
		CreateDirs:          true,
	})
	if err != nil {
		t.Fatalf("NewTraceExporter() error = %v", err)
	}

	stub := tracetest.SpanStub{
		Name:        "buffered-span",
		SpanContext: spanContext(t),
		StartTime:   time.Unix(100, 0),
		EndTime:     time.Unix(101, 0),
	}
	if err := exporter.ExportSpans(context.Background(), []sdktrace.ReadOnlySpan{stub.Snapshot()}); err != nil {
		t.Fatalf("ExportSpans() error = %v", err)
	}

	if data, err := readTestFile(path); err == nil && len(data) != 0 {
		t.Fatalf("expected buffered file to stay empty before flush, got %q", string(data))
	}

	if err := exporter.sink.flush(context.Background()); err != nil {
		t.Fatalf("flush() error = %v", err)
	}

	if err := exporter.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}

	data, err := readTestFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	if len(data) == 0 {
		t.Fatal("expected file to contain flushed data")
	}
}

//nolint:wsl_v5
func TestAppendMode(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "append.jsonl")
	ctx := context.Background()

	first, err := NewTraceExporter(Config{Path: path, CreateDirs: true})
	if err != nil {
		t.Fatalf("first exporter error = %v", err)
	}
	if err := first.ExportSpans(ctx, []sdktrace.ReadOnlySpan{tracetest.SpanStub{
		Name:        "first",
		SpanContext: spanContext(t),
		StartTime:   time.Unix(1, 0),
		EndTime:     time.Unix(2, 0),
	}.Snapshot()}); err != nil {
		t.Fatalf("first export error = %v", err)
	}

	if err := first.Shutdown(ctx); err != nil {
		t.Fatalf("first shutdown error = %v", err)
	}

	second, err := NewTraceExporter(Config{Path: path, Append: true, CreateDirs: true})
	if err != nil {
		t.Fatalf("second exporter error = %v", err)
	}
	if err := second.ExportSpans(ctx, []sdktrace.ReadOnlySpan{tracetest.SpanStub{
		Name:        "second",
		SpanContext: anotherSpanContext(t),
		StartTime:   time.Unix(3, 0),
		EndTime:     time.Unix(4, 0),
	}.Snapshot()}); err != nil {
		t.Fatalf("second export error = %v", err)
	}

	if err := second.Shutdown(ctx); err != nil {
		t.Fatalf("second shutdown error = %v", err)
	}

	lines := readLines(t, path)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}

	if !strings.Contains(lines[0], `"name":"first"`) || !strings.Contains(lines[1], `"name":"second"`) {
		t.Fatalf("unexpected append contents: %v", lines)
	}
}

//nolint:wsl_v5
func TestCompressionAndEncryptionEnvelope(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "secure.jsonl")
	key := bytes.Repeat([]byte{7}, 32)

	cfg, err := NewConfig(
		WithPath(path),
		WithCreateDirs(true),
		WithCompression(CompressionGzip),
		WithSymmetricEncryption(key, []byte("test-aad")),
	)
	if err != nil {
		t.Fatalf("NewConfig() error = %v", err)
	}

	exporter, err := NewTraceExporter(cfg)
	if err != nil {
		t.Fatalf("NewTraceExporter() error = %v", err)
	}

	if err := exporter.ExportSpans(context.Background(), []sdktrace.ReadOnlySpan{tracetest.SpanStub{
		Name:        "secure",
		SpanContext: spanContext(t),
		StartTime:   time.Unix(10, 0),
		EndTime:     time.Unix(11, 0),
	}.Snapshot()}); err != nil {
		t.Fatalf("ExportSpans() error = %v", err)
	}

	if err := exporter.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}

	lines := readLines(t, path)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}

	var encoded encodedLine
	if err := json.Unmarshal([]byte(lines[0]), &encoded); err != nil {
		t.Fatalf("Unmarshal(encoded line) error = %v", err)
	}
	if encoded.Compression != CompressionGzip || encoded.Encryption != payloadEncryptionAESGCM {
		t.Fatalf("unexpected envelope metadata: %+v", encoded)
	}

	payload, err := DecodeLine([]byte(lines[0]), DecryptConfig{
		Key: key,
		AAD: []byte("test-aad"),
	})
	if err != nil {
		t.Fatalf("DecodeLine() error = %v", err)
	}

	if !strings.Contains(string(payload), `"resourceSpans"`) || !strings.Contains(string(payload), `"name":"secure"`) {
		t.Fatalf("unexpected decoded payload: %s", payload)
	}
}

//nolint:wsl_v5
func TestAsymmetricRecipientEncryptionEnvelope(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "recipient-secure.jsonl")

	publicKey, privateKey, err := GenerateX25519KeyPair()
	if err != nil {
		t.Fatalf("GenerateX25519KeyPair() error = %v", err)
	}

	cfg, err := NewConfig(
		WithPath(path),
		WithCreateDirs(true),
		WithCompression(CompressionGzip),
		WithAsymmetricRecipients([]byte("recipient-aad"), RecipientPublicKey{
			KeyID:     "primary",
			PublicKey: publicKey,
		}),
	)
	if err != nil {
		t.Fatalf("NewConfig() error = %v", err)
	}
	exporter, err := NewTraceExporter(cfg)
	if err != nil {
		t.Fatalf("NewTraceExporter() error = %v", err)
	}

	if err := exporter.ExportSpans(context.Background(), []sdktrace.ReadOnlySpan{tracetest.SpanStub{
		Name:        "recipient-secure",
		SpanContext: spanContext(t),
		StartTime:   time.Unix(20, 0),
		EndTime:     time.Unix(21, 0),
	}.Snapshot()}); err != nil {
		t.Fatalf("ExportSpans() error = %v", err)
	}

	if err := exporter.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}

	lines := readLines(t, path)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}

	if strings.Contains(lines[0], "recipient-secure") {
		t.Fatalf("plaintext leaked into encrypted envelope: %s", lines[0])
	}

	var encoded encodedLine
	if err := json.Unmarshal([]byte(lines[0]), &encoded); err != nil {
		t.Fatalf("Unmarshal(encoded line) error = %v", err)
	}
	if encoded.Encryption != payloadEncryptionAESGCM || encoded.KeyWrapping != keyWrappingX25519HKDFAESGCM || len(encoded.Recipients) != 1 {
		t.Fatalf("unexpected asymmetric envelope metadata: %+v", encoded)
	}

	payload, err := DecodeLine([]byte(lines[0]), DecryptConfig{
		AAD: []byte("recipient-aad"),
		RecipientKeys: []RecipientPrivateKey{{
			KeyID:      "primary",
			PrivateKey: privateKey,
		}},
	})
	if err != nil {
		t.Fatalf("DecodeLine() error = %v", err)
	}
	if !strings.Contains(string(payload), `"resourceSpans"`) || !strings.Contains(string(payload), `"name":"recipient-secure"`) {
		t.Fatalf("unexpected decoded payload: %s", payload)
	}
}

func TestRejectMixedSymmetricAndAsymmetricEncryption(t *testing.T) {
	t.Parallel()

	_, err := NewTraceExporter(Config{
		Path: filepath.Join(t.TempDir(), "mixed.jsonl"),
		Encryption: EncryptionConfig{
			Key: bytes.Repeat([]byte{1}, 32),
			Recipients: []RecipientPublicKey{{
				KeyID:     "mixed",
				PublicKey: bytes.Repeat([]byte{2}, 32),
			}},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "either a symmetric key or asymmetric recipients") {
		t.Fatalf("expected mixed encryption config to fail, got %v", err)
	}
}

//nolint:wsl_v5
func TestSharedExportersWithSDKPipelines(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "signals.jsonl")

	exporters, err := NewExporters(Config{
		Path:       path,
		CreateDirs: true,
	})
	if err != nil {
		t.Fatalf("NewExporters() error = %v", err)
	}

	ctx := context.Background()
	res := sdkresource.NewWithAttributes("", attribute.String("service.name", "oteljsonl-test"))

	emitTraceForSharedExporterTest(t, ctx, res, exporters.Trace)
	emitLogForSharedExporterTest(t, ctx, res, exporters.Log)
	emitMetricForSharedExporterTest(t, ctx, res, exporters.Metric)

	lines := readLines(t, path)
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 lines, got %d: %v", len(lines), lines)
	}

	signals := make(map[string]string, 3)
	for _, line := range lines {
		switch {
		case strings.Contains(line, `"resourceSpans"`):
			signals["trace"] = line
		case strings.Contains(line, `"resourceLogs"`):
			signals["log"] = line
		case strings.Contains(line, `"resourceMetrics"`):
			signals["metric"] = line
		}
	}

	if !strings.Contains(signals["trace"], `"trace-span"`) {
		t.Fatalf("trace line missing exported span: %q", signals["trace"])
	}
	if !strings.Contains(signals["log"], `"hello log"`) {
		t.Fatalf("log line missing exported record: %q", signals["log"])
	}
	if !strings.Contains(signals["metric"], `"requests_total"`) {
		t.Fatalf("metric line missing exported metric: %q", signals["metric"])
	}
}

func emitTraceForSharedExporterTest(t *testing.T, ctx context.Context, res *sdkresource.Resource, exporter *TraceExporter) {
	t.Helper()

	traceProvider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithSyncer(exporter),
	)

	tracer := traceProvider.Tracer("trace-scope")
	_, span := tracer.Start(ctx, "trace-span")
	span.SetAttributes(attribute.String("trace.attr", "value"))
	span.End()

	if err := traceProvider.Shutdown(ctx); err != nil {
		t.Fatalf("traceProvider.Shutdown() error = %v", err)
	}
}

func emitLogForSharedExporterTest(t *testing.T, ctx context.Context, res *sdkresource.Resource, exporter *LogExporter) {
	t.Helper()

	logProvider := sdklog.NewLoggerProvider(
		sdklog.WithResource(res),
		sdklog.WithProcessor(sdklog.NewSimpleProcessor(exporter)),
	)

	logger := logProvider.Logger("log-scope")

	var record apilog.Record

	record.SetTimestamp(time.Unix(200, 0))
	record.SetSeverity(apilog.SeverityInfo)
	record.SetSeverityText("INFO")
	record.SetBody(apilog.StringValue("hello log"))
	record.AddAttributes(apilog.String("log.attr", "value"))
	logger.Emit(ctx, record)

	if err := logProvider.Shutdown(ctx); err != nil {
		t.Fatalf("logProvider.Shutdown() error = %v", err)
	}
}

func emitMetricForSharedExporterTest(t *testing.T, ctx context.Context, res *sdkresource.Resource, exporter *MetricExporter) {
	t.Helper()

	reader := sdkmetric.NewPeriodicReader(exporter, sdkmetric.WithInterval(time.Hour))
	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(reader),
	)

	meter := meterProvider.Meter("metric-scope")

	counter, err := meter.Int64Counter("requests_total")
	if err != nil {
		t.Fatalf("Int64Counter() error = %v", err)
	}

	counter.Add(ctx, 1, metric.WithAttributes(attribute.String("metric.attr", "value")))

	if err := meterProvider.ForceFlush(ctx); err != nil {
		t.Fatalf("meterProvider.ForceFlush() error = %v", err)
	}

	if err := meterProvider.Shutdown(ctx); err != nil {
		t.Fatalf("meterProvider.Shutdown() error = %v", err)
	}
}

func readLines(t *testing.T, path string) []string {
	t.Helper()

	file, err := openTestFile(path)
	if err != nil {
		t.Fatalf("Open(%s) error = %v", path, err)
	}
	defer file.Close()

	var lines []string

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024), 1024*1024)

	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner.Err() = %v", err)
	}

	return lines
}

func readTestFile(path string) ([]byte, error) {
	//nolint:gosec // Tests read files created in a temporary directory.
	return os.ReadFile(path)
}

func openTestFile(path string) (*os.File, error) {
	//nolint:gosec // Tests open files created in a temporary directory.
	return os.Open(path)
}

//nolint:wsl_v5
func spanContext(t *testing.T) trace.SpanContext {
	t.Helper()
	traceID, err := trace.TraceIDFromHex("00112233445566778899aabbccddeeff")
	if err != nil {
		t.Fatalf("TraceIDFromHex() error = %v", err)
	}

	spanID, err := trace.SpanIDFromHex("0011223344556677")
	if err != nil {
		t.Fatalf("SpanIDFromHex() error = %v", err)
	}

	return trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID,
		SpanID:  spanID,
	})
}

//nolint:wsl_v5
func anotherSpanContext(t *testing.T) trace.SpanContext {
	t.Helper()

	traceID, err := trace.TraceIDFromHex("11112222333344445555666677778888")
	if err != nil {
		t.Fatalf("TraceIDFromHex() error = %v", err)
	}
	spanID, err := trace.SpanIDFromHex("8888777766665555")
	if err != nil {
		t.Fatalf("SpanIDFromHex() error = %v", err)
	}

	return trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID,
		SpanID:  spanID,
	})
}
