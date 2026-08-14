package metricsproxy

import (
	"fmt"

	dto "github.com/prometheus/client_model/go"
)

type Labeler struct {
	namespace string
}

func NewLabeler(namespace string) *Labeler {
	return &Labeler{namespace: namespace}
}

func (l *Labeler) Inject(families map[string]*dto.MetricFamily, target ScrapeTarget, componentName string, cfg ComponentConfig) map[string]*dto.MetricFamily {
	job := cfg.MetricsJob
	if job == "" {
		job = componentName
	}
	namespace := cfg.MetricsNamespace
	if namespace == "" {
		namespace = l.namespace
	}
	service := cfg.MetricsService
	if service == "" {
		service = componentName
	}
	endpoint := cfg.MetricsEndpoint
	if endpoint == "" {
		endpoint = "metrics"
	}

	extraLabels := map[string]string{
		"pod":       target.PodName,
		"namespace": namespace,
		"job":       job,
		"service":   service,
		"endpoint":  endpoint,
		"instance":  fmt.Sprintf("%s:%d", target.PodIP, target.Port),
	}

	for _, mf := range families {
		for _, m := range mf.Metric {
			for k, v := range extraLabels {
				if !hasLabel(m, k) {
					name := k
					value := v
					m.Label = append(m.Label, &dto.LabelPair{
						Name:  &name,
						Value: &value,
					})
				}
			}
		}
	}

	return families
}

func hasLabel(m *dto.Metric, name string) bool {
	for _, lp := range m.Label {
		if lp.GetName() == name {
			return true
		}
	}
	return false
}
