package internal

import "sync/atomic"

// counter for internal metrics
type MetricCounter struct {
	value int64
}

func (c *MetricCounter) Inc() {
	atomic.AddInt64(&c.value, 1)
}

func (c *MetricCounter) dec(n int64) {
	atomic.AddInt64(&c.value, -n)
}

func (c *MetricCounter) count() int64 {
	return atomic.LoadInt64(&c.value)
}

type DeltaCounter struct {
	MetricCounter
}

// functional gauge for internal metrics
type FunctionalGauge struct {
	value func() int64
}

func (g *FunctionalGauge) instantValue() int64 {
	return g.value()
}

// functional gauge for internal metrics
type FunctionalGaugeFloat64 struct {
	value func() float64
}

func (g *FunctionalGaugeFloat64) instantValue() float64 {
	return g.value()
}
