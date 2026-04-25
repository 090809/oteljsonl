package oteljsonl

import (
	"fmt"
	"math"
	"time"

	"go.opentelemetry.io/otel/attribute"
	otellog "go.opentelemetry.io/otel/log"
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
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: v.AsBool()}}
	case attribute.INT64:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: v.AsInt64()}}
	case attribute.FLOAT64:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_DoubleValue{DoubleValue: v.AsFloat64()}}
	case attribute.STRING:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v.AsString()}}
	case attribute.BOOLSLICE:
		return protoArrayFromBools(v.AsBoolSlice())
	case attribute.INT64SLICE:
		return protoArrayFromInt64s(v.AsInt64Slice())
	case attribute.FLOAT64SLICE:
		return protoArrayFromFloat64s(v.AsFloat64Slice())
	case attribute.STRINGSLICE:
		return protoArrayFromStrings(v.AsStringSlice())
	default:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v.Emit()}}
	}
}

func protoArrayFromBools(values []bool) *commonpb.AnyValue {
	items := make([]*commonpb.AnyValue, 0, len(values))
	for _, value := range values {
		items = append(items, &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: value}})
	}

	return &commonpb.AnyValue{Value: &commonpb.AnyValue_ArrayValue{ArrayValue: &commonpb.ArrayValue{Values: items}}}
}

func protoArrayFromInt64s(values []int64) *commonpb.AnyValue {
	items := make([]*commonpb.AnyValue, 0, len(values))
	for _, value := range values {
		items = append(items, &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: value}})
	}

	return &commonpb.AnyValue{Value: &commonpb.AnyValue_ArrayValue{ArrayValue: &commonpb.ArrayValue{Values: items}}}
}

func protoArrayFromFloat64s(values []float64) *commonpb.AnyValue {
	items := make([]*commonpb.AnyValue, 0, len(values))
	for _, value := range values {
		items = append(items, &commonpb.AnyValue{Value: &commonpb.AnyValue_DoubleValue{DoubleValue: value}})
	}

	return &commonpb.AnyValue{Value: &commonpb.AnyValue_ArrayValue{ArrayValue: &commonpb.ArrayValue{Values: items}}}
}

func protoArrayFromStrings(values []string) *commonpb.AnyValue {
	items := make([]*commonpb.AnyValue, 0, len(values))
	for _, value := range values {
		items = append(items, &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: value}})
	}

	return &commonpb.AnyValue{Value: &commonpb.AnyValue_ArrayValue{ArrayValue: &commonpb.ArrayValue{Values: items}}}
}

func logValueToProto(v otellog.Value) *commonpb.AnyValue {
	switch v.Kind() {
	case otellog.KindEmpty:
		return &commonpb.AnyValue{}
	case otellog.KindBool:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: v.AsBool()}}
	case otellog.KindFloat64:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_DoubleValue{DoubleValue: v.AsFloat64()}}
	case otellog.KindInt64:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: v.AsInt64()}}
	case otellog.KindString:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v.AsString()}}
	case otellog.KindBytes:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_BytesValue{BytesValue: append([]byte(nil), v.AsBytes()...)}}
	case otellog.KindSlice:
		values := v.AsSlice()

		items := make([]*commonpb.AnyValue, 0, len(values))
		for _, item := range values {
			items = append(items, logValueToProto(item))
		}

		return &commonpb.AnyValue{Value: &commonpb.AnyValue_ArrayValue{ArrayValue: &commonpb.ArrayValue{Values: items}}}
	case otellog.KindMap:
		kvs := v.AsMap()

		items := make([]*commonpb.KeyValue, 0, len(kvs))
		for _, kv := range kvs {
			items = append(items, &commonpb.KeyValue{
				Key:   kv.Key,
				Value: logValueToProto(kv.Value),
			})
		}

		return &commonpb.AnyValue{Value: &commonpb.AnyValue_KvlistValue{KvlistValue: &commonpb.KeyValueList{Values: items}}}
	default:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v.String()}}
	}
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
