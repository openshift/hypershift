package senders

import (
	"github.com/wavefronthq/wavefront-sdk-go/event"
	"github.com/wavefronthq/wavefront-sdk-go/histogram"
)

type SpanTag struct {
	Key   string
	Value string
}

type SpanLog struct {
	Timestamp int64             `json:"timestamp"`
	Fields    map[string]string `json:"fields"`
}

type SpanLogs struct {
	TraceId string    `json:"traceId"`
	SpanId  string    `json:"spanId"`
	Logs    []SpanLog `json:"logs"`
}

// MetricSender Interface for sending metrics to Wavefront
type MetricSender interface {
	// Sends a single metric to Wavefront with optional timestamp and tags.
	SendMetric(name string, value float64, ts int64, source string, tags map[string]string) error

	// Sends a delta counter (counter aggregated at the Wavefront service) to Wavefront.
	// the timestamp for a delta counter is assigned at the server side.
	SendDeltaCounter(name string, value float64, source string, tags map[string]string) error
}

// DistributionSender Interface for sending distributions to Wavefront
type DistributionSender interface {
	// Sends a distribution of metrics to Wavefront with optional timestamp and tags.
	// Each centroid is a 2-dimensional entity with the first dimension the mean value
	// and the second dimension the count of points in the centroid.
	// The granularity informs the set of intervals (minute, hour, and/or day) by which the
	// histogram data should be aggregated.
	SendDistribution(name string, centroids []histogram.Centroid, hgs map[histogram.Granularity]bool, ts int64, source string, tags map[string]string) error
}

// SpanSender Interface for sending tracing spans to Wavefront
type SpanSender interface {
	// Sends a tracing span to Wavefront.
	// traceId, spanId, parentIds and preceding spanIds are expected to be UUID strings.
	// parents and preceding spans can be empty for a root span.
	// span tag keys can be repeated (example: "user"="foo" and "user"="bar")
	// span logs are currently omitted
	SendSpan(name string, startMillis, durationMillis int64, source, traceId, spanId string, parents, followsFrom []string, tags []SpanTag, spanLogs []SpanLog) error
}

// EventSender Interface for sending events to Wavefront. NOT yet supported.
type EventSender interface {
	// Sends an event to Wavefront with optional tags
	SendEvent(name string, startMillis, endMillis int64, source string, tags map[string]string, setters ...event.Option) error
}
