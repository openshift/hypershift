package metrics

type MultiCounter struct {
	counters []CounterMetric
}

func NewMultiCounter(counters ...CounterMetric) CounterMetric {
	return &MultiCounter{counters: counters}
}

func (mc *MultiCounter) Inc(labels map[string]string) {
	for _, c := range mc.counters {
		c.Inc(labels)
	}
}

func (mc *MultiCounter) Add(v float64, labels map[string]string) {
	for _, c := range mc.counters {
		c.Add(v, labels)
	}
}

func (mc *MultiCounter) Delete(labels map[string]string) {
	for _, c := range mc.counters {
		c.Delete(labels)
	}
}

func (mc *MultiCounter) DeletePartialMatch(labels map[string]string) {
	for _, c := range mc.counters {
		c.DeletePartialMatch(labels)
	}
}

func (mc *MultiCounter) Reset() {
	for _, c := range mc.counters {
		c.Reset()
	}
}

func (mc *MultiCounter) Labels() []Label {
	if len(mc.counters) == 0 {
		return nil
	}
	return mc.counters[0].Labels()
}

func (mc *MultiCounter) Stage() Stage {
	if len(mc.counters) == 0 {
		return ""
	}
	return mc.counters[0].Stage()
}

type MultiGauge struct {
	gauges []GaugeMetric
}

func NewMultiGauge(gauges ...GaugeMetric) GaugeMetric {
	return &MultiGauge{gauges: gauges}
}

func (mg *MultiGauge) Set(v float64, labels map[string]string) {
	for _, g := range mg.gauges {
		g.Set(v, labels)
	}
}

func (mg *MultiGauge) Delete(labels map[string]string) {
	for _, g := range mg.gauges {
		g.Delete(labels)
	}
}

func (mg *MultiGauge) DeletePartialMatch(labels map[string]string) {
	for _, g := range mg.gauges {
		g.DeletePartialMatch(labels)
	}
}

func (mg *MultiGauge) Reset() {
	for _, g := range mg.gauges {
		g.Reset()
	}
}

func (mg *MultiGauge) Labels() []Label {
	if len(mg.gauges) == 0 {
		return nil
	}
	return mg.gauges[0].Labels()
}

func (mg *MultiGauge) Stage() Stage {
	if len(mg.gauges) == 0 {
		return ""
	}
	return mg.gauges[0].Stage()
}

type MultiObservation struct {
	observations []ObservationMetric
}

func NewMultiObservation(observations ...ObservationMetric) ObservationMetric {
	return &MultiObservation{observations: observations}
}

func (mo *MultiObservation) Observe(v float64, labels map[string]string) {
	for _, o := range mo.observations {
		o.Observe(v, labels)
	}
}

func (mo *MultiObservation) Delete(labels map[string]string) {
	for _, o := range mo.observations {
		o.Delete(labels)
	}
}

func (mo *MultiObservation) DeletePartialMatch(labels map[string]string) {
	for _, o := range mo.observations {
		o.DeletePartialMatch(labels)
	}
}

func (mo *MultiObservation) Reset() {
	for _, o := range mo.observations {
		o.Reset()
	}
}

func (mo *MultiObservation) Labels() []Label {
	if len(mo.observations) == 0 {
		return nil
	}
	return mo.observations[0].Labels()
}

func (mo *MultiObservation) Stage() Stage {
	if len(mo.observations) == 0 {
		return ""
	}
	return mo.observations[0].Stage()
}
