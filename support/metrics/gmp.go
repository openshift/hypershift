package metrics

import (
	"fmt"
	"strings"

	"github.com/openshift/hypershift/support/gmpmonitoring"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

const (
	// GMPNamespacePlaceholder is the literal the generated GMP assets use in
	// place of the control-plane namespace. The component framework rewrites
	// metadata.namespace, but it does not rewrite spec fields such as
	// tls.serverName, so whatever reconciles a staged asset substitutes the live
	// namespace here first.
	GMPNamespacePlaceholder = "HCP_NAMESPACE"

	// GMPClusterIDLabel is the label telemetry uses to identify a cluster. It is
	// appended to every GMP endpoint exactly like the CoreOS adapters append it
	// through support/util.
	GMPClusterIDLabel = "_id"
)

// gmpTargetLabels are the target labels prometheus-operator adds and GMP does
// not. The asset generator reproduces them with constant metric relabeling
// rules at the start of each endpoint. They do not depend on the MetricsSet, so
// the runtime adapter keeps them when it replaces the filter rules.
//
// container is only constant for an endpoint whose port is written as a number:
// GMP fills container for a port referenced by name, but not for a number, so
// the generator pins it to the one container that exposes the port.
var gmpTargetLabels = map[string]struct{}{
	"service":   {},
	"endpoint":  {},
	"container": {},
}

// GMPMetricRelabelingAdapters maps a component asset directory to the adapter
// that owns each of its endpoints' CoreOS metric relabelings.
//
// A generated PodMonitoring is a snapshot of the static asset, so it can only
// encode the Telemetry MetricsSet. The GMP runtime adapter replaces the staged
// filter rules with the adapter output for the cluster's set before
// reconciling, keeps the staged target-label rules, and appends the cluster ID
// label, exactly like the CoreOS adapters do. Components
// whose CoreOS adapter only adds relabelings at reconcile time have no staged
// block but still need an entry here, otherwise their metrics reach GMP
// unfiltered.
var GMPMetricRelabelingAdapters = map[string][]func(MetricsSet) []prometheusoperatorv1.RelabelConfig{
	"catalog-operator":                   {CatalogOperatorRelabelConfigs},
	"cluster-image-registry-operator":    {RegistryOperatorRelabelConfigs},
	"cluster-node-tuning-operator":       {NTORelabelConfigs},
	"cluster-version-operator":           {CVORelabelConfigs},
	"control-plane-operator":             {ControlPlaneOperatorRelabelConfigs},
	"etcd":                               {EtcdRelabelConfigs},
	"hosted-cluster-config-operator":     {HostedClusterConfigOperatorRelabelConfigs},
	"ingress-operator":                   {RegistryOperatorRelabelConfigs},
	"kube-apiserver":                     {KASRelabelConfigs},
	"kube-controller-manager":            {KCMRelabelConfigs},
	"kube-scheduler":                     {SchedulerRelabelConfigs, SchedulerResourceRelabelConfigs},
	"olm-operator":                       {OLMRelabelConfigs},
	"openshift-apiserver":                {OpenShiftAPIServerRelabelConfigs},
	"openshift-controller-manager":       {OpenShiftControllerManagerRelabelConfigs},
	"openshift-route-controller-manager": {OpenShiftRouteControllerManagerRelabelConfigs},
}

// AdaptGMPPodMonitoring prepares a staged PodMonitoring for a live cluster: it
// substitutes the control-plane namespace, replaces each endpoint's metric
// filter rules with the adapter output for the given MetricsSet, and appends the
// cluster ID label. The staged filter rules are only a snapshot of the Telemetry
// set, so leaving them in place would both ignore the cluster's MetricsSet and
// drop the cluster ID. The staged target-label rules are kept ahead of the
// adapter output, where prometheus-operator's target labels would be.
func AdaptGMPPodMonitoring(componentName string, podMonitoring *gmpmonitoring.PodMonitoring, set MetricsSet, clusterID, namespace string) error {
	if podMonitoring == nil {
		return fmt.Errorf("podMonitoring is nil")
	}

	podMonitoring.Namespace = namespace

	adapters := GMPMetricRelabelingAdapters[componentName]
	for i := range podMonitoring.Spec.Endpoints {
		endpoint := &podMonitoring.Spec.Endpoints[i]
		if endpoint.TLS != nil {
			endpoint.TLS.ServerName = strings.ReplaceAll(endpoint.TLS.ServerName, GMPNamespacePlaceholder, namespace)
		}

		if i < len(adapters) && adapters[i] != nil {
			relabeling, err := ToGMPRelabelingRules(adapters[i](set))
			if err != nil {
				return fmt.Errorf("%s endpoint %d: %w", componentName, i, err)
			}
			endpoint.MetricRelabeling = append(gmpTargetLabelRules(endpoint.MetricRelabeling), relabeling...)
		}

		ApplyGMPClusterIDLabel(endpoint, clusterID)
	}
	return nil
}

// gmpTargetLabelRules returns the constant rules the asset generator added for
// the labels in gmpTargetLabels.
func gmpTargetLabelRules(rules []gmpmonitoring.RelabelingRule) []gmpmonitoring.RelabelingRule {
	var kept []gmpmonitoring.RelabelingRule
	for _, rule := range rules {
		if _, ok := gmpTargetLabels[rule.TargetLabel]; !ok {
			continue
		}
		if (rule.Action == "" || rule.Action == "replace") && len(rule.SourceLabels) == 0 && rule.Regex == "" && rule.Replacement != "" {
			kept = append(kept, rule)
		}
	}
	return kept
}

// ApplyGMPClusterIDLabel appends the cluster ID relabel already applied by
// support/util.ApplyClusterIDLabel on the CoreOS path.
func ApplyGMPClusterIDLabel(endpoint *gmpmonitoring.ScrapeEndpoint, clusterID string) {
	if clusterID == "" {
		return
	}
	endpoint.MetricRelabeling = append(endpoint.MetricRelabeling, gmpmonitoring.RelabelingRule{
		TargetLabel: GMPClusterIDLabel,
		Replacement: clusterID,
		Action:      "replace",
	})
}

// ToGMPRelabelingRules translates CoreOS relabel configs into the restricted GMP
// form. It applies the validation and rewrites the asset generator uses, so a
// runtime adapter cannot emit a rule GMP's admission or collector rejects: a
// rule with no GMP-safe equivalent fails the reconcile here instead.
func ToGMPRelabelingRules(source []prometheusoperatorv1.RelabelConfig) ([]gmpmonitoring.RelabelingRule, error) {
	source, err := SanitizeGMPRelabelConfigs(source)
	if err != nil {
		return nil, err
	}
	if len(source) == 0 {
		return nil, nil
	}

	result := make([]gmpmonitoring.RelabelingRule, 0, len(source))
	for i, rule := range source {
		action := strings.ToLower(rule.Action)
		if !SupportedGMPRelabelAction(action) {
			return nil, fmt.Errorf("metric relabeling %d: action %q is not supported by GMP", i, rule.Action)
		}

		// An omitted separator or replacement keeps the GMP default, which
		// matches the Prometheus default the CoreOS config relied on. Only an
		// explicitly empty value is unrepresentable, because GMP treats the
		// field as unset.
		if rule.Separator != nil && *rule.Separator == "" {
			return nil, fmt.Errorf("metric relabeling %d: empty separator cannot be preserved by GMP", i)
		}
		if rule.Replacement != nil && *rule.Replacement == "" {
			return nil, fmt.Errorf("metric relabeling %d: empty replacement cannot be preserved by GMP", i)
		}
		if action == "labeldrop" && rule.Regex == "" {
			return nil, fmt.Errorf("metric relabeling %d: labeldrop must set a regex, which the GMP API requires", i)
		}

		gmpRule := gmpmonitoring.RelabelingRule{
			TargetLabel: rule.TargetLabel,
			Regex:       rule.Regex,
			Modulus:     rule.Modulus,
			Replacement: stringValue(rule.Replacement),
			Separator:   stringValue(rule.Separator),
			Action:      action,
		}
		for _, label := range rule.SourceLabels {
			gmpRule.SourceLabels = append(gmpRule.SourceLabels, string(label))
		}
		result = append(result, gmpRule)
	}
	return result, nil
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
