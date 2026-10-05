package oteljsonl

import (
	"fmt"
	"math"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/trace"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

var protoJSON = protojson.MarshalOptions{}

func marshalProtoLine(message proto.Message) ([]byte, error) {
	raw, err := protoJSON.Marshal(message)
	if err != nil {
		return nil, fmt.Errorf("oteljsonl: marshal proto line: %w", err)
	}

	return raw, nil
}

func timestampUnixNano(t time.Time) uint64 {
	if t.IsZero() {
		return 0
	}

	return uint64(t.UnixNano())
}

func resourceToProto(res *sdkresource.Resource) *resourcepb.Resource {
	if res == nil {
		return nil
	}

	return &resourcepb.Resource{
		Attributes: attrSliceToProto(res.Attributes()),
	}
}

func resourceSchemaURL(res *sdkresource.Resource) string {
	if res == nil {
		return ""
	}

	return res.SchemaURL()
}

func scopeToProto(scope instrumentation.Scope) *commonpb.InstrumentationScope {
	return &commonpb.InstrumentationScope{
		Name:       scope.Name,
		Version:    scope.Version,
		Attributes: attrSetToProto(scope.Attributes),
	}
}

func attrSliceToProto(attrs []attribute.KeyValue) []*commonpb.KeyValue {
	if len(attrs) == 0 {
		return nil
	}

	out := make([]*commonpb.KeyValue, 0, len(attrs))
	for _, kv := range attrs {
		out = append(out, &commonpb.KeyValue{
			Key:   string(kv.Key),
			Value: attrValueToProto(kv.Value),
		})
	}

	return out
}

func attrSetToProto(set attribute.Set) []*commonpb.KeyValue {
	if set.Len() == 0 {
		return nil
	}

	return attrSliceToProto(set.ToSlice())
}

func attrValueToProto(v attribute.Value) *commonpb.AnyValue {
	switch v.Type() {
	case attribute.BOOL:
		return protoBool(v.AsBool())
	case attribute.INT64:
		return protoInt64(v.AsInt64())
	case attribute.FLOAT64:
		return protoFloat64(v.AsFloat64())
	case attribute.STRING:
		return protoString(v.AsString())
	case attribute.BOOLSLICE:
		return protoArray(v.AsBoolSlice(), protoBool)
	case attribute.INT64SLICE:
		return protoArray(v.AsInt64Slice(), protoInt64)
	case attribute.FLOAT64SLICE:
		return protoArray(v.AsFloat64Slice(), protoFloat64)
	case attribute.STRINGSLICE:
		return protoArray(v.AsStringSlice(), protoString)
	case attribute.EMPTY:
		// OTLP models an empty value as an AnyValue with no field set, which is
		// also what the log pipeline has always emitted for an unset record body.
		return &commonpb.AnyValue{}
	case attribute.BYTESLICE:
		// AsByteSlice converts the internally stored string, so it already
		// returns a fresh slice that the caller may retain.
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_BytesValue{BytesValue: v.AsByteSlice()}}
	case attribute.SLICE:
		return protoArray(v.AsSlice(), attrValueToProto)
	case attribute.MAP:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_KvlistValue{
			KvlistValue: &commonpb.KeyValueList{Values: attrSliceToProto(v.AsMap())},
		}}
	default:
		return protoString(v.String())
	}
}

func protoBool(value bool) *commonpb.AnyValue {
	return &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: value}}
}

func protoInt64(value int64) *commonpb.AnyValue {
	return &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: value}}
}

func protoFloat64(value float64) *commonpb.AnyValue {
	return &commonpb.AnyValue{Value: &commonpb.AnyValue_DoubleValue{DoubleValue: value}}
}

func protoString(value string) *commonpb.AnyValue {
	return &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: value}}
}

func protoArray[T any](values []T, convert func(T) *commonpb.AnyValue) *commonpb.AnyValue {
	items := make([]*commonpb.AnyValue, 0, len(values))
	for _, value := range values {
		items = append(items, convert(value))
	}

	return &commonpb.AnyValue{Value: &commonpb.AnyValue_ArrayValue{ArrayValue: &commonpb.ArrayValue{Values: items}}}
}

func traceIDBytes(id trace.TraceID) []byte {
	if !id.IsValid() {
		return nil
	}

	data := id

	return append([]byte(nil), data[:]...)
}

func spanIDBytes(id trace.SpanID) []byte {
	if !id.IsValid() {
		return nil
	}

	data := id

	return append([]byte(nil), data[:]...)
}

func intToUint32(value int) uint32 {
	if value <= 0 {
		return 0
	}

	if uint64(value) > uint64(^uint32(0)) {
		return ^uint32(0)
	}

	return uint32(value)
}

func metricTemporalityToProto(value metricdata.Temporality) metricspb.AggregationTemporality {
	switch value {
	case metricdata.DeltaTemporality:
		return metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA
	case metricdata.CumulativeTemporality:
		return metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE
	default:
		return metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_UNSPECIFIED
	}
}

func optionalFloat64FromInt64(extrema metricdata.Extrema[int64]) *float64 {
	value, ok := extrema.Value()
	if !ok {
		return nil
	}

	floatValue := float64(value)

	return &floatValue
}

func optionalFloat64FromFloat64(extrema metricdata.Extrema[float64]) *float64 {
	value, ok := extrema.Value()
	if !ok || math.IsNaN(value) {
		return nil
	}

	return &value
}
