package gmpparity

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"text/template"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	cpov2assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	etcdutil "github.com/openshift/hypershift/support/etcd"
	"github.com/openshift/hypershift/support/gmpmonitoring"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

const (
	// hcpNamespace is the placeholder used by the v2 assets. The component
	// framework always reconciles manifests into cpContext.HCP.Namespace, so the
	// inventory uses a single synthetic namespace for every control-plane asset.
	hcpNamespace = "hcp-namespace"

	// managementNamespace is the default HyperShift operator namespace used by
	// `hypershift install`.
	managementNamespace = "hypershift"

	// openshiftMonitoringNamespace holds the management-level alerting rules.
	openshiftMonitoringNamespace = "openshift-monitoring"
)

// componentAssets describes one reconciled component and the asset directory it
// renders. Most components map one-to-one to an asset directory; etcd shards
// share the etcd directory and are rendered with different template data.
type componentAssets struct {
	Name         string
	Dir          string
	TemplateData map[string]string
}

func etcdTemplateData(name string) map[string]string {
	return map[string]string{
		"Name":                 name,
		"ClientServiceName":    etcdutil.ClientServiceName(name),
		"DiscoveryServiceName": etcdutil.DiscoveryServiceName(name),
	}
}

// repoRoot walks up from this test file until it finds the repository go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to resolve the test file path")
	}

	dir := filepath.Dir(thisFile)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not locate the repository root above %s", filepath.Dir(thisFile))
		}
		dir = parent
	}
}

// componentAssetsForTest enumerates every component asset directory and adds the
// etcd shard component, which shares the etcd asset directory.
func componentAssetsForTest(t *testing.T) []componentAssets {
	t.Helper()

	assetsRoot := filepath.Join(repoRoot(t), "control-plane-operator", "controllers", "hostedcontrolplane", "v2", "assets")
	entries, err := os.ReadDir(assetsRoot)
	if err != nil {
		t.Fatalf("failed to read the v2 assets directory: %v", err)
	}

	var components []componentAssets
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == "etcd" {
			// The default etcd component and every managed shard reconcile the
			// same asset directory with different template data. Only the
			// default and one representative shard are enumerated; additional
			// shards are template-identical apart from their names.
			components = append(components,
				componentAssets{Name: "etcd", Dir: name, TemplateData: etcdTemplateData("etcd")},
				componentAssets{Name: "etcd-events", Dir: name, TemplateData: etcdTemplateData("etcd-events")},
			)
			continue
		}
		components = append(components, componentAssets{Name: name, Dir: name})
	}
	return components
}

// monitorAssets lists the manifest files in a component asset directory.
func monitorAssets(t *testing.T, component componentAssets) []string {
	t.Helper()

	dir := filepath.Join(repoRoot(t), "control-plane-operator", "controllers", "hostedcontrolplane", "v2", "assets", component.Dir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("failed to read asset directory %s: %v", component.Dir, err)
	}

	var files []string
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".yaml" {
			continue
		}
		files = append(files, entry.Name())
	}
	sort.Strings(files)
	return files
}

// loadComponentManifest loads an embedded component manifest using the
// component's template data, exactly as the component framework does.
func loadComponentManifest(t *testing.T, component componentAssets, file string) client.Object {
	t.Helper()

	obj, _, err := cpov2assets.LoadManifestTemplated(component.Dir, file, component.TemplateData)
	if err != nil {
		t.Fatalf("failed to load %s/%s: %v", component.Dir, file, err)
	}

	// The component framework sets the namespace on every namespaced manifest;
	// assets use the literal HCP_NAMESPACE placeholder in some specs as well.
	obj.SetNamespace(normalizeNamespace(obj.GetNamespace()))
	return obj
}

// loadGeneratedPodMonitoring reads the staged GMP manifest. These files are
// deliberately outside v2/assets: that directory is automatically reconciled
// for every running CPO component, while this PR only prepares the config.
func loadGeneratedPodMonitoring(t *testing.T, component componentAssets) *gmpmonitoring.PodMonitoring {
	t.Helper()
	path := filepath.Join(repoRoot(t), "control-plane-operator", "controllers", "hostedcontrolplane", "v2", "gmpassets", component.Dir, "podmonitoring.yaml")
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("failed to read generated GMP asset %s: %v", path, err)
	}
	if component.TemplateData != nil {
		tmpl, err := template.New(path).Option("missingkey=error").Parse(string(raw))
		if err != nil {
			t.Fatalf("failed to parse generated GMP template %s: %v", path, err)
		}
		var rendered bytes.Buffer
		if err := tmpl.Execute(&rendered, component.TemplateData); err != nil {
			t.Fatalf("failed to render generated GMP template %s: %v", path, err)
		}
		raw = rendered.Bytes()
	}
	monitor := &gmpmonitoring.PodMonitoring{}
	if err := yaml.UnmarshalStrict(raw, monitor); err != nil {
		t.Fatalf("failed to decode generated GMP asset %s: %v", path, err)
	}
	monitor.Namespace = normalizeNamespace(monitor.Namespace)
	return monitor
}

func normalizeNamespace(namespace string) string {
	switch namespace {
	case "", "HCP_NAMESPACE":
		return hcpNamespace
	default:
		return namespace
	}
}

func normalizeNamespaces(namespaces []string) []string {
	normalized := make([]string, 0, len(namespaces))
	for _, namespace := range namespaces {
		normalized = append(normalized, normalizeNamespace(namespace))
	}
	return normalized
}

// inventoryPod is a pod template resolved from a workload or a Go constructor.
type inventoryPod struct {
	Component string
	Namespace string
	Labels    map[string]string
	Ports     []corev1.ContainerPort
}

type inventoryService struct {
	Component string
	Service   *corev1.Service
}

type inventoryServiceMonitor struct {
	Component string
	Monitor   *prometheusoperatorv1.ServiceMonitor
}

type inventoryPodMonitor struct {
	Component string
	Monitor   *prometheusoperatorv1.PodMonitor
}

type inventoryPodMonitoring struct {
	Component string
	Monitor   *gmpmonitoring.PodMonitoring
}

// ruleScope is the GMP rule scope required for a CoreOS rule group. Namespaced
// Rules may not constrain expressions that aggregate across namespaces, so the
// scope is an audited decision per group rather than a mechanical mapping.
type ruleScope string

const (
	ruleScopeNamespace ruleScope = "namespace"
	ruleScopeCluster   ruleScope = "cluster"
	ruleScopeGlobal    ruleScope = "global"
)

// ruleRequirement is one CoreOS rule group that GMP must reproduce.
type ruleRequirement struct {
	Source    string
	Namespace string
	Group     string
	Scope     ruleScope
}

// gmpRuleGroup is one rule group emitted by a GMP Rules resource.
type gmpRuleGroup struct {
	Source    string
	Namespace string
	Group     string
	Scope     ruleScope
}

// clusterInventory is the offline discovery inventory: Services, workload pod
// templates, monitors and rule groups rendered or constructed for one
// management cluster configuration.
type clusterInventory struct {
	services         []inventoryService
	pods             []inventoryPod
	serviceMonitors  []inventoryServiceMonitor
	podMonitors      []inventoryPodMonitor
	podMonitorings   []inventoryPodMonitoring
	ruleRequirements []ruleRequirement
	gmpRuleGroups    []gmpRuleGroup
}

// addAssetComponent adds every Service, workload pod template, monitor and rule
// group found in a component asset directory.
func (inv *clusterInventory) addAssetComponent(t *testing.T, component componentAssets) {
	t.Helper()

	for _, file := range monitorAssets(t, component) {
		obj := loadComponentManifest(t, component, file)

		switch typed := obj.(type) {
		case *corev1.Service:
			inv.services = append(inv.services, inventoryService{Component: component.Name, Service: typed})
		case *appsv1.Deployment:
			inv.pods = append(inv.pods, podFromTemplate(component.Name, typed.Namespace, typed.Spec.Template))
		case *appsv1.StatefulSet:
			inv.pods = append(inv.pods, podFromTemplate(component.Name, typed.Namespace, typed.Spec.Template))
		case *prometheusoperatorv1.ServiceMonitor:
			typed.Spec.NamespaceSelector.MatchNames = normalizeNamespaces(typed.Spec.NamespaceSelector.MatchNames)
			inv.serviceMonitors = append(inv.serviceMonitors, inventoryServiceMonitor{Component: component.Name, Monitor: typed})
		case *prometheusoperatorv1.PodMonitor:
			typed.Spec.NamespaceSelector.MatchNames = normalizeNamespaces(typed.Spec.NamespaceSelector.MatchNames)
			inv.podMonitors = append(inv.podMonitors, inventoryPodMonitor{Component: component.Name, Monitor: typed})
		case *prometheusoperatorv1.PrometheusRule:
			inv.addPrometheusRule(component.Name, typed)
		case *gmpmonitoring.PodMonitoring:
			inv.podMonitorings = append(inv.podMonitorings, inventoryPodMonitoring{Component: component.Name, Monitor: typed})
		case *gmpmonitoring.Rules:
			for _, group := range typed.Spec.Groups {
				inv.gmpRuleGroups = append(inv.gmpRuleGroups, gmpRuleGroup{
					Source:    component.Name + "/Rules/" + typed.Name,
					Namespace: typed.Namespace,
					Group:     group.Name,
					Scope:     ruleScopeNamespace,
				})
			}
		}
	}
	if generated := loadGeneratedPodMonitoring(t, component); generated != nil {
		inv.podMonitorings = append(inv.podMonitorings, inventoryPodMonitoring{Component: component.Name, Monitor: generated})
	}
}

func (inv *clusterInventory) addPrometheusRule(component string, rule *prometheusoperatorv1.PrometheusRule) {
	inv.ruleRequirements = append(inv.ruleRequirements, prometheusRuleRequirements(component, rule)...)
}

func prometheusRuleRequirements(source string, rule *prometheusoperatorv1.PrometheusRule) []ruleRequirement {
	requirements := make([]ruleRequirement, 0, len(rule.Spec.Groups))
	for _, group := range rule.Spec.Groups {
		requirements = append(requirements, ruleRequirement{
			Source:    source + "/PrometheusRule/" + rule.Name,
			Namespace: rule.Namespace,
			Group:     group.Name,
			Scope:     ruleScopeNamespace,
		})
	}
	return requirements
}

func podFromTemplate(component, namespace string, template corev1.PodTemplateSpec) inventoryPod {
	var ports []corev1.ContainerPort
	for _, container := range template.Spec.Containers {
		ports = append(ports, container.Ports...)
	}
	return inventoryPod{
		Component: component,
		Namespace: namespace,
		Labels:    template.Labels,
		Ports:     ports,
	}
}

// scrapeTarget is a normalized, resolved scrape destination. Coverage is
// compared on everything except Component: GMP may consolidate endpoints from
// several CoreOS monitors into one PodMonitoring endpoint, so the monitor name is
// not part of target identity. Component is retained for reporting and waivers.
type scrapeTarget struct {
	Component string
	Namespace string
	Selector  map[string]string
	Port      int32
	Path      string
	Scheme    string
}

func (t scrapeTarget) coverageKey() string {
	return fmt.Sprintf("%s|%s|%d|%s|%s", t.Namespace, selectorKey(t.Selector), t.Port, t.Path, t.Scheme)
}

func (t scrapeTarget) String() string {
	return fmt.Sprintf("component=%s namespace=%s selector={%s} port=%d path=%s scheme=%s",
		t.Component, t.Namespace, selectorKey(t.Selector), t.Port, t.Path, t.Scheme)
}

// selectorKey renders a label selector as a stable, sorted string.
func selectorKey(selector map[string]string) string {
	keys := make([]string, 0, len(selector))
	for key := range selector {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+selector[key])
	}
	return strings.Join(parts, ",")
}

// effectivePodSelector returns the pod selector that a Service applies after
// removing labels the component framework injects into every control-plane pod.
// Keeping the inject label would make every Service selector artificially wider
// than the equivalent PodMonitoring selector without changing the selected pods.
func effectivePodSelector(selector map[string]string) map[string]string {
	effective := make(map[string]string, len(selector))
	for key, value := range selector {
		if key == hyperv1.ControlPlaneComponentLabel {
			continue
		}
		effective[key] = value
	}
	return effective
}

func labelsMatchSelector(selector map[string]string, labels map[string]string) bool {
	for key, value := range selector {
		if labels[key] != value {
			return false
		}
	}
	return true
}

func labelSelectorMatches(selector metav1.LabelSelector, labels map[string]string) (bool, error) {
	if !labelsMatchSelector(selector.MatchLabels, labels) {
		return false, nil
	}
	for _, expression := range selector.MatchExpressions {
		matched, err := matchExpression(expression, labels)
		if err != nil {
			return false, err
		}
		if !matched {
			return false, nil
		}
	}
	return true, nil
}

func matchExpression(expression metav1.LabelSelectorRequirement, labels map[string]string) (bool, error) {
	value, exists := labels[expression.Key]
	switch expression.Operator {
	case metav1.LabelSelectorOpIn:
		return exists && contains(expression.Values, value), nil
	case metav1.LabelSelectorOpNotIn:
		return !exists || !contains(expression.Values, value), nil
	case metav1.LabelSelectorOpExists:
		return exists, nil
	case metav1.LabelSelectorOpDoesNotExist:
		return !exists, nil
	default:
		return false, fmt.Errorf("unsupported label selector operator %q on key %q", expression.Operator, expression.Key)
	}
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

// resolveContainerPort maps a named or numeric port reference to the concrete
// container port it selects. It fails rather than silently skipping a pod whose
// container does not expose the referenced port.
func resolveContainerPort(port intstr.IntOrString, pod inventoryPod) (int32, error) {
	if port.Type == intstr.Int {
		for _, containerPort := range pod.Ports {
			if containerPort.ContainerPort == port.IntVal {
				return containerPort.ContainerPort, nil
			}
		}
		return 0, fmt.Errorf("pod template for component %s has no container port %d", pod.Component, port.IntVal)
	}

	for _, containerPort := range pod.Ports {
		if containerPort.Name == port.StrVal {
			return containerPort.ContainerPort, nil
		}
	}
	return 0, fmt.Errorf("pod template for component %s has no container port named %q", pod.Component, port.StrVal)
}

// serviceTargetPort resolves the port a Service comparable monitor endpoint
// selects on the backing pods. A named endpoint port selects the Service port by
// name and takes precedence over targetPort; otherwise the endpoint targetPort
// selects a container port directly.
func serviceTargetPort(endpoint prometheusoperatorv1.Endpoint, servicePorts []corev1.ServicePort) (intstr.IntOrString, error) {
	if endpoint.Port != "" {
		for _, servicePort := range servicePorts {
			if servicePort.Name != endpoint.Port {
				continue
			}
			if servicePort.TargetPort.Type == intstr.String && servicePort.TargetPort.StrVal == "" {
				// A targetPort with no value defaults to the Service port number.
				return intstr.FromInt(int(servicePort.Port)), nil
			}
			if servicePort.TargetPort.Type == intstr.Int && servicePort.TargetPort.IntVal == 0 {
				return intstr.FromInt(int(servicePort.Port)), nil
			}
			return servicePort.TargetPort, nil
		}
		return intstr.IntOrString{}, fmt.Errorf("Service has no port named %q", endpoint.Port)
	}

	if endpoint.TargetPort != nil {
		return *endpoint.TargetPort, nil
	}
	return intstr.IntOrString{}, fmt.Errorf("endpoint specifies neither port nor targetPort")
}

// resolveServiceMonitorTargets resolves a ServiceMonitor to the concrete pods,
// ports and paths it scrapes. Unresolved Service, pod or port discovery is an
// error: silently dropping a target would make the parity guard pass while the
// target is unscraped.
func (inv *clusterInventory) resolveServiceMonitorTargets(component string, monitor *prometheusoperatorv1.ServiceMonitor) ([]scrapeTarget, error) {
	if len(monitor.Spec.Endpoints) == 0 {
		return nil, fmt.Errorf("ServiceMonitor %s has no endpoints", monitor.Name)
	}

	namespaces := monitor.Spec.NamespaceSelector.MatchNames
	if len(namespaces) == 0 {
		namespaces = []string{monitor.Namespace}
	}

	var matchedServices []inventoryService
	for _, service := range inv.services {
		if !contains(namespaces, service.Service.Namespace) {
			continue
		}
		matched, err := labelSelectorMatches(monitor.Spec.Selector, service.Service.Labels)
		if err != nil {
			return nil, fmt.Errorf("ServiceMonitor %s selector: %w", monitor.Name, err)
		}
		if matched {
			matchedServices = append(matchedServices, service)
		}
	}
	if len(matchedServices) == 0 {
		return nil, fmt.Errorf("ServiceMonitor %s selects no Service in namespaces %v", monitor.Name, namespaces)
	}

	var targets []scrapeTarget
	for _, service := range matchedServices {
		podSelector := effectivePodSelector(service.Service.Spec.Selector)
		if len(podSelector) == 0 {
			return nil, fmt.Errorf("Service %s selected by ServiceMonitor %s has no pod selector", service.Service.Name, monitor.Name)
		}

		var pods []inventoryPod
		for _, pod := range inv.pods {
			if pod.Namespace != service.Service.Namespace {
				continue
			}
			if labelsMatchSelector(podSelector, pod.Labels) {
				pods = append(pods, pod)
			}
		}
		if len(pods) == 0 {
			return nil, fmt.Errorf("Service %s selected by ServiceMonitor %s fronts no workload pod template", service.Service.Name, monitor.Name)
		}

		for _, endpoint := range monitor.Spec.Endpoints {
			targetPort, err := serviceTargetPort(endpoint, service.Service.Spec.Ports)
			if err != nil {
				return nil, fmt.Errorf("ServiceMonitor %s endpoint: %w", monitor.Name, err)
			}

			for _, pod := range pods {
				port, err := resolveContainerPort(targetPort, pod)
				if err != nil {
					return nil, fmt.Errorf("ServiceMonitor %s: %w", monitor.Name, err)
				}
				targets = append(targets, scrapeTarget{
					Component: component,
					Namespace: service.Service.Namespace,
					Selector:  podSelector,
					Port:      port,
					Path:      defaultPath(endpoint.Path),
					Scheme:    coreOSScheme(endpoint.Scheme),
				})
			}
		}
	}

	return deduplicateTargets(targets), nil
}

// resolvePodMonitorTargets resolves a pod-targeting CoreOS monitor.
func (inv *clusterInventory) resolvePodMonitorTargets(component string, monitor *prometheusoperatorv1.PodMonitor) ([]scrapeTarget, error) {
	namespaces := monitor.Spec.NamespaceSelector.MatchNames
	if len(namespaces) == 0 {
		namespaces = []string{monitor.Namespace}
	}

	var pods []inventoryPod
	for _, pod := range inv.pods {
		if !contains(namespaces, pod.Namespace) {
			continue
		}
		matched, err := labelSelectorMatches(monitor.Spec.Selector, pod.Labels)
		if err != nil {
			return nil, fmt.Errorf("PodMonitor %s selector: %w", monitor.Name, err)
		}
		if matched {
			pods = append(pods, pod)
		}
	}
	if len(pods) == 0 {
		return nil, fmt.Errorf("PodMonitor %s selects no workload pod template in namespaces %v", monitor.Name, namespaces)
	}

	var targets []scrapeTarget
	for _, endpoint := range monitor.Spec.PodMetricsEndpoints {
		port, err := podEndpointPort(endpoint.Port, endpoint.TargetPort)
		if err != nil {
			return nil, fmt.Errorf("PodMonitor %s endpoint: %w", monitor.Name, err)
		}
		for _, pod := range pods {
			concretePort, err := resolveContainerPort(port, pod)
			if err != nil {
				return nil, fmt.Errorf("PodMonitor %s: %w", monitor.Name, err)
			}
			targets = append(targets, scrapeTarget{
				Component: component,
				Namespace: pod.Namespace,
				Selector:  effectivePodSelector(monitor.Spec.Selector.MatchLabels),
				Port:      concretePort,
				Path:      defaultPath(endpoint.Path),
				Scheme:    coreOSScheme(endpoint.Scheme),
			})
		}
	}

	return deduplicateTargets(targets), nil
}

func podEndpointPort(port *string, targetPort *intstr.IntOrString) (intstr.IntOrString, error) {
	if port != nil && *port != "" {
		return intstr.FromString(*port), nil
	}
	if targetPort != nil {
		return *targetPort, nil
	}
	return intstr.IntOrString{}, fmt.Errorf("endpoint specifies neither port nor targetPort")
}

// resolvePodMonitoringTargets resolves a GMP PodMonitoring to its scrape targets.
func (inv *clusterInventory) resolvePodMonitoringTargets(component string, monitor *gmpmonitoring.PodMonitoring) ([]scrapeTarget, error) {
	selector := effectivePodSelector(monitor.Spec.Selector.MatchLabels)

	var pods []inventoryPod
	for _, pod := range inv.pods {
		if pod.Namespace != monitor.Namespace {
			continue
		}
		matched, err := labelSelectorMatches(monitor.Spec.Selector, pod.Labels)
		if err != nil {
			return nil, fmt.Errorf("PodMonitoring %s selector: %w", monitor.Name, err)
		}
		if matched {
			pods = append(pods, pod)
		}
	}
	if len(pods) == 0 {
		return nil, fmt.Errorf("PodMonitoring %s selects no workload pod template in namespace %s", monitor.Name, monitor.Namespace)
	}

	var targets []scrapeTarget
	for _, endpoint := range monitor.Spec.Endpoints {
		for _, pod := range pods {
			port, err := resolveContainerPort(endpoint.Port, pod)
			if err != nil {
				return nil, fmt.Errorf("PodMonitoring %s: %w", monitor.Name, err)
			}
			targets = append(targets, scrapeTarget{
				Component: component,
				Namespace: pod.Namespace,
				Selector:  selector,
				Port:      port,
				Path:      defaultPath(endpoint.Path),
				Scheme:    defaultScheme(endpoint.Scheme),
			})
		}
	}

	return deduplicateTargets(targets), nil
}

func defaultPath(path string) string {
	if path == "" {
		return "/metrics"
	}
	return path
}

func defaultScheme(scheme string) string {
	if scheme == "" {
		return "http"
	}
	return scheme
}

func coreOSScheme(scheme *prometheusoperatorv1.Scheme) string {
	if scheme == nil {
		return defaultScheme("")
	}
	return defaultScheme(string(*scheme))
}

func deduplicateTargets(targets []scrapeTarget) []scrapeTarget {
	seen := make(map[string]struct{}, len(targets))
	deduplicated := make([]scrapeTarget, 0, len(targets))
	for _, target := range targets {
		key := target.coverageKey()
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		deduplicated = append(deduplicated, target)
	}
	sort.Slice(deduplicated, func(i, j int) bool {
		return deduplicated[i].coverageKey() < deduplicated[j].coverageKey()
	})
	return deduplicated
}

// coreOSTargets resolves every endpoint-bearing CoreOS monitor in the inventory.
func (inv *clusterInventory) coreOSTargets() ([]scrapeTarget, error) {
	var targets []scrapeTarget
	for _, monitor := range inv.serviceMonitors {
		resolved, err := inv.resolveServiceMonitorTargets(monitor.Component, monitor.Monitor)
		if err != nil {
			return nil, err
		}
		targets = append(targets, resolved...)
	}
	for _, monitor := range inv.podMonitors {
		resolved, err := inv.resolvePodMonitorTargets(monitor.Component, monitor.Monitor)
		if err != nil {
			return nil, err
		}
		targets = append(targets, resolved...)
	}
	return deduplicateTargets(targets), nil
}

// gmpTargets resolves every endpoint-bearing GMP PodMonitoring in the inventory.
func (inv *clusterInventory) gmpTargets() ([]scrapeTarget, error) {
	var targets []scrapeTarget
	for _, monitor := range inv.podMonitorings {
		resolved, err := inv.resolvePodMonitoringTargets(monitor.Component, monitor.Monitor)
		if err != nil {
			return nil, err
		}
		targets = append(targets, resolved...)
	}
	return deduplicateTargets(targets), nil
}

// uncoveredTargets returns the CoreOS scrape targets with no equivalent GMP
// endpoint, keyed by coverage identity.
func uncoveredTargets(coreOS, gmp []scrapeTarget) []scrapeTarget {
	covered := make(map[string]struct{}, len(gmp))
	for _, target := range gmp {
		covered[target.coverageKey()] = struct{}{}
	}

	var uncovered []scrapeTarget
	for _, target := range coreOS {
		if _, ok := covered[target.coverageKey()]; ok {
			continue
		}
		uncovered = append(uncovered, target)
	}
	return deduplicateTargets(uncovered)
}
