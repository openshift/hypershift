package gmpparity

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/openshift/hypershift/support/gmpmonitoring"
	"github.com/openshift/hypershift/support/metrics"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

// perTargetValue stands for a target label whose value comes from the scraped
// pod (namespace, pod, container) rather than from the monitor, so only its
// presence can be compared offline.
const perTargetValue = "<per-target>"

// gmpDefaultMetadataLabels is what GMP adds when a PodMonitoring leaves
// targetLabels.metadata unset.
var gmpDefaultMetadataLabels = []string{"container", "pod", "top_level_controller_name", "top_level_controller_type"}

// TestGeneratedTargetLabelsMatchCoreOS checks that every generated
// PodMonitoring endpoint produces the same target labels as the CoreOS
// endpoint it mirrors, so dashboards and rules see the same label set from
// either backend. The CoreOS labels are derived independently of the generator,
// from the labels prometheus-operator adds to ServiceMonitor and PodMonitor
// targets.
//
// instance is excluded: GMP always sets it to <pod>:<port name> and does not
// allow relabeling it, while prometheus-operator uses <pod IP>:<port>. A
// PodMonitor's job label is the other unavoidable difference, pinned below.
func TestGeneratedTargetLabelsMatchCoreOS(t *testing.T) {
	inventory := buildInventory(t)

	generated := make(map[string]*gmpmonitoring.PodMonitoring, len(inventory.podMonitorings))
	for _, podMonitoring := range inventory.podMonitorings {
		generated[podMonitoring.Component] = podMonitoring.Monitor
	}

	checked := 0
	for _, source := range inventory.serviceMonitors {
		podMonitoring, ok := generated[source.Component]
		if !ok {
			// Missing coverage is reported by TestGMPMonitoringParity.
			continue
		}
		service, err := inventory.selectedService(source.Monitor)
		if err != nil {
			t.Fatalf("%s: %v", source.Component, err)
		}
		var expected []map[string]string
		for _, endpoint := range source.Monitor.Spec.Endpoints {
			expected = append(expected, serviceMonitorTargetLabels(source.Monitor, service, endpoint))
		}
		assertTargetLabels(t, inventory, source.Component, expected, podMonitoring)
		checked++
	}

	for _, source := range inventory.podMonitors {
		podMonitoring, ok := generated[source.Component]
		if !ok {
			continue
		}
		var expected []map[string]string
		for _, endpoint := range source.Monitor.Spec.PodMetricsEndpoints {
			labels := podMonitorTargetLabels(source.Monitor, endpoint)
			// prometheus-operator names a PodMonitor job <namespace>/<name>,
			// which a PodMonitoring name cannot hold. The generator keeps the
			// monitor name; this pins that documented difference.
			labels["job"] = source.Monitor.Name
			expected = append(expected, labels)
		}
		assertTargetLabels(t, inventory, source.Component, expected, podMonitoring)
		checked++
	}

	if checked == 0 {
		t.Fatal("no generated PodMonitoring was compared; the test would pass vacuously")
	}
}

// TestAdaptedPodMonitoringKeepsTargetLabels runs the runtime GMP adapter over
// every staged PodMonitoring for every MetricsSet and checks that the target
// labels survive. The adapter replaces the staged filter rules, so without this
// a change to it could silently drop the service and endpoint labels that
// TestGeneratedTargetLabelsMatchCoreOS proves the staged assets carry.
func TestAdaptedPodMonitoringKeepsTargetLabels(t *testing.T) {
	const clusterID = "cluster-id"

	checked := 0
	for _, component := range componentAssetsForTest(t) {
		staged := loadGeneratedPodMonitoring(t, component)
		if staged == nil {
			continue
		}
		expected := podMonitoringTargetLabels(staged)
		for _, labels := range expected {
			labels[metrics.GMPClusterIDLabel] = clusterID
		}

		for _, set := range []metrics.MetricsSet{metrics.MetricsSetTelemetry, metrics.MetricsSetSRE, metrics.MetricsSetAll} {
			adapted := staged.DeepCopy()
			if err := metrics.AdaptGMPPodMonitoring(component.Dir, adapted, set, clusterID, hcpNamespace); err != nil {
				t.Fatalf("%s %s: %v", component.Name, set, err)
			}
			actual := podMonitoringTargetLabels(adapted)
			if !reflect.DeepEqual(expected, actual) {
				t.Errorf("%s %s: adapted PodMonitoring target labels differ from the staged asset\nstaged:  %v\nadapted: %v", component.Name, set, expected, actual)
			}
			checked++
		}
	}

	if checked == 0 {
		t.Fatal("no staged PodMonitoring was adapted; the test would pass vacuously")
	}
}

func assertTargetLabels(t *testing.T, inventory *clusterInventory, component string, expected []map[string]string, podMonitoring *gmpmonitoring.PodMonitoring) {
	t.Helper()

	actual := podMonitoringTargetLabels(podMonitoring)
	if len(expected) != len(actual) {
		t.Errorf("%s: CoreOS monitor has %d endpoints, generated PodMonitoring has %d", component, len(expected), len(actual))
		return
	}
	for i := range expected {
		// GMP fills container only for a port referenced by name, so the
		// generator writes a constant for a port written as a number. CoreOS
		// takes the container from service discovery; the constant is equivalent
		// only when it names the one container that exposes the port.
		if container := actual[i]["container"]; container != perTargetValue &&
			inventory.portOwnedByContainer(component, podMonitoring.Spec.Endpoints[i].Port, container) {
			actual[i]["container"] = perTargetValue
		}
		if !reflect.DeepEqual(expected[i], actual[i]) {
			t.Errorf("%s endpoint %d: generated PodMonitoring target labels differ from CoreOS\nCoreOS: %v\nGMP:    %v", component, i, expected[i], actual[i])
		}
	}
}

// portOwnedByContainer reports whether every pod of a component that exposes the
// port exposes it from exactly the named container, and at least one does.
func (inv *clusterInventory) portOwnedByContainer(component string, port intstr.IntOrString, container string) bool {
	found := false
	for _, pod := range inv.pods {
		if pod.Component != component {
			continue
		}
		resolved, err := resolveContainerPort(port, pod)
		if err != nil {
			continue
		}
		owners := pod.PortContainers[resolved]
		if len(owners) != 1 || owners[0] != container {
			return false
		}
		found = true
	}
	return found
}

// selectedService returns the single Service a ServiceMonitor selects.
func (inv *clusterInventory) selectedService(monitor *prometheusoperatorv1.ServiceMonitor) (*corev1.Service, error) {
	namespaces := monitor.Spec.NamespaceSelector.MatchNames
	if len(namespaces) == 0 {
		namespaces = []string{monitor.Namespace}
	}

	var matched []*corev1.Service
	for _, service := range inv.services {
		if !contains(namespaces, service.Service.Namespace) {
			continue
		}
		ok, err := labelSelectorMatches(monitor.Spec.Selector, service.Service.Labels)
		if err != nil {
			return nil, fmt.Errorf("ServiceMonitor %s selector: %w", monitor.Name, err)
		}
		if ok {
			matched = append(matched, service.Service)
		}
	}
	if len(matched) != 1 {
		return nil, fmt.Errorf("ServiceMonitor %s selects %d Services, expected exactly one", monitor.Name, len(matched))
	}
	return matched[0], nil
}

// serviceMonitorTargetLabels returns the labels prometheus-operator adds to a
// ServiceMonitor endpoint's series: namespace, service, pod and container from
// service discovery, job from the Service name (or a jobLabel the Service
// carries), and endpoint from the Service port name or targetPort.
func serviceMonitorTargetLabels(monitor *prometheusoperatorv1.ServiceMonitor, service *corev1.Service, endpoint prometheusoperatorv1.Endpoint) map[string]string {
	labels := map[string]string{
		"namespace": perTargetValue,
		"pod":       perTargetValue,
		"container": perTargetValue,
		"service":   service.Name,
		"job":       service.Name,
	}
	if value, ok := service.Labels[monitor.Spec.JobLabel]; ok && monitor.Spec.JobLabel != "" {
		labels["job"] = value
	}
	switch {
	case endpoint.Port != "":
		labels["endpoint"] = endpoint.Port
	case endpoint.TargetPort != nil: //nolint:staticcheck // SA1019: legacy CoreOS field still emitted by existing assets
		labels["endpoint"] = endpoint.TargetPort.String() //nolint:staticcheck // SA1019: legacy CoreOS field still emitted by existing assets
	}
	addConstantRelabelings(labels, gmpRelabelingRules(endpoint.MetricRelabelConfigs))
	return labels
}

// podMonitorTargetLabels returns the labels prometheus-operator adds to a
// PodMonitor endpoint's series: namespace, pod and container from service
// discovery, job as <namespace>/<name>, and endpoint from the port name or
// targetPort.
func podMonitorTargetLabels(monitor *prometheusoperatorv1.PodMonitor, endpoint prometheusoperatorv1.PodMetricsEndpoint) map[string]string {
	labels := map[string]string{
		"namespace": perTargetValue,
		"pod":       perTargetValue,
		"container": perTargetValue,
		"job":       monitor.Namespace + "/" + monitor.Name,
	}
	switch {
	case endpoint.Port != nil && *endpoint.Port != "":
		labels["endpoint"] = *endpoint.Port
	case endpoint.TargetPort != nil: //nolint:staticcheck // SA1019: legacy CoreOS field still emitted by existing assets
		labels["endpoint"] = endpoint.TargetPort.String() //nolint:staticcheck // SA1019: legacy CoreOS field still emitted by existing assets
	}
	addConstantRelabelings(labels, gmpRelabelingRules(endpoint.MetricRelabelConfigs))
	return labels
}

// podMonitoringTargetLabels returns the labels GMP adds to each endpoint's
// series: namespace, job from the PodMonitoring name, the configured pod
// metadata labels, and any constant metric relabeling.
func podMonitoringTargetLabels(podMonitoring *gmpmonitoring.PodMonitoring) []map[string]string {
	metadata := gmpDefaultMetadataLabels
	if podMonitoring.Spec.TargetLabels.Metadata != nil {
		metadata = *podMonitoring.Spec.TargetLabels.Metadata
	}

	result := make([]map[string]string, 0, len(podMonitoring.Spec.Endpoints))
	for _, endpoint := range podMonitoring.Spec.Endpoints {
		labels := map[string]string{
			"namespace": perTargetValue,
			"job":       podMonitoring.Name,
		}
		for _, label := range metadata {
			labels[label] = perTargetValue
		}
		addConstantRelabelings(labels, endpoint.MetricRelabeling)
		result = append(result, labels)
	}
	return result
}

// addConstantRelabelings applies replace rules that write a fixed value, which
// is how both backends attach a constant label to every series of a target.
func addConstantRelabelings(labels map[string]string, rules []gmpmonitoring.RelabelingRule) {
	for _, rule := range rules {
		if isConstantRelabeling(rule) {
			labels[rule.TargetLabel] = rule.Replacement
		}
	}
}

func isConstantRelabeling(rule gmpmonitoring.RelabelingRule) bool {
	return (rule.Action == "" || rule.Action == "replace") &&
		len(rule.SourceLabels) == 0 &&
		(rule.Regex == "" || rule.Regex == "(.*)") &&
		rule.TargetLabel != "" &&
		rule.Replacement != ""
}
