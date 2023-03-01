package senders

import (
	"github.com/wavefronthq/wavefront-sdk-go/event"
	"github.com/wavefronthq/wavefront-sdk-go/histogram"
)

type wavefrontNoOpSender struct {
}

var (
	defaultNoopClient Sender = &wavefrontNoOpSender{}
)

// NewWavefrontNoOpClient returns a Wavefront Client instance for which all operations are no-ops.
func NewWavefrontNoOpClient() (Sender, error) {
	return defaultNoopClient, nil
}

func (sender *wavefrontNoOpSender) Start() {
	// no-op
}

func (sender *wavefrontNoOpSender) SendMetric(name string, value float64, ts int64, source string, tags map[string]string) error {
	return nil
}

func (sender *wavefrontNoOpSender) SendDeltaCounter(name string, value float64, source string, tags map[string]string) error {
	return nil
}

func (sender *wavefrontNoOpSender) SendDistribution(name string, centroids []histogram.Centroid,
	hgs map[histogram.Granularity]bool, ts int64, source string, tags map[string]string) error {
	return nil
}

func (sender *wavefrontNoOpSender) SendSpan(name string, startMillis, durationMillis int64, source, traceId, spanId string,
	parents, followsFrom []string, tags []SpanTag, spanLogs []SpanLog) error {
	return nil
}

func (sender *wavefrontNoOpSender) SendEvent(name string, startMillis, endMillis int64, source string, tags map[string]string, setters ...event.Option) error {
	return nil
}

func (sender *wavefrontNoOpSender) Close() {
	// no-op
}

func (sender *wavefrontNoOpSender) Flush() error {
	return nil
}

func (sender *wavefrontNoOpSender) GetFailureCount() int64 {
	return 0
}
