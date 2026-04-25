package oteljsonl

import (
	"context"
	"sync/atomic"

	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	colllogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
)

type LogExporter struct {
	sink *sink
	done atomic.Bool
}

var _ sdklog.Exporter = (*LogExporter)(nil)

func (e *LogExporter) Export(ctx context.Context, records []sdklog.Record) error {
	if e.done.Load() {
		return nil
	}

	resourceLogs := make([]*logspb.ResourceLogs, 0, len(records))

	for _, record := range records {
		rec := record
		scope := rec.InstrumentationScope()

		resourceLogs = append(resourceLogs, &logspb.ResourceLogs{
			Resource:  resourceToProto(rec.Resource()),
			SchemaUrl: resourceSchemaURL(rec.Resource()),
			ScopeLogs: []*logspb.ScopeLogs{{
				Scope:      scopeToProto(scope),
				SchemaUrl:  scope.SchemaURL,
				LogRecords: []*logspb.LogRecord{logRecordFromSDK(rec)},
			}},
		})
	}

	raw, err := marshalProtoLine(&colllogspb.ExportLogsServiceRequest{
		ResourceLogs: resourceLogs,
	})
	if err != nil {
		return err
	}

	return e.sink.writeRawLine(ctx, "log", raw)
}

func logRecordFromSDK(rec sdklog.Record) *logspb.LogRecord {
	out := &logspb.LogRecord{
		TimeUnixNano:           timestampUnixNano(rec.Timestamp()),
		ObservedTimeUnixNano:   timestampUnixNano(rec.ObservedTimestamp()),
		SeverityNumber:         severityToProto(rec.Severity()),
		SeverityText:           rec.SeverityText(),
		Body:                   logValueToProto(rec.Body()),
		EventName:              rec.EventName(),
		Attributes:             logRecordAttributes(rec),
		DroppedAttributesCount: intToUint32(rec.DroppedAttributes()),
		Flags:                  uint32(rec.TraceFlags()),
	}

	if traceID := rec.TraceID(); traceID.IsValid() {
		out.TraceId = traceIDBytes(traceID)
	}

	if spanID := rec.SpanID(); spanID.IsValid() {
		out.SpanId = spanIDBytes(spanID)
	}

	return out
}

func (e *LogExporter) Shutdown(ctx context.Context) error {
	if e.sink == nil || e.done.Swap(true) {
		return nil
	}

	if err := e.sink.closeRef(ctx); err != nil && err != errSinkClosed {
		return err
	}

	return nil
}

func (e *LogExporter) ForceFlush(ctx context.Context) error {
	if e.sink == nil || e.done.Load() {
		return nil
	}

	if err := e.sink.flush(ctx); err != nil && err != errSinkClosed {
		return err
	}

	return nil
}

func logRecordAttributes(record sdklog.Record) []*commonpb.KeyValue {
	if record.AttributesLen() == 0 {
		return nil
	}

	out := make([]*commonpb.KeyValue, 0, record.AttributesLen())

	record.WalkAttributes(func(kv otellog.KeyValue) bool {
		out = append(out, &commonpb.KeyValue{
			Key:   kv.Key,
			Value: logValueToProto(kv.Value),
		})

		return true
	})

	return out
}

var severityNumbers = [...]logspb.SeverityNumber{
	logspb.SeverityNumber_SEVERITY_NUMBER_UNSPECIFIED,
	logspb.SeverityNumber_SEVERITY_NUMBER_TRACE,
	logspb.SeverityNumber_SEVERITY_NUMBER_TRACE2,
	logspb.SeverityNumber_SEVERITY_NUMBER_TRACE3,
	logspb.SeverityNumber_SEVERITY_NUMBER_TRACE4,
	logspb.SeverityNumber_SEVERITY_NUMBER_DEBUG,
	logspb.SeverityNumber_SEVERITY_NUMBER_DEBUG2,
	logspb.SeverityNumber_SEVERITY_NUMBER_DEBUG3,
	logspb.SeverityNumber_SEVERITY_NUMBER_DEBUG4,
	logspb.SeverityNumber_SEVERITY_NUMBER_INFO,
	logspb.SeverityNumber_SEVERITY_NUMBER_INFO2,
	logspb.SeverityNumber_SEVERITY_NUMBER_INFO3,
	logspb.SeverityNumber_SEVERITY_NUMBER_INFO4,
	logspb.SeverityNumber_SEVERITY_NUMBER_WARN,
	logspb.SeverityNumber_SEVERITY_NUMBER_WARN2,
	logspb.SeverityNumber_SEVERITY_NUMBER_WARN3,
	logspb.SeverityNumber_SEVERITY_NUMBER_WARN4,
	logspb.SeverityNumber_SEVERITY_NUMBER_ERROR,
	logspb.SeverityNumber_SEVERITY_NUMBER_ERROR2,
	logspb.SeverityNumber_SEVERITY_NUMBER_ERROR3,
	logspb.SeverityNumber_SEVERITY_NUMBER_ERROR4,
	logspb.SeverityNumber_SEVERITY_NUMBER_FATAL,
	logspb.SeverityNumber_SEVERITY_NUMBER_FATAL2,
	logspb.SeverityNumber_SEVERITY_NUMBER_FATAL3,
	logspb.SeverityNumber_SEVERITY_NUMBER_FATAL4,
}

func severityToProto(severity otellog.Severity) logspb.SeverityNumber {
	index := int(severity)
	if index < 0 || index >= len(severityNumbers) {
		return logspb.SeverityNumber_SEVERITY_NUMBER_UNSPECIFIED
	}

	return severityNumbers[index]
}
