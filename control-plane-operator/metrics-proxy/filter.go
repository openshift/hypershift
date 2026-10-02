package metricsproxy

import (
	"regexp"
	"sync"

	"github.com/openshift/hypershift/support/metrics"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	dto "github.com/prometheus/client_model/go"
)

// matchNothing is a regex that never matches, used as a fail-closed fallback
// when a keep-regex fails to compile.
var matchNothing = regexp.MustCompile("a^")

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

	regexStr, hasConfig := getFilterRegexForComponent(componentName, f.metricsSet)
	if !hasConfig {
		f.mu.Lock()
		f.cache[componentName] = nil
		f.mu.Unlock()
		return nil
	}
	if regexStr == "" {
		// Component has a drop-all rule: block everything.
		f.mu.Lock()
		f.cache[componentName] = matchNothing
		f.mu.Unlock()
		return matchNothing
	}

	compiled, err := regexp.Compile("^(" + regexStr + ")$")
	if err != nil {
		// Fail closed: if the regex can't compile, block all metrics rather
		// than allowing unfiltered access.
		f.mu.Lock()
		f.cache[componentName] = matchNothing
		f.mu.Unlock()
		return matchNothing
	}

	f.mu.Lock()
	f.cache[componentName] = compiled
	f.mu.Unlock()
	return compiled
}

// getFilterRegexForComponent returns the keep regex for a component and whether
// any relabel config exists. When a drop-all rule is found (regex matches
// everything), it returns ("", true) to signal that all metrics should be
// blocked. Selective drop rules are not supported by this filter and are
// treated as having no config.
func getFilterRegexForComponent(componentName string, metricsSet metrics.MetricsSet) (string, bool) {
	configs := getRelabelConfigsForComponent(componentName, metricsSet)
	for _, rc := range configs {
		if rc.Action == "keep" {
			return rc.Regex, true
		}
		if rc.Action == "drop" && isDropAllRegex(rc.Regex) {
			return "", true
		}
	}
	return "", false
}

func isDropAllRegex(regex string) bool {
	return regex == ".*" || regex == "(.*)"
}

func getRelabelConfigsForComponent(componentName string, metricsSet metrics.MetricsSet) []prometheusoperatorv1.RelabelConfig {
	switch componentName {
	case "kube-apiserver":
		return metrics.KASRelabelConfigs(metricsSet)
	case "etcd":
		return metrics.EtcdRelabelConfigs(metricsSet)
	case "kube-controller-manager":
		return metrics.KCMRelabelConfigs(metricsSet)
	case "kube-scheduler":
		return metrics.SchedulerRelabelConfigs(metricsSet)
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
