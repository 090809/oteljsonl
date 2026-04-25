package oteljsonl

import (
	"context"
	"fmt"
	"sync/atomic"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	collmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
)

type MetricExporter struct {
	sink                *sink
	temporalitySelector sdkmetric.TemporalitySelector
	aggregationSelector sdkmetric.AggregationSelector
	done                atomic.Bool
}

var _ sdkmetric.Exporter = (*MetricExporter)(nil)

func (e *MetricExporter) Temporality(kind sdkmetric.InstrumentKind) metricdata.Temporality {
	return e.temporalitySelector(kind)
}

func (e *MetricExporter) Aggregation(kind sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return e.aggregationSelector(kind)
}

func (e *MetricExporter) Export(ctx context.Context, rm *metricdata.ResourceMetrics) error {
	if e.done.Load() {
		return sdkmetric.ErrExporterShutdown
	}

	if rm == nil {
		return fmt.Errorf("oteljsonl: resource metrics are nil")
	}

	raw, err := marshalProtoLine(&collmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{metricResourceFromSDK(rm)},
	})
	if err != nil {
		return err
	}

	return e.sink.writeRawLine(ctx, "metric", raw)
}

func (e *MetricExporter) ForceFlush(ctx context.Context) error {
	if e.done.Load() {
		return sdkmetric.ErrExporterShutdown
	}

	if err := e.sink.flush(ctx); err != nil {
		if err == errSinkClosed {
			return sdkmetric.ErrExporterShutdown
		}

		return err
	}

	return nil
}

func (e *MetricExporter) Shutdown(ctx context.Context) error {
	if e.done.Swap(true) {
		return nil
	}

	if err := e.sink.closeRef(ctx); err != nil {
		if err == errSinkClosed {
			return nil
		}

		return err
	}

	return nil
}

func metricResourceFromSDK(rm *metricdata.ResourceMetrics) *metricspb.ResourceMetrics {
	out := &metricspb.ResourceMetrics{
		Resource:  resourceToProto(rm.Resource),
		SchemaUrl: resourceSchemaURL(rm.Resource),
	}

	if len(rm.ScopeMetrics) == 0 {
		return out
	}

	out.ScopeMetrics = make([]*metricspb.ScopeMetrics, 0, len(rm.ScopeMetrics))

	for _, scopeMetrics := range rm.ScopeMetrics {
		entry := &metricspb.ScopeMetrics{
			Scope:     scopeToProto(scopeMetrics.Scope),
			SchemaUrl: scopeMetrics.Scope.SchemaURL,
		}

		if len(scopeMetrics.Metrics) > 0 {
			entry.Metrics = make([]*metricspb.Metric, 0, len(scopeMetrics.Metrics))
			for _, metric := range scopeMetrics.Metrics {
				entry.Metrics = append(entry.Metrics, metricEntryFromSDK(metric))
			}
		}

		out.ScopeMetrics = append(out.ScopeMetrics, entry)
	}

	return out
}

func metricEntryFromSDK(metric metricdata.Metrics) *metricspb.Metric {
	out := &metricspb.Metric{
		Name:        metric.Name,
		Description: metric.Description,
		Unit:        metric.Unit,
	}

	if data, ok := metric.Data.(metricdata.Gauge[int64]); ok {
		out.Data = &metricspb.Metric_Gauge{
			Gauge: &metricspb.Gauge{DataPoints: intNumberDataPointsFromSDK(data.DataPoints)},
		}

		return out
	}

	if data, ok := metric.Data.(metricdata.Gauge[float64]); ok {
		out.Data = &metricspb.Metric_Gauge{
			Gauge: &metricspb.Gauge{DataPoints: floatNumberDataPointsFromSDK(data.DataPoints)},
		}

		return out
	}

	if data, ok := metric.Data.(metricdata.Sum[int64]); ok {
		out.Data = &metricspb.Metric_Sum{
			Sum: &metricspb.Sum{
				AggregationTemporality: metricTemporalityToProto(data.Temporality),
				IsMonotonic:            data.IsMonotonic,
				DataPoints:             intNumberDataPointsFromSDK(data.DataPoints),
			},
		}

		return out
	}

	if data, ok := metric.Data.(metricdata.Sum[float64]); ok {
		out.Data = &metricspb.Metric_Sum{
			Sum: &metricspb.Sum{
				AggregationTemporality: metricTemporalityToProto(data.Temporality),
				IsMonotonic:            data.IsMonotonic,
				DataPoints:             floatNumberDataPointsFromSDK(data.DataPoints),
			},
		}

		return out
	}

	if data, ok := metric.Data.(metricdata.Histogram[int64]); ok {
		out.Data = &metricspb.Metric_Histogram{
			Histogram: &metricspb.Histogram{
				AggregationTemporality: metricTemporalityToProto(data.Temporality),
				DataPoints:             intHistogramDataPointsFromSDK(data.DataPoints),
			},
		}

		return out
	}

	if data, ok := metric.Data.(metricdata.Histogram[float64]); ok {
		out.Data = &metricspb.Metric_Histogram{
			Histogram: &metricspb.Histogram{
				AggregationTemporality: metricTemporalityToProto(data.Temporality),
				DataPoints:             floatHistogramDataPointsFromSDK(data.DataPoints),
			},
		}

		return out
	}

	if data, ok := metric.Data.(metricdata.ExponentialHistogram[int64]); ok {
		out.Data = &metricspb.Metric_ExponentialHistogram{
			ExponentialHistogram: &metricspb.ExponentialHistogram{
				AggregationTemporality: metricTemporalityToProto(data.Temporality),
				DataPoints:             intExponentialHistogramPointsFromSDK(data.DataPoints),
			},
		}

		return out
	}

	if data, ok := metric.Data.(metricdata.ExponentialHistogram[float64]); ok {
		out.Data = &metricspb.Metric_ExponentialHistogram{
			ExponentialHistogram: &metricspb.ExponentialHistogram{
				AggregationTemporality: metricTemporalityToProto(data.Temporality),
				DataPoints:             floatExponentialHistogramPointsFromSDK(data.DataPoints),
			},
		}

		return out
	}

	if data, ok := metric.Data.(metricdata.Summary); ok {
		out.Data = &metricspb.Metric_Summary{
			Summary: &metricspb.Summary{
				DataPoints: summaryDataPointsFromSDK(data.DataPoints),
			},
		}

		return out
	}

	out.Data = &metricspb.Metric_Gauge{
		Gauge: &metricspb.Gauge{},
	}

	return out
}

func intNumberDataPointsFromSDK(points []metricdata.DataPoint[int64]) []*metricspb.NumberDataPoint {
	out := make([]*metricspb.NumberDataPoint, 0, len(points))
	for _, point := range points {
		out = append(out, &metricspb.NumberDataPoint{
			Attributes:        attrSetToProto(point.Attributes),
			StartTimeUnixNano: timestampUnixNano(point.StartTime),
			TimeUnixNano:      timestampUnixNano(point.Time),
			Exemplars:         intExemplarsFromSDK(point.Exemplars),
			Value:             &metricspb.NumberDataPoint_AsInt{AsInt: point.Value},
		})
	}

	return out
}

func floatNumberDataPointsFromSDK(points []metricdata.DataPoint[float64]) []*metricspb.NumberDataPoint {
	out := make([]*metricspb.NumberDataPoint, 0, len(points))
	for _, point := range points {
		out = append(out, &metricspb.NumberDataPoint{
			Attributes:        attrSetToProto(point.Attributes),
			StartTimeUnixNano: timestampUnixNano(point.StartTime),
			TimeUnixNano:      timestampUnixNano(point.Time),
			Exemplars:         floatExemplarsFromSDK(point.Exemplars),
			Value:             &metricspb.NumberDataPoint_AsDouble{AsDouble: point.Value},
		})
	}

	return out
}

func intHistogramDataPointsFromSDK(points []metricdata.HistogramDataPoint[int64]) []*metricspb.HistogramDataPoint {
	out := make([]*metricspb.HistogramDataPoint, 0, len(points))
	for _, point := range points {
		entry := &metricspb.HistogramDataPoint{
			Attributes:        attrSetToProto(point.Attributes),
			StartTimeUnixNano: timestampUnixNano(point.StartTime),
			TimeUnixNano:      timestampUnixNano(point.Time),
			Count:             point.Count,
			BucketCounts:      append([]uint64(nil), point.BucketCounts...),
			ExplicitBounds:    append([]float64(nil), point.Bounds...),
			Exemplars:         intExemplarsFromSDK(point.Exemplars),
			Min:               optionalFloat64FromInt64(point.Min),
			Max:               optionalFloat64FromInt64(point.Max),
		}

		sum := float64(point.Sum)
		entry.Sum = &sum
		out = append(out, entry)
	}

	return out
}

func floatHistogramDataPointsFromSDK(points []metricdata.HistogramDataPoint[float64]) []*metricspb.HistogramDataPoint {
	out := make([]*metricspb.HistogramDataPoint, 0, len(points))
	for _, point := range points {
		entry := &metricspb.HistogramDataPoint{
			Attributes:        attrSetToProto(point.Attributes),
			StartTimeUnixNano: timestampUnixNano(point.StartTime),
			TimeUnixNano:      timestampUnixNano(point.Time),
			Count:             point.Count,
			BucketCounts:      append([]uint64(nil), point.BucketCounts...),
			ExplicitBounds:    append([]float64(nil), point.Bounds...),
			Exemplars:         floatExemplarsFromSDK(point.Exemplars),
			Min:               optionalFloat64FromFloat64(point.Min),
			Max:               optionalFloat64FromFloat64(point.Max),
		}

		sum := point.Sum
		entry.Sum = &sum
		out = append(out, entry)
	}

	return out
}

func intExponentialHistogramPointsFromSDK(
	points []metricdata.ExponentialHistogramDataPoint[int64],
) []*metricspb.ExponentialHistogramDataPoint {
	out := make([]*metricspb.ExponentialHistogramDataPoint, 0, len(points))
	for _, point := range points {
		entry := &metricspb.ExponentialHistogramDataPoint{
			Attributes:        attrSetToProto(point.Attributes),
			StartTimeUnixNano: timestampUnixNano(point.StartTime),
			TimeUnixNano:      timestampUnixNano(point.Time),
			Count:             point.Count,
			Scale:             point.Scale,
			ZeroCount:         point.ZeroCount,
			Positive: &metricspb.ExponentialHistogramDataPoint_Buckets{
				Offset:       point.PositiveBucket.Offset,
				BucketCounts: append([]uint64(nil), point.PositiveBucket.Counts...),
			},
			Negative: &metricspb.ExponentialHistogramDataPoint_Buckets{
				Offset:       point.NegativeBucket.Offset,
				BucketCounts: append([]uint64(nil), point.NegativeBucket.Counts...),
			},
			Exemplars:     intExemplarsFromSDK(point.Exemplars),
			Min:           optionalFloat64FromInt64(point.Min),
			Max:           optionalFloat64FromInt64(point.Max),
			ZeroThreshold: point.ZeroThreshold,
		}

		sum := float64(point.Sum)
		entry.Sum = &sum
		out = append(out, entry)
	}

	return out
}

func floatExponentialHistogramPointsFromSDK(
	points []metricdata.ExponentialHistogramDataPoint[float64],
) []*metricspb.ExponentialHistogramDataPoint {
	out := make([]*metricspb.ExponentialHistogramDataPoint, 0, len(points))
	for _, point := range points {
		entry := &metricspb.ExponentialHistogramDataPoint{
			Attributes:        attrSetToProto(point.Attributes),
			StartTimeUnixNano: timestampUnixNano(point.StartTime),
			TimeUnixNano:      timestampUnixNano(point.Time),
			Count:             point.Count,
			Scale:             point.Scale,
			ZeroCount:         point.ZeroCount,
			Positive: &metricspb.ExponentialHistogramDataPoint_Buckets{
				Offset:       point.PositiveBucket.Offset,
				BucketCounts: append([]uint64(nil), point.PositiveBucket.Counts...),
			},
			Negative: &metricspb.ExponentialHistogramDataPoint_Buckets{
				Offset:       point.NegativeBucket.Offset,
				BucketCounts: append([]uint64(nil), point.NegativeBucket.Counts...),
			},
			Exemplars:     floatExemplarsFromSDK(point.Exemplars),
			Min:           optionalFloat64FromFloat64(point.Min),
			Max:           optionalFloat64FromFloat64(point.Max),
			ZeroThreshold: point.ZeroThreshold,
		}

		sum := point.Sum
		entry.Sum = &sum
		out = append(out, entry)
	}

	return out
}

func summaryDataPointsFromSDK(points []metricdata.SummaryDataPoint) []*metricspb.SummaryDataPoint {
	out := make([]*metricspb.SummaryDataPoint, 0, len(points))
	for _, point := range points {
		entry := &metricspb.SummaryDataPoint{
			Attributes:        attrSetToProto(point.Attributes),
			StartTimeUnixNano: timestampUnixNano(point.StartTime),
			TimeUnixNano:      timestampUnixNano(point.Time),
			Count:             point.Count,
			Sum:               point.Sum,
		}

		if len(point.QuantileValues) > 0 {
			entry.QuantileValues = make([]*metricspb.SummaryDataPoint_ValueAtQuantile, 0, len(point.QuantileValues))
			for _, qv := range point.QuantileValues {
				entry.QuantileValues = append(entry.QuantileValues, &metricspb.SummaryDataPoint_ValueAtQuantile{
					Quantile: qv.Quantile,
					Value:    qv.Value,
				})
			}
		}

		out = append(out, entry)
	}

	return out
}

func intExemplarsFromSDK(exemplars []metricdata.Exemplar[int64]) []*metricspb.Exemplar {
	if len(exemplars) == 0 {
		return nil
	}

	out := make([]*metricspb.Exemplar, 0, len(exemplars))
	for _, exemplar := range exemplars {
		out = append(out, &metricspb.Exemplar{
			FilteredAttributes: attrSliceToProto(exemplar.FilteredAttributes),
			TimeUnixNano:       timestampUnixNano(exemplar.Time),
			SpanId:             append([]byte(nil), exemplar.SpanID...),
			TraceId:            append([]byte(nil), exemplar.TraceID...),
			Value:              &metricspb.Exemplar_AsInt{AsInt: exemplar.Value},
		})
	}

	return out
}

func floatExemplarsFromSDK(exemplars []metricdata.Exemplar[float64]) []*metricspb.Exemplar {
	if len(exemplars) == 0 {
		return nil
	}

	out := make([]*metricspb.Exemplar, 0, len(exemplars))
	for _, exemplar := range exemplars {
		entry := &metricspb.Exemplar{
			FilteredAttributes: attrSliceToProto(exemplar.FilteredAttributes),
			TimeUnixNano:       timestampUnixNano(exemplar.Time),
			SpanId:             append([]byte(nil), exemplar.SpanID...),
			TraceId:            append([]byte(nil), exemplar.TraceID...),
			Value:              &metricspb.Exemplar_AsDouble{AsDouble: exemplar.Value},
		}

		out = append(out, entry)
	}

	return out
}
