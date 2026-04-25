package oteljsonl

import (
	"context"
	"fmt"
	"sync/atomic"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	traceapi "go.opentelemetry.io/otel/trace"
	colltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

type TraceExporter struct {
	sink *sink
	done atomic.Bool
}

var _ sdktrace.SpanExporter = (*TraceExporter)(nil)

func (e *TraceExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	if e.done.Load() {
		return errTraceExporterShutdown
	}

	resourceSpans := make([]*tracepb.ResourceSpans, 0, len(spans))

	for _, span := range spans {
		scope := span.InstrumentationScope()
		resourceSpans = append(resourceSpans, &tracepb.ResourceSpans{
			Resource:   resourceToProto(span.Resource()),
			SchemaUrl:  resourceSchemaURL(span.Resource()),
			ScopeSpans: []*tracepb.ScopeSpans{traceScopeSpansFromSDK(span, scope)},
		})
	}

	raw, err := marshalProtoLine(&colltracepb.ExportTraceServiceRequest{
		ResourceSpans: resourceSpans,
	})
	if err != nil {
		return err
	}

	return e.sink.writeRawLine(ctx, "trace", raw)
}

func (e *TraceExporter) Shutdown(ctx context.Context) error {
	if e.sink == nil || e.done.Swap(true) {
		return nil
	}

	if err := e.sink.closeRef(ctx); err != nil && err != errSinkClosed {
		return err
	}

	return nil
}

var errTraceExporterShutdown = fmt.Errorf("oteljsonl: trace exporter is shut down")

func traceScopeSpansFromSDK(span sdktrace.ReadOnlySpan, scope instrumentation.Scope) *tracepb.ScopeSpans {
	return &tracepb.ScopeSpans{
		Scope:     scopeToProto(scope),
		SchemaUrl: scope.SchemaURL,
		Spans:     []*tracepb.Span{traceSpanFromSDK(span)},
	}
}

func traceSpanFromSDK(span sdktrace.ReadOnlySpan) *tracepb.Span {
	spanContext := span.SpanContext()
	parent := span.Parent()
	status := span.Status()

	out := &tracepb.Span{
		TraceId:                traceIDBytes(spanContext.TraceID()),
		SpanId:                 spanIDBytes(spanContext.SpanID()),
		TraceState:             spanContext.TraceState().String(),
		Flags:                  uint32(spanContext.TraceFlags()),
		Name:                   span.Name(),
		Kind:                   spanKindToProto(span.SpanKind()),
		StartTimeUnixNano:      timestampUnixNano(span.StartTime()),
		EndTimeUnixNano:        timestampUnixNano(span.EndTime()),
		Attributes:             attrSliceToProto(span.Attributes()),
		DroppedAttributesCount: intToUint32(span.DroppedAttributes()),
		Events:                 spanEventsFromSDK(span.Events()),
		DroppedEventsCount:     intToUint32(span.DroppedEvents()),
		Links:                  spanLinksFromSDK(span.Links()),
		DroppedLinksCount:      intToUint32(span.DroppedLinks()),
		Status:                 traceStatusFromSDK(status),
	}

	if parent.IsValid() {
		out.ParentSpanId = spanIDBytes(parent.SpanID())
	}

	return out
}

func traceStatusFromSDK(status sdktrace.Status) *tracepb.Status {
	if status.Code == codes.Unset && status.Description == "" {
		return nil
	}

	return &tracepb.Status{
		Code:    statusCodeToProto(status.Code),
		Message: status.Description,
	}
}

func spanEventsFromSDK(events []sdktrace.Event) []*tracepb.Span_Event {
	if len(events) == 0 {
		return nil
	}

	out := make([]*tracepb.Span_Event, 0, len(events))
	for _, event := range events {
		out = append(out, &tracepb.Span_Event{
			TimeUnixNano:           timestampUnixNano(event.Time),
			Name:                   event.Name,
			Attributes:             attrSliceToProto(event.Attributes),
			DroppedAttributesCount: intToUint32(event.DroppedAttributeCount),
		})
	}

	return out
}

func spanLinksFromSDK(links []sdktrace.Link) []*tracepb.Span_Link {
	if len(links) == 0 {
		return nil
	}

	out := make([]*tracepb.Span_Link, 0, len(links))
	for _, link := range links {
		out = append(out, &tracepb.Span_Link{
			TraceId:                traceIDBytes(link.SpanContext.TraceID()),
			SpanId:                 spanIDBytes(link.SpanContext.SpanID()),
			TraceState:             link.SpanContext.TraceState().String(),
			Attributes:             attrSliceToProto(link.Attributes),
			DroppedAttributesCount: intToUint32(link.DroppedAttributeCount),
		})
	}

	return out
}

func spanKindToProto(kind traceapi.SpanKind) tracepb.Span_SpanKind {
	switch int(kind) {
	case 1:
		return tracepb.Span_SPAN_KIND_INTERNAL
	case 2:
		return tracepb.Span_SPAN_KIND_SERVER
	case 3:
		return tracepb.Span_SPAN_KIND_CLIENT
	case 4:
		return tracepb.Span_SPAN_KIND_PRODUCER
	case 5:
		return tracepb.Span_SPAN_KIND_CONSUMER
	default:
		return tracepb.Span_SPAN_KIND_UNSPECIFIED
	}
}

func statusCodeToProto(code codes.Code) tracepb.Status_StatusCode {
	switch code {
	case codes.Ok:
		return tracepb.Status_STATUS_CODE_OK
	case codes.Error:
		return tracepb.Status_STATUS_CODE_ERROR
	default:
		return tracepb.Status_STATUS_CODE_UNSET
	}
}
