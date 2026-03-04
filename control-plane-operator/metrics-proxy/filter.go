package metricsproxy

import (
	"regexp"
	"sync"

	"github.com/openshift/hypershift/support/metrics"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	dto "github.com/prometheus/client_model/go"
)

type Filter struct {
	metricsSet metrics.MetricsSet

	mu    sync.RWMutex
	cache map[string]*regexp.Regexp
}

func NewFilter(metricsSet metrics.MetricsSet) *Filter {
	return &Filter{
		metricsSet: metricsSet,
		cache:      make(map[string]*regexp.Regexp),
	}
}

func (f *Filter) Apply(componentName string, families map[string]*dto.MetricFamily) map[string]*dto.MetricFamily {
	if f.metricsSet == metrics.MetricsSetAll {
		return families
	}

	filter := f.getOrCompile(componentName)
	if filter == nil {
		return families
	}

	filtered := make(map[string]*dto.MetricFamily)
	for name, mf := range families {
		if filter.MatchString(name) {
			filtered[name] = mf
		}
	}
	return filtered
}

func (f *Filter) getOrCompile(componentName string) *regexp.Regexp {
	f.mu.RLock()
	if compiled, ok := f.cache[componentName]; ok {
		f.mu.RUnlock()
		return compiled
	}
	f.mu.RUnlock()

	regexStr := getKeepRegexForComponent(componentName, f.metricsSet)
	if regexStr == "" {
		f.mu.Lock()
		f.cache[componentName] = nil
		f.mu.Unlock()
		return nil
	}

	compiled, err := regexp.Compile("^(" + regexStr + ")$")
	if err != nil {
		return nil
	}

	f.mu.Lock()
	f.cache[componentName] = compiled
	f.mu.Unlock()
	return compiled
}

func getKeepRegexForComponent(componentName string, metricsSet metrics.MetricsSet) string {
	configs := getRelabelConfigsForComponent(componentName, metricsSet)
	for _, rc := range configs {
		if rc.Action == "keep" {
			return rc.Regex
		}
	}
	return ""
}

func getRelabelConfigsForComponent(componentName string, metricsSet metrics.MetricsSet) []prometheusoperatorv1.RelabelConfig {
	switch componentName {
	case "kube-apiserver":
		return metrics.KASRelabelConfigs(metricsSet)
	case "etcd":
		return metrics.EtcdRelabelConfigs(metricsSet)
	case "kube-controller-manager":
		return metrics.KCMRelabelConfigs(metricsSet)
	case "openshift-apiserver":
		return metrics.OpenShiftAPIServerRelabelConfigs(metricsSet)
	case "openshift-controller-manager":
		return metrics.OpenShiftControllerManagerRelabelConfigs(metricsSet)
	case "openshift-route-controller-manager":
		return metrics.OpenShiftRouteControllerManagerRelabelConfigs(metricsSet)
	case "cluster-version-operator":
		return metrics.CVORelabelConfigs(metricsSet)
	case "olm-operator":
		return metrics.OLMRelabelConfigs(metricsSet)
	case "catalog-operator":
		return metrics.CatalogOperatorRelabelConfigs(metricsSet)
	case "node-tuning-operator":
		return metrics.NTORelabelConfigs(metricsSet)
	default:
		return nil
	}
}
