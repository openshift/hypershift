package internal

import (
	"sync"
	"time"
)

// mimics senders.MetricSender to avoid circular dependency
type internalSender interface {
	// Sends a single metric to Wavefront with optional timestamp and tags.
	SendMetric(name string, value float64, ts int64, source string, tags map[string]string) error

	// Sends a delta counter (counter aggregated at the Wavefront service) to Wavefront.
	// the timestamp for a delta counter is assigned at the server side.
	SendDeltaCounter(name string, value float64, source string, tags map[string]string) error
}

// metric registry for internal metrics
type MetricRegistry struct {
	source       string
	prefix       string
	tags         map[string]string
	reportTicker *time.Ticker
	sender       internalSender
	done         chan struct{}

	mtx     sync.Mutex
	metrics map[string]interface{}
}

type RegistryOption func(*MetricRegistry)

func SetSource(source string) RegistryOption {
	return func(registry *MetricRegistry) {
		registry.source = source
	}
}

func SetInterval(interval time.Duration) RegistryOption {
	return func(registry *MetricRegistry) {
		registry.reportTicker = time.NewTicker(interval)
	}
}

func SetTags(tags map[string]string) RegistryOption {
	return func(registry *MetricRegistry) {
		registry.tags = tags
	}
}

func SetTag(key, value string) RegistryOption {
	return func(registry *MetricRegistry) {
		if registry.tags == nil {
			registry.tags = make(map[string]string)
		}
		registry.tags[key] = value
	}
}

func SetPrefix(prefix string) RegistryOption {
	return func(registry *MetricRegistry) {
		registry.prefix = prefix
	}
}

func NewMetricRegistry(sender internalSender, setters ...RegistryOption) *MetricRegistry {
	registry := &MetricRegistry{
		sender:       sender,
		metrics:      make(map[string]interface{}),
		reportTicker: time.NewTicker(time.Second * 60),
		done:         make(chan struct{}),
	}
	for _, setter := range setters {
		setter(registry)
	}
	return registry
}

func (registry *MetricRegistry) NewCounter(name string) *MetricCounter {
	return registry.getOrAdd(name, &MetricCounter{}).(*MetricCounter)
}

func (registry *MetricRegistry) NewDeltaCounter(name string) *DeltaCounter {
	return registry.getOrAdd(name, &DeltaCounter{MetricCounter{}}).(*DeltaCounter)
}

func (registry *MetricRegistry) NewGauge(name string, f func() int64) *FunctionalGauge {
	return registry.getOrAdd(name, &FunctionalGauge{value: f}).(*FunctionalGauge)
}

func (registry *MetricRegistry) NewGaugeFloat64(name string, f func() float64) *FunctionalGaugeFloat64 {
	return registry.getOrAdd(name, &FunctionalGaugeFloat64{value: f}).(*FunctionalGaugeFloat64)
}

func (registry *MetricRegistry) Start() {
	go registry.start()
}

func (registry *MetricRegistry) start() {
	for {
		select {
		case <-registry.reportTicker.C:
			registry.report()
		case <-registry.done:
			return
		}
	}
}

func (registry *MetricRegistry) Stop() {
	registry.reportTicker.Stop()
	registry.done <- struct{}{}
}

func (registry *MetricRegistry) report() {
	registry.mtx.Lock()
	defer registry.mtx.Unlock()

	for k, metric := range registry.metrics {
		switch metric.(type) {
		case *DeltaCounter:
			deltaCount := metric.(*DeltaCounter).count()
			registry.sender.SendDeltaCounter(registry.prefix+"."+k, float64(deltaCount), "", registry.tags)
			metric.(*DeltaCounter).dec(deltaCount)
		case *MetricCounter:
			registry.sender.SendMetric(registry.prefix+"."+k, float64(metric.(*MetricCounter).count()), 0, "", registry.tags)
		case *FunctionalGauge:
			registry.sender.SendMetric(registry.prefix+"."+k, float64(metric.(*FunctionalGauge).instantValue()), 0, "", registry.tags)
		case *FunctionalGaugeFloat64:
			registry.sender.SendMetric(registry.prefix+"."+k, metric.(*FunctionalGaugeFloat64).instantValue(), 0, "", registry.tags)
		}
	}
}

func (registry *MetricRegistry) getOrAdd(name string, metric interface{}) interface{} {
	registry.mtx.Lock()
	defer registry.mtx.Unlock()

	if val, ok := registry.metrics[name]; ok {
		return val
	}
	registry.metrics[name] = metric
	return metric
}
