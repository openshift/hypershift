package metrics

const (
	Namespace   = "operator"
	LabelGroup  = "group"
	LabelKind   = "kind"
	LabelType   = "type"
	LabelReason = "reason"
)

type ObservationMetric interface {
	Observe(v float64, labels map[string]string)
	Delete(labels map[string]string)
	DeletePartialMatch(labels map[string]string)
	Reset()
	// Labels returns the dimensions the metric was declared with.
	Labels() []Label
	// Stage returns the metric's API stability.
	Stage() Stage
}

type CounterMetric interface {
	Add(v float64, labels map[string]string)
	Inc(labels map[string]string)
	Delete(labels map[string]string)
	DeletePartialMatch(labels map[string]string)
	Reset()
	// Labels returns the dimensions the metric was declared with.
	Labels() []Label
	// Stage returns the metric's API stability.
	Stage() Stage
}

type GaugeMetric interface {
	Set(v float64, labels map[string]string)
	Delete(labels map[string]string)
	DeletePartialMatch(labels map[string]string)
	Reset()
	// Labels returns the dimensions the metric was declared with.
	Labels() []Label
	// Stage returns the metric's API stability.
	Stage() Stage
}
