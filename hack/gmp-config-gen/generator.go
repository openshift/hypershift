package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	installassets "github.com/openshift/hypershift/cmd/install/assets"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/oapi"
	kubemanifests "github.com/openshift/hypershift/pkg/manifests/cpo"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/metrics"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"

	"sigs.k8s.io/yaml"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	yamlv3 "gopkg.in/yaml.v3"
)

// The GMP API types are imported through a thin alias so the conversion logic
// can be read next to the CoreOS types it translates.
type (
	prometheusServiceMonitor = prometheusoperatorv1.ServiceMonitor
	prometheusPodMonitor     = prometheusoperatorv1.PodMonitor
)

const (
	gmpAPIVersion = "monitoring.googleapis.com/v1"

	// placeholderNamespace is the namespace literal used by the component
	// assets. The component framework replaces metadata.namespace with the real
	// HCP namespace; it does not rewrite the literal anywhere in the spec, so a
	// spec field that carries it (tls.serverName) must be substituted by
	// whatever reconciles the staged asset.
	placeholderNamespace = "HCP_NAMESPACE"

	// managementNamespacePlaceholder stands in for the namespace the HyperShift
	// operator is installed into, which is configurable. Like HCP_NAMESPACE it
	// keeps a staged manifest independent of the cluster it was generated from,
	// so whatever applies the asset must substitute the live namespace.
	managementNamespacePlaceholder = "MANAGEMENT_NAMESPACE"

	// Unset CoreOS endpoint intervals inherit the GCP management Prometheus
	// scrapeInterval of 60s. GMP requires an explicit interval.
	defaultScrapeInterval = "60s"
	defaultMetricsPath    = "/metrics"

	// serviceTargetLabel and endpointTargetLabel are target labels that
	// prometheus-operator adds to every series from a CoreOS monitor and GMP
	// does not. The generator reproduces them with constant metric relabeling
	// rules, which the runtime adapters must keep when they replace the
	// MetricsSet-dependent rules.
	serviceTargetLabel  = "service"
	endpointTargetLabel = "endpoint"
	// containerTargetLabel is a pod metadata label, but GMP only reports it for
	// an endpoint addressed by port name. An endpoint the generator has to
	// address by number gets a constant rule for it instead, which the runtime
	// adapters must keep in the same way.
	containerTargetLabel = "container"
)

// coreOSMetadataLabels are the pod metadata target labels prometheus-operator
// adds for both ServiceMonitor and PodMonitor targets. GMP defaults to also
// adding top_level_controller_name and top_level_controller_type, which CoreOS
// series do not carry. GMP adds the namespace label unconditionally.
var coreOSMetadataLabels = []string{"container", "pod"}

// caConfigMapSecrets maps each CA ConfigMap referenced by the CoreOS monitors to
// the CA-only Secret that mirrors it. GMP only accepts Secret references, so a
// PodMonitoring cannot point at the ConfigMap.
//
// The signer Secrets (manifests.RootCASecret, manifests.EtcdSignerSecret) hold
// the CA private key next to ca.crt, and RBAC grants a whole Secret rather than
// one key. Referencing them would require granting the GMP collector read access
// to the signing keys. The mirrors carry only the ConfigMap's ca.crt bytes and
// are created by the GMP runtime reconciliation, not by the CoreOS path.
var caConfigMapSecrets = map[string]string{
	// The root-ca ConfigMap aggregates the self-signed root CA with the default
	// ingress CA; the mirror copies that bundle byte for byte.
	"root-ca": "root-ca-cert",
	// The etcd-ca ConfigMap carries the etcd signer CA.
	"etcd-ca": "etcd-ca-cert",
}

// forbiddenCASecretNames are Secrets that hold a CA private key. A monitor may
// only reference a CA through the CA-only mirror, so generation fails if a
// source asset points TLS at one of these directly. A test pins this list.
var forbiddenCASecretNames = map[string]struct{}{
	"root-ca":             {},
	"etcd-signer":         {},
	"etcd-metrics-signer": {},
}

// externalServices returns Services that are reconciled in Go rather than from
// an asset. The generator must resolve their pod selectors just like an asset
// Service, otherwise a ServiceMonitor that selects them cannot be translated.
func externalServices(componentDir string) ([]*corev1.Service, error) {
	switch filepath.Base(componentDir) {
	case "kube-apiserver":
		service := kubemanifests.KubeAPIServerService(placeholderNamespace)
		if err := kas.ReconcileServiceClusterIP(service, nil); err != nil {
			return nil, fmt.Errorf("failed to construct the kube-apiserver Service: %w", err)
		}
		return []*corev1.Service{service}, nil
	case "openshift-apiserver":
		service := manifests.OpenshiftAPIServerService(placeholderNamespace)
		if err := oapi.ReconcileOpenShiftAPIService(service, config.OwnerRef{}); err != nil {
			return nil, fmt.Errorf("failed to construct the openshift-apiserver Service: %w", err)
		}
		return []*corev1.Service{service}, nil
	default:
		return nil, nil
	}
}

// componentAssets is the set of manifests that describe one component's pods,
// Services and monitors.
type componentAssets struct {
	dir            string
	services       []*corev1.Service
	containerPorts []corev1.ContainerPort
	// portContainers maps each named container port to the container that
	// declares it. A name declared by more than one container maps to "".
	portContainers map[string]string
	// podLabels holds the labels of every workload pod template in the
	// component. A monitor's jobLabel reads one of them (Service labels for a
	// ServiceMonitor, pod labels for a PodMonitor), so they are needed to tell
	// whether the field actually selects anything.
	podLabels []map[string]string
}

// loadComponent reads the non-monitor manifests in a component directory.
// Decoding is intentionally lenient: the generator only needs pod selectors and
// container ports, and unrelated assets may carry fields newer than the
// vendored API types.
func loadComponent(componentDir string, placeholders *templatePlaceholders) (*componentAssets, error) {
	component := &componentAssets{dir: componentDir}

	entries, err := os.ReadDir(componentDir)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".yaml" {
			continue
		}
		switch entry.Name() {
		case sourceServiceMonitor, sourcePodMonitor, generatedPodMonitoring:
			continue
		}

		raw, err := os.ReadFile(filepath.Join(componentDir, entry.Name()))
		if err != nil {
			return nil, err
		}
		prepared := placeholders.protect(raw)

		var probe struct {
			Kind string `json:"kind"`
		}
		if err := yaml.Unmarshal(prepared, &probe); err != nil {
			return nil, fmt.Errorf("failed to parse %s: %w", entry.Name(), err)
		}

		switch probe.Kind {
		case "Service":
			service := &corev1.Service{}
			if err := yaml.Unmarshal(prepared, service); err != nil {
				return nil, fmt.Errorf("failed to parse Service in %s: %w", entry.Name(), err)
			}
			component.services = append(component.services, service)
		case "Deployment":
			deployment := &appsv1.Deployment{}
			if err := yaml.Unmarshal(prepared, deployment); err != nil {
				return nil, fmt.Errorf("failed to parse Deployment in %s: %w", entry.Name(), err)
			}
			component.addContainerPorts(deployment.Spec.Template.Spec)
			component.podLabels = append(component.podLabels, deployment.Spec.Template.Labels)
		case "StatefulSet":
			statefulSet := &appsv1.StatefulSet{}
			if err := yaml.Unmarshal(prepared, statefulSet); err != nil {
				return nil, fmt.Errorf("failed to parse StatefulSet in %s: %w", entry.Name(), err)
			}
			component.addContainerPorts(statefulSet.Spec.Template.Spec)
			component.podLabels = append(component.podLabels, statefulSet.Spec.Template.Labels)
		}
	}

	external, err := externalServices(componentDir)
	if err != nil {
		return nil, err
	}
	component.services = append(component.services, external...)
	return component, nil
}

// addContainerPorts records a workload pod template's container ports and the
// container that declares each named port.
func (c *componentAssets) addContainerPorts(spec corev1.PodSpec) {
	if c.portContainers == nil {
		c.portContainers = map[string]string{}
	}
	for _, container := range spec.Containers {
		c.containerPorts = append(c.containerPorts, container.Ports...)
		for _, port := range container.Ports {
			if port.Name == "" {
				continue
			}
			if existing, ok := c.portContainers[port.Name]; ok && existing != container.Name {
				c.portContainers[port.Name] = ""
				continue
			}
			c.portContainers[port.Name] = container.Name
		}
	}
}

// managementMonitor is a CoreOS monitor that `hypershift install` constructs in
// Go instead of reading from a v2 asset, with the Services and workload
// metadata needed to translate it.
type managementMonitor struct {
	// component names the staged output directory.
	component string
	// source is the `# Source:` line recorded in the generated file.
	source         string
	assets         *componentAssets
	serviceMonitor *prometheusServiceMonitor
	podMonitor     *prometheusPodMonitor
}

// managementMonitors returns the management-level monitors, constructed exactly
// as `hypershift install` builds them. The install namespace is configurable, so
// the staged manifests carry managementNamespacePlaceholder in place of a
// concrete namespace.
func managementMonitors() []*managementMonitor {
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: managementNamespacePlaceholder}}

	operatorService := installassets.HyperShiftOperatorService{Namespace: namespace}.Build()
	operatorDeployment := installassets.HyperShiftOperatorDeployment{
		Namespace:      namespace,
		ServiceAccount: installassets.HyperShiftOperatorServiceAccount{Namespace: namespace}.Build(),
	}.Build()

	// external-dns publishes metrics from its pods only; `hypershift install`
	// creates no Service for it.
	externalDNSDeployment := installassets.ExternalDNSDeployment{
		Namespace:      namespace,
		ServiceAccount: installassets.ExternalDNSServiceAccount{Namespace: namespace}.Build(),
	}.Build()

	operatorAssets := &componentAssets{
		services:  []*corev1.Service{operatorService},
		podLabels: []map[string]string{operatorDeployment.Spec.Template.Labels},
	}
	operatorAssets.addContainerPorts(operatorDeployment.Spec.Template.Spec)
	externalDNSAssets := &componentAssets{
		podLabels: []map[string]string{externalDNSDeployment.Spec.Template.Labels},
	}
	externalDNSAssets.addContainerPorts(externalDNSDeployment.Spec.Template.Spec)

	return []*managementMonitor{
		{
			component:      "operator",
			source:         "cmd/install/assets/hypershift_operator.go HyperShiftServiceMonitor",
			assets:         operatorAssets,
			serviceMonitor: installassets.HyperShiftServiceMonitor{Namespace: namespace}.Build(),
		},
		{
			component:  "external-dns",
			source:     "cmd/install/assets/hypershift_operator.go ExternalDNSPodMonitor",
			assets:     externalDNSAssets,
			podMonitor: installassets.ExternalDNSPodMonitor{Namespace: namespace}.Build(),
		},
	}
}

// generateManagement converts a Go-constructed management monitor into the
// committed GMP PodMonitoring document.
func generateManagement(monitor *managementMonitor) ([]byte, error) {
	var doc *podMonitoringDoc
	var err error
	switch {
	case monitor.serviceMonitor != nil:
		doc, err = monitor.assets.convertServiceMonitor(monitor.serviceMonitor)
	case monitor.podMonitor != nil:
		doc, err = monitor.assets.convertPodMonitor(monitor.podMonitor)
	default:
		return nil, fmt.Errorf("management monitor %q has no CoreOS source", monitor.component)
	}
	if err != nil {
		return nil, err
	}
	return marshalDoc(doc, monitor.source)
}

// convertServiceMonitor translates a ServiceMonitor into a PodMonitoring.
//
// A ServiceMonitor selects Services and scrapes their backing pods, while a
// PodMonitoring selects pods directly. The Service therefore has to be resolved
// first: its pod selector becomes the PodMonitoring selector and its named
// ports resolve the container port the endpoint actually scrapes.
func (c *componentAssets) convertServiceMonitor(monitor *prometheusServiceMonitor) (*podMonitoringDoc, error) {
	if err := validateNamespaceSelector(monitor.Spec.NamespaceSelector); err != nil {
		return nil, fmt.Errorf("ServiceMonitor %s: %w", monitor.Name, err)
	}
	if len(monitor.Spec.TargetLabels) > 0 || len(monitor.Spec.PodTargetLabels) > 0 {
		return nil, fmt.Errorf("ServiceMonitor %s: targetLabels and podTargetLabels are not translated yet", monitor.Name)
	}
	if monitor.Spec.SelectorMechanism != nil {
		return nil, fmt.Errorf("ServiceMonitor %s: selectorMechanism is not representable in a GMP PodMonitoring", monitor.Name)
	}
	if len(monitor.Spec.Endpoints) == 0 {
		return nil, fmt.Errorf("ServiceMonitor %s has no endpoints", monitor.Name)
	}

	service, err := c.serviceForMonitor(monitor)
	if err != nil {
		return nil, err
	}
	podSelector, err := podSelectorForService(service)
	if err != nil {
		return nil, fmt.Errorf("ServiceMonitor %s: %w", monitor.Name, err)
	}
	if err := validateServiceMonitorJobLabel(monitor, service); err != nil {
		return nil, err
	}

	endpoints := make([]endpointDoc, 0, len(monitor.Spec.Endpoints))
	for i, endpoint := range monitor.Spec.Endpoints {
		port, err := c.resolveServiceEndpointPort(endpoint, service)
		if err != nil {
			return nil, fmt.Errorf("ServiceMonitor %s endpoint %d: %w", monitor.Name, i, err)
		}
		converted, err := convertEndpoint(endpointHTTPConfig(endpoint), endpoint.Path, endpoint.Scheme, endpoint.Interval, endpoint.ScrapeTimeout, endpoint.Params, port)
		if err != nil {
			return nil, fmt.Errorf("ServiceMonitor %s endpoint %d: %w", monitor.Name, i, err)
		}
		converted.MetricRelabeling, err = convertMetricRelabeling(endpoint.MetricRelabelConfigs)
		if err != nil {
			return nil, fmt.Errorf("ServiceMonitor %s endpoint %d: %w", monitor.Name, i, err)
		}
		converted.MetricRelabeling = append(targetLabelRules(service.Name, serviceEndpointLabel(endpoint)), converted.MetricRelabeling...)
		if len(endpoint.RelabelConfigs) > 0 {
			return nil, fmt.Errorf("ServiceMonitor %s endpoint %d: target relabelings are not representable in a GMP PodMonitoring", monitor.Name, i)
		}
		if endpoint.HonorLabels || endpoint.HonorTimestamps != nil || endpoint.FilterRunning != nil {
			return nil, fmt.Errorf("ServiceMonitor %s endpoint %d: honorLabels, honorTimestamps and filterRunning are not translated yet", monitor.Name, i)
		}
		if endpoint.TLSConfig != nil {
			if endpoint.TLSConfig.CAFile != "" || endpoint.TLSConfig.CertFile != "" || endpoint.TLSConfig.KeyFile != "" {
				return nil, fmt.Errorf("ServiceMonitor %s endpoint %d: file-based TLS references are not representable in a GMP PodMonitoring", monitor.Name, i)
			}
			converted.TLS, err = convertTLS(&endpoint.TLSConfig.SafeTLSConfig)
			if err != nil {
				return nil, fmt.Errorf("ServiceMonitor %s endpoint %d: %w", monitor.Name, i, err)
			}
		}
		if err := rejectUntranslated(fmt.Sprintf("spec.endpoints[%d]", i), untranslatedServiceEndpoint(endpoint)); err != nil {
			return nil, fmt.Errorf("ServiceMonitor %s: %w", monitor.Name, err)
		}
		endpoints = append(endpoints, converted)
	}
	if err := c.ensureUniqueEndpointPorts(endpoints); err != nil {
		return nil, fmt.Errorf("ServiceMonitor %s: %w", monitor.Name, err)
	}
	if err := rejectUntranslated("spec", untranslatedServiceMonitorSpec(monitor.Spec)); err != nil {
		return nil, fmt.Errorf("ServiceMonitor %s: %w", monitor.Name, err)
	}

	return &podMonitoringDoc{
		APIVersion: gmpAPIVersion,
		Kind:       "PodMonitoring",
		Metadata: metadataDoc{
			// prometheus-operator sets a ServiceMonitor's job label to the
			// Service name, and GMP sets it to the PodMonitoring name, so the
			// PodMonitoring is named after the Service rather than the monitor.
			Name:        service.Name,
			Namespace:   monitor.Namespace,
			Labels:      copyStringMap(monitor.Labels),
			Annotations: copyStringMap(monitor.Annotations),
		},
		Spec: specDoc{
			// The monitor's own selector matches Service labels and was consumed
			// by serviceForMonitor; only the Service's pod selector applies to pods.
			Selector:     labelSelectorDoc{MatchLabels: podSelector},
			TargetLabels: coreOSTargetLabels(),
			Endpoints:    endpoints,
		},
	}, nil
}

// convertPodMonitor translates a PodMonitor into a PodMonitoring.
func (c *componentAssets) convertPodMonitor(monitor *prometheusPodMonitor) (*podMonitoringDoc, error) {
	if err := validateNamespaceSelector(monitor.Spec.NamespaceSelector); err != nil {
		return nil, fmt.Errorf("PodMonitor %s: %w", monitor.Name, err)
	}
	if err := c.validatePodMonitorJobLabel(monitor); err != nil {
		return nil, err
	}
	if len(monitor.Spec.PodTargetLabels) > 0 {
		return nil, fmt.Errorf("PodMonitor %s: podTargetLabels are not translated yet", monitor.Name)
	}
	if monitor.Spec.SelectorMechanism != nil {
		return nil, fmt.Errorf("PodMonitor %s: selectorMechanism is not representable in a GMP PodMonitoring", monitor.Name)
	}
	if len(monitor.Spec.PodMetricsEndpoints) == 0 {
		return nil, fmt.Errorf("PodMonitor %s has no podMetricsEndpoints", monitor.Name)
	}
	if len(monitor.Spec.Selector.MatchLabels) == 0 && len(monitor.Spec.Selector.MatchExpressions) == 0 {
		return nil, fmt.Errorf("PodMonitor %s has an empty pod selector", monitor.Name)
	}

	endpoints := make([]endpointDoc, 0, len(monitor.Spec.PodMetricsEndpoints))
	for i, endpoint := range monitor.Spec.PodMetricsEndpoints {
		port, err := c.resolvePodEndpointPort(endpoint)
		if err != nil {
			return nil, fmt.Errorf("PodMonitor %s endpoint %d: %w", monitor.Name, i, err)
		}
		converted, err := convertEndpoint(endpointHTTPConfigFromPodEndpoint(endpoint), endpoint.Path, endpoint.Scheme, endpoint.Interval, endpoint.ScrapeTimeout, endpoint.Params, port)
		if err != nil {
			return nil, fmt.Errorf("PodMonitor %s endpoint %d: %w", monitor.Name, i, err)
		}
		converted.MetricRelabeling, err = convertMetricRelabeling(endpoint.MetricRelabelConfigs)
		if err != nil {
			return nil, fmt.Errorf("PodMonitor %s endpoint %d: %w", monitor.Name, i, err)
		}
		converted.MetricRelabeling = append(targetLabelRules("", podEndpointLabel(endpoint)), converted.MetricRelabeling...)
		if len(endpoint.RelabelConfigs) > 0 {
			return nil, fmt.Errorf("PodMonitor %s endpoint %d: target relabelings are not representable in a GMP PodMonitoring", monitor.Name, i)
		}
		if endpoint.HonorLabels || endpoint.HonorTimestamps != nil || endpoint.FilterRunning != nil {
			return nil, fmt.Errorf("PodMonitor %s endpoint %d: honorLabels, honorTimestamps and filterRunning are not translated yet", monitor.Name, i)
		}
		converted.TLS, err = convertTLS(endpoint.TLSConfig)
		if err != nil {
			return nil, fmt.Errorf("PodMonitor %s endpoint %d: %w", monitor.Name, i, err)
		}
		if err := rejectUntranslated(fmt.Sprintf("spec.podMetricsEndpoints[%d]", i), untranslatedPodEndpoint(endpoint)); err != nil {
			return nil, fmt.Errorf("PodMonitor %s: %w", monitor.Name, err)
		}
		endpoints = append(endpoints, converted)
	}
	if err := c.ensureUniqueEndpointPorts(endpoints); err != nil {
		return nil, fmt.Errorf("PodMonitor %s: %w", monitor.Name, err)
	}
	if err := rejectUntranslated("spec", untranslatedPodMonitorSpec(monitor.Spec)); err != nil {
		return nil, fmt.Errorf("PodMonitor %s: %w", monitor.Name, err)
	}

	return &podMonitoringDoc{
		APIVersion: gmpAPIVersion,
		Kind:       "PodMonitoring",
		Metadata: metadataDoc{
			// prometheus-operator sets a PodMonitor's job label to
			// <namespace>/<name>, which a PodMonitoring name cannot hold. The
			// monitor name is the closest value GMP can produce.
			Name:        monitor.Name,
			Namespace:   monitor.Namespace,
			Labels:      copyStringMap(monitor.Labels),
			Annotations: copyStringMap(monitor.Annotations),
		},
		Spec: specDoc{
			Selector:     labelSelectorDoc{MatchLabels: copyStringMap(monitor.Spec.Selector.MatchLabels), MatchExpressions: labelSelectorRequirements(monitor.Spec.Selector.MatchExpressions)},
			TargetLabels: coreOSTargetLabels(),
			Endpoints:    endpoints,
		},
	}, nil
}

// coreOSTargetLabels limits the GMP pod metadata labels to the ones
// prometheus-operator adds, so series from both backends carry the same labels.
func coreOSTargetLabels() *targetLabelsDoc {
	return &targetLabelsDoc{Metadata: slices.Clone(coreOSMetadataLabels)}
}

// serviceEndpointLabel returns the endpoint label prometheus-operator gives a
// ServiceMonitor endpoint: the Service port name, falling back to targetPort.
func serviceEndpointLabel(endpoint prometheusoperatorv1.Endpoint) string {
	if endpoint.Port != "" {
		return endpoint.Port
	}
	if endpoint.TargetPort != nil { //nolint:staticcheck // SA1019: legacy CoreOS field still emitted by existing assets
		return endpoint.TargetPort.String() //nolint:staticcheck // SA1019: legacy CoreOS field still emitted by existing assets
	}
	return ""
}

// podEndpointLabel returns the endpoint label prometheus-operator gives a
// PodMonitor endpoint: the pod port name, falling back to targetPort. A
// portNumber endpoint gets no endpoint label.
func podEndpointLabel(endpoint prometheusoperatorv1.PodMetricsEndpoint) string {
	if endpoint.Port != nil && *endpoint.Port != "" {
		return *endpoint.Port
	}
	if endpoint.TargetPort != nil { //nolint:staticcheck // SA1019: legacy CoreOS field still emitted by existing assets
		return endpoint.TargetPort.String() //nolint:staticcheck // SA1019: legacy CoreOS field still emitted by existing assets
	}
	return ""
}

// targetLabelRules returns constant metric relabeling rules that add the
// service and endpoint target labels prometheus-operator would have added.
// Empty values are skipped, matching prometheus-operator, which omits a label
// it has no value for. The rules overwrite a same-named label exposed by the
// target, where prometheus-operator would have renamed it to exported_<name>.
func targetLabelRules(service, endpoint string) []relabelingRuleDoc {
	var rules []relabelingRuleDoc
	if service != "" {
		rules = append(rules, relabelingRuleDoc{TargetLabel: serviceTargetLabel, Replacement: service, Action: "replace"})
	}
	if endpoint != "" {
		rules = append(rules, relabelingRuleDoc{TargetLabel: endpointTargetLabel, Replacement: endpoint, Action: "replace"})
	}
	return rules
}

// labelSelectorRequirements converts selector requirements into the document
// form, preserving order.
func labelSelectorRequirements(requirements []metav1.LabelSelectorRequirement) []labelSelectorRequirementDoc {
	if len(requirements) == 0 {
		return nil
	}
	converted := make([]labelSelectorRequirementDoc, 0, len(requirements))
	for _, requirement := range requirements {
		converted = append(converted, labelSelectorRequirementDoc{
			Key:      requirement.Key,
			Operator: string(requirement.Operator),
			Values:   slices.Clone(requirement.Values),
		})
	}
	return converted
}

// The untranslated* functions return a copy of a CoreOS value with every field
// the generator translates (or deliberately leaves to the runtime adapters)
// cleared. Whatever remains set would be silently dropped from the GMP
// PodMonitoring, so rejectUntranslated fails generation on it. Clearing the
// handled fields, rather than listing the unsupported ones, also catches fields
// added by a future prometheus-operator bump.

func untranslatedServiceMonitorSpec(spec prometheusoperatorv1.ServiceMonitorSpec) *prometheusoperatorv1.ServiceMonitorSpec {
	remainder := spec.DeepCopy()
	remainder.Selector = metav1.LabelSelector{}
	remainder.NamespaceSelector = prometheusoperatorv1.NamespaceSelector{}
	remainder.Endpoints = nil
	// The jobLabel is validated next to the Service it reads labels from; it is
	// consumed there rather than rejected as untranslated.
	remainder.JobLabel = ""
	return remainder
}

func untranslatedPodMonitorSpec(spec prometheusoperatorv1.PodMonitorSpec) *prometheusoperatorv1.PodMonitorSpec {
	remainder := spec.DeepCopy()
	remainder.Selector = metav1.LabelSelector{}
	remainder.NamespaceSelector = prometheusoperatorv1.NamespaceSelector{}
	remainder.PodMetricsEndpoints = nil
	// The jobLabel is validated against the pod templates; it is consumed there
	// rather than rejected as untranslated.
	remainder.JobLabel = ""
	return remainder
}

func untranslatedServiceEndpoint(endpoint prometheusoperatorv1.Endpoint) *prometheusoperatorv1.Endpoint {
	remainder := endpoint.DeepCopy()
	remainder.Port = ""
	remainder.TargetPort = nil //nolint:staticcheck // SA1019: cleared because the field is translated separately
	remainder.Path = ""
	remainder.Scheme = nil
	remainder.Params = nil
	remainder.Interval = ""
	remainder.ScrapeTimeout = ""
	remainder.TLSConfig = nil
	remainder.ProxyURL = nil
	// Static rules are translated here; the later GMP runtime adapters must
	// inject MetricsSet-dependent rules at reconcile time.
	remainder.MetricRelabelConfigs = nil
	return remainder
}

func untranslatedPodEndpoint(endpoint prometheusoperatorv1.PodMetricsEndpoint) *prometheusoperatorv1.PodMetricsEndpoint {
	remainder := endpoint.DeepCopy()
	remainder.Port = nil
	remainder.PortNumber = nil
	remainder.TargetPort = nil //nolint:staticcheck // SA1019: cleared because the field is translated separately
	remainder.Path = ""
	remainder.Scheme = nil
	remainder.Params = nil
	remainder.Interval = ""
	remainder.ScrapeTimeout = ""
	remainder.TLSConfig = nil
	remainder.ProxyURL = nil
	// Static rules are translated here; the later GMP runtime adapters must
	// inject MetricsSet-dependent rules at reconcile time.
	remainder.MetricRelabelConfigs = nil
	return remainder
}

// rejectUntranslated fails when remainder still has a field set, naming each
// field by its JSON path under prefix.
func rejectUntranslated(prefix string, remainder any) error {
	raw, err := json.Marshal(remainder)
	if err != nil {
		return fmt.Errorf("failed to inspect %s: %w", prefix, err)
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return fmt.Errorf("failed to inspect %s: %w", prefix, err)
	}

	var fields []string
	collectSetFields(prefix, decoded, &fields)
	if len(fields) == 0 {
		return nil
	}
	sort.Strings(fields)
	return fmt.Errorf("%s not translated to GMP; translate it in hack/gmp-config-gen or remove it from the monitor", strings.Join(fields, ", "))
}

// collectSetFields appends the path of every field that carries a value. Empty
// strings, objects and lists come from fields serialized without omitempty (for
// example an empty bearerTokenSecret placeholder) and are not settings. An
// explicit false or zero is kept, because a pointer field set to it changes the
// scrape behavior.
func collectSetFields(path string, value any, fields *[]string) {
	switch typed := value.(type) {
	case nil:
	case string:
		if typed != "" {
			*fields = append(*fields, path)
		}
	case map[string]any:
		for key, child := range typed {
			collectSetFields(path+"."+key, child, fields)
		}
	case []any:
		if len(typed) > 0 {
			*fields = append(*fields, path)
		}
	default:
		*fields = append(*fields, path)
	}
}

// endpointHTTPConfig normalizes the authentication fields shared by both
// endpoint types.
type endpointHTTPConfigFields struct {
	bearerTokenSecret *corev1.SecretKeySelector
	authorization     *prometheusoperatorv1.SafeAuthorization
	basicAuth         *prometheusoperatorv1.BasicAuth
	oauth2            *prometheusoperatorv1.OAuth2
	proxyURL          string
}

func endpointHTTPConfig(endpoint prometheusoperatorv1.Endpoint) endpointHTTPConfigFields {
	return endpointHTTPConfigFields{
		bearerTokenSecret: endpoint.BearerTokenSecret, //nolint:staticcheck // SA1019: legacy CoreOS field rejected by the conversion
		authorization:     endpoint.Authorization,
		basicAuth:         endpoint.BasicAuth,
		oauth2:            endpoint.OAuth2,
		proxyURL:          stringValue(endpoint.ProxyURL),
	}
}

func endpointHTTPConfigFromPodEndpoint(endpoint prometheusoperatorv1.PodMetricsEndpoint) endpointHTTPConfigFields {
	return endpointHTTPConfigFields{
		bearerTokenSecret: endpoint.BearerTokenSecret, //nolint:staticcheck // SA1019: legacy CoreOS field rejected by the conversion
		authorization:     endpoint.Authorization,
		basicAuth:         endpoint.BasicAuth,
		oauth2:            endpoint.OAuth2,
		proxyURL:          stringValue(endpoint.ProxyURL),
	}
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// convertEndpoint translates the fields shared by ServiceMonitor and PodMonitor
// endpoints. Static metric relabeling is converted by convertMetricRelabeling;
// MetricsSet-dependent additions remain a gated runtime responsibility.
func convertEndpoint(httpConfig endpointHTTPConfigFields, path string, scheme *prometheusoperatorv1.Scheme, interval prometheusoperatorv1.Duration, timeout prometheusoperatorv1.Duration, params map[string][]string, port intstr.IntOrString) (endpointDoc, error) {
	if httpConfig.bearerTokenSecret != nil && httpConfig.bearerTokenSecret.Name != "" {
		return endpointDoc{}, fmt.Errorf("bearerTokenSecret is not translated yet")
	}
	if httpConfig.authorization != nil || httpConfig.basicAuth != nil || httpConfig.oauth2 != nil {
		return endpointDoc{}, fmt.Errorf("authorization, basicAuth and oauth2 are not translated yet")
	}

	settings := endpointDoc{
		Port:     portRef{Value: port},
		Scheme:   schemeOrDefault(scheme),
		Path:     pathOrDefault(path),
		Interval: intervalOrDefault(interval),
		Timeout:  string(timeout),
		Params:   params,
		ProxyURL: httpConfig.proxyURL,
	}
	return settings, nil
}

// convertMetricRelabeling preserves static metric filters and labels from a
// CoreOS endpoint. The runtime adapter must add/replace MetricsSet-dependent
// relabeling from support/metrics/sets.go before reconciling the GMP object.
// Rules GMP's protected-labels policy would reject are sanitized first, so a
// rule with no GMP-safe equivalent fails generation instead of admission.
func convertMetricRelabeling(source []prometheusoperatorv1.RelabelConfig) ([]relabelingRuleDoc, error) {
	source, err := metrics.SanitizeGMPRelabelConfigs(source)
	if err != nil {
		return nil, err
	}
	if len(source) == 0 {
		return nil, nil
	}
	result := make([]relabelingRuleDoc, 0, len(source))
	for i, rule := range source {
		action := strings.ToLower(rule.Action)
		switch action {
		case "", "replace", "keep", "drop", "hashmod", "labeldrop", "labelkeep":
		default:
			return nil, fmt.Errorf("metric relabeling %d: action %q is not supported by GMP", i, rule.Action)
		}
		if rule.Separator != nil && *rule.Separator == "" {
			return nil, fmt.Errorf("metric relabeling %d: empty separator cannot be preserved by GMP", i)
		}
		if rule.Replacement != nil && *rule.Replacement == "" {
			return nil, fmt.Errorf("metric relabeling %d: empty replacement cannot be preserved by GMP", i)
		}
		if action == "labeldrop" && rule.Regex == "" {
			return nil, fmt.Errorf("metric relabeling %d: labeldrop must set a regex, which the GMP API requires", i)
		}
		var labels []string
		if len(rule.SourceLabels) > 0 {
			labels = make([]string, len(rule.SourceLabels))
		}
		for j, label := range rule.SourceLabels {
			labels[j] = string(label)
		}
		result = append(result, relabelingRuleDoc{
			SourceLabels: labels,
			Separator:    stringValue(rule.Separator),
			TargetLabel:  rule.TargetLabel,
			Regex:        rule.Regex,
			Modulus:      rule.Modulus,
			Replacement:  stringValue(rule.Replacement),
			Action:       action,
		})
	}
	return result, nil
}

func pathOrDefault(path string) string {
	if path == "" {
		return defaultMetricsPath
	}
	return path
}

func intervalOrDefault(interval prometheusoperatorv1.Duration) string {
	if interval == "" {
		return defaultScrapeInterval
	}
	return string(interval)
}

func schemeOrDefault(scheme *prometheusoperatorv1.Scheme) string {
	if scheme == nil {
		return "http"
	}
	return strings.ToLower(string(*scheme))
}

// serviceForMonitor resolves the unique Service a ServiceMonitor selects.
func (c *componentAssets) serviceForMonitor(monitor *prometheusServiceMonitor) (*corev1.Service, error) {
	selector, err := metav1.LabelSelectorAsSelector(&monitor.Spec.Selector)
	if err != nil {
		return nil, fmt.Errorf("ServiceMonitor %s has an invalid selector: %w", monitor.Name, err)
	}

	var matched []*corev1.Service
	for _, service := range c.services {
		if selector.Matches(labels.Set(service.Labels)) {
			matched = append(matched, service)
		}
	}
	if len(matched) == 0 {
		return nil, fmt.Errorf("ServiceMonitor %s selects no Service in the component's assets; add the Service or register it in externalServices", monitor.Name)
	}
	if len(matched) > 1 {
		names := make([]string, 0, len(matched))
		for _, service := range matched {
			names = append(names, service.Name)
		}
		return nil, fmt.Errorf("ServiceMonitor %s selects %d Services (%s); a single Service is required to derive the PodMonitoring pod selector", monitor.Name, len(matched), strings.Join(names, ", "))
	}
	return matched[0], nil
}

// podSelectorForService returns the Service's pod selector without the label the
// component framework injects into every control-plane pod.
func podSelectorForService(service *corev1.Service) (map[string]string, error) {
	selector := make(map[string]string, len(service.Spec.Selector))
	for key, value := range service.Spec.Selector {
		if key == hyperv1.ControlPlaneComponentLabel {
			continue
		}
		selector[key] = value
	}
	if len(selector) == 0 {
		return nil, fmt.Errorf("service %s has no pod selector", service.Name)
	}
	return selector, nil
}

// validateServiceMonitorJobLabel rejects a jobLabel the GMP PodMonitoring cannot
// reproduce.
//
// GMP derives the job label from the PodMonitoring name and offers no way to
// select it from a Service. A jobLabel that names a Service label therefore
// changes the job label of every scraped series and is rejected. A jobLabel the
// Service does not carry is inert: prometheus-operator falls back to the Service
// name, which is the name the generator gives the PodMonitoring.
func validateServiceMonitorJobLabel(monitor *prometheusServiceMonitor, service *corev1.Service) error {
	if monitor.Spec.JobLabel == "" {
		return nil
	}
	if value, ok := service.Labels[monitor.Spec.JobLabel]; ok {
		return fmt.Errorf("ServiceMonitor %s: jobLabel %q selects Service label value %q, which a GMP PodMonitoring cannot reproduce", monitor.Name, monitor.Spec.JobLabel, value)
	}
	return nil
}

// validatePodMonitorJobLabel rejects a jobLabel that selects a pod label.
//
// A pod-targeting CoreOS monitor with no effective jobLabel derives the job
// label from its namespace and name, while a GMP PodMonitoring uses its own
// name. An inert jobLabel leaves that known difference untouched; one that
// selects a pod label would change the job label of every scraped series and is
// rejected.
func (c *componentAssets) validatePodMonitorJobLabel(monitor *prometheusPodMonitor) error {
	if monitor.Spec.JobLabel == "" {
		return nil
	}
	for _, podLabels := range c.podLabels {
		if value, ok := podLabels[monitor.Spec.JobLabel]; ok {
			return fmt.Errorf("PodMonitor %s: jobLabel %q selects pod label value %q, which a GMP PodMonitoring cannot reproduce", monitor.Name, monitor.Spec.JobLabel, value)
		}
	}
	return nil
}

// resolveServiceEndpointPort resolves the endpoint to the container port a
// PodMonitoring must name.
func (c *componentAssets) resolveServiceEndpointPort(endpoint prometheusoperatorv1.Endpoint, service *corev1.Service) (intstr.IntOrString, error) {
	var ref intstr.IntOrString
	switch {
	case endpoint.Port != "":
		// A named Service port takes precedence over targetPort.
		var servicePort *corev1.ServicePort
		for i := range service.Spec.Ports {
			if service.Spec.Ports[i].Name == endpoint.Port {
				servicePort = &service.Spec.Ports[i]
				break
			}
		}
		if servicePort == nil {
			return intstr.IntOrString{}, fmt.Errorf("service %s has no port named %q", service.Name, endpoint.Port)
		}
		ref = servicePort.TargetPort
		if (ref.Type == intstr.String && ref.StrVal == "") || (ref.Type == intstr.Int && ref.IntVal == 0) {
			ref = intstr.FromInt(int(servicePort.Port))
		}
	case endpoint.TargetPort != nil:
		ref = *endpoint.TargetPort //nolint:staticcheck // SA1019: legacy CoreOS field still emitted by existing assets
	default:
		return intstr.IntOrString{}, fmt.Errorf("endpoint specifies neither port nor targetPort")
	}
	return c.containerPortRef(ref)
}

// resolvePodEndpointPort resolves a PodMonitor endpoint to its container port.
func (c *componentAssets) resolvePodEndpointPort(endpoint prometheusoperatorv1.PodMetricsEndpoint) (intstr.IntOrString, error) {
	switch {
	case endpoint.Port != nil && *endpoint.Port != "":
		return c.containerPortRef(intstr.FromString(*endpoint.Port))
	case endpoint.PortNumber != nil:
		return c.containerPortRef(intstr.FromInt(int(*endpoint.PortNumber)))
	case endpoint.TargetPort != nil: //nolint:staticcheck // SA1019: legacy CoreOS field still emitted by existing assets
		return c.containerPortRef(*endpoint.TargetPort) //nolint:staticcheck // SA1019: legacy CoreOS field still emitted by existing assets
	default:
		return intstr.IntOrString{}, fmt.Errorf("endpoint specifies neither port, portNumber nor targetPort")
	}
}

// containerPortRef maps a port reference onto a named container port so the GMP
// scrape reports the container metadata label. A numeric reference is rewritten
// to the container port's name when one exists.
func (c *componentAssets) containerPortRef(ref intstr.IntOrString) (intstr.IntOrString, error) {
	if len(c.containerPorts) == 0 {
		// The workload is reconciled in Go, so there is no asset to validate
		// against; keep the reference as written.
		return ref, nil
	}

	if ref.Type == intstr.String {
		for _, port := range c.containerPorts {
			if port.Name == ref.StrVal {
				return ref, nil
			}
		}
		return intstr.IntOrString{}, fmt.Errorf("no container port named %q in the component workload", ref.StrVal)
	}

	for _, port := range c.containerPorts {
		if port.ContainerPort == ref.IntVal {
			if port.Name != "" {
				return intstr.FromString(port.Name), nil
			}
			return ref, nil
		}
	}
	return intstr.IntOrString{}, fmt.Errorf("no container port %d in the component workload", ref.IntVal)
}

// ensureUniqueEndpointPorts makes each endpoint's port unique within the
// PodMonitoring, as GMP requires.
//
// A ServiceMonitor may list two endpoints on the same port to scrape different
// paths, but GMP derives the job name from the port alone, so that becomes a
// duplicate job. When the repeated port is a named container port, the second
// occurrence is written as its number: the scraped container port is the same,
// so the target is unchanged. GMP cannot report the container label for a
// numeric port, so that endpoint also gets a constant rule naming the container
// that declares the port.
func (c *componentAssets) ensureUniqueEndpointPorts(endpoints []endpointDoc) error {
	declared := make(map[string]bool, len(endpoints))
	for i := range endpoints {
		key := endpoints[i].Port.Value.String()
		if !declared[key] {
			declared[key] = true
			continue
		}
		number, ok := c.numericContainerPort(endpoints[i].Port.Value)
		if !ok {
			return fmt.Errorf("endpoints %d and an earlier endpoint both use port %q and no distinct numeric form exists; GMP requires unique ports, so these endpoints need separate monitors", i, key)
		}
		if declared[number.String()] {
			return fmt.Errorf("endpoints %d and an earlier endpoint both use port %q, and its numeric form %q is already in use; GMP requires unique ports", i, key, number.String())
		}
		container := c.portContainers[key]
		if container == "" {
			return fmt.Errorf("endpoint %d: port %q must be written as %s, for which GMP omits the container label, and no single container declares it", i, key, number.String())
		}
		endpoints[i].Port = portRef{Value: number}
		endpoints[i].MetricRelabeling = withContainerRule(endpoints[i].MetricRelabeling, container)
		declared[number.String()] = true
	}
	return nil
}

// withContainerRule inserts a constant container rule after the leading
// service and endpoint rules, so the target-label rules stay together.
func withContainerRule(rules []relabelingRuleDoc, container string) []relabelingRuleDoc {
	at := 0
	for at < len(rules) && len(rules[at].SourceLabels) == 0 && rules[at].Action == "replace" &&
		(rules[at].TargetLabel == serviceTargetLabel || rules[at].TargetLabel == endpointTargetLabel) {
		at++
	}
	return slices.Insert(rules, at, relabelingRuleDoc{TargetLabel: containerTargetLabel, Replacement: container, Action: "replace"})
}

// numericContainerPort returns the container port number for a named port
// reference, reporting false when the reference is already numeric or the
// workload has no such named port.
func (c *componentAssets) numericContainerPort(ref intstr.IntOrString) (intstr.IntOrString, bool) {
	if ref.Type == intstr.Int {
		return ref, false
	}
	for _, port := range c.containerPorts {
		if port.Name == ref.StrVal {
			return intstr.FromInt(int(port.ContainerPort)), true
		}
	}
	return intstr.IntOrString{}, false
}

// clusterIndependentServerName replaces the namespace label of a Service DNS
// server name with the HCP_NAMESPACE placeholder.
//
// A server name that targets a Service in the control-plane namespace is
// per-cluster: the CoreOS adapters that need one derive it from the live
// namespace at reconcile time, so the value in the asset is not what runs.
// Writing the placeholder keeps the generated asset independent of the cluster
// it was generated from without changing the CoreOS asset. The placeholder is
// not a resolvable host name: the runtime reconciliation must substitute the
// live namespace, as the CoreOS NTO adapter does, before the asset is applied.
// Names that are not Service DNS names (for example "etcd-client", or
// "kube-apiserver.svc" with no namespace label) are left alone.
func clusterIndependentServerName(serverName string) string {
	labels := strings.Split(serverName, ".")
	for i, label := range labels {
		if label == "svc" && i >= 2 {
			labels[i-1] = placeholderNamespace
			break
		}
	}
	return strings.Join(labels, ".")
}

// convertTLS translates a CoreOS TLS configuration. GMP only accepts Secret
// references, so a ConfigMap CA is replaced with the existing Secret that holds
// the same CA.
func convertTLS(config *prometheusoperatorv1.SafeTLSConfig) (*tlsDoc, error) {
	if config == nil {
		return nil, nil
	}

	tls := &tlsDoc{
		InsecureSkipVerify: config.InsecureSkipVerify != nil && *config.InsecureSkipVerify,
	}
	if config.ServerName != nil {
		tls.ServerName = clusterIndependentServerName(*config.ServerName)
	}
	if config.MinVersion != nil {
		tls.MinVersion = string(*config.MinVersion)
	}
	if config.MaxVersion != nil {
		tls.MaxVersion = string(*config.MaxVersion)
	}

	var err error
	tls.CA, err = convertCASecret(config.CA)
	if err != nil {
		return nil, err
	}
	tls.Cert, err = convertCertSecretRef(config.Cert)
	if err != nil {
		return nil, err
	}
	if config.KeySecret != nil && config.KeySecret.Name != "" {
		tls.Key = &secretRefDoc{Secret: secretKeyRefDoc{Name: config.KeySecret.Name, Key: config.KeySecret.Key}}
	}
	if (tls.Cert == nil) != (tls.Key == nil) {
		return nil, fmt.Errorf("GMP requires the client certificate and key to be provided together")
	}
	return tls, nil
}

func convertCASecret(ca prometheusoperatorv1.SecretOrConfigMap) (*secretRefDoc, error) {
	switch {
	case ca.Secret != nil && ca.Secret.Name != "" && ca.ConfigMap != nil && ca.ConfigMap.Name != "":
		return nil, fmt.Errorf("TLS CA references both a Secret and a ConfigMap")
	case ca.Secret != nil && ca.Secret.Name != "":
		if _, forbidden := forbiddenCASecretNames[ca.Secret.Name]; forbidden {
			return nil, fmt.Errorf("TLS CA references Secret %q, which holds a CA private key; reference the CA-only mirror instead", ca.Secret.Name)
		}
		return &secretRefDoc{Secret: secretKeyRefDoc{Name: ca.Secret.Name, Key: ca.Secret.Key}}, nil
	case ca.ConfigMap != nil && ca.ConfigMap.Name != "":
		secretName, ok := caConfigMapSecrets[ca.ConfigMap.Name]
		if !ok {
			return nil, fmt.Errorf("TLS CA ConfigMap %q has no Secret counterpart; add it to caConfigMapSecrets", ca.ConfigMap.Name)
		}
		return &secretRefDoc{Secret: secretKeyRefDoc{Name: secretName, Key: ca.ConfigMap.Key}}, nil
	default:
		return nil, nil
	}
}

func convertCertSecretRef(source prometheusoperatorv1.SecretOrConfigMap) (*secretRefDoc, error) {
	switch {
	case source.Secret != nil && source.Secret.Name != "" && source.ConfigMap != nil && source.ConfigMap.Name != "":
		return nil, fmt.Errorf("TLS cert references both a Secret and a ConfigMap")
	case source.Secret != nil && source.Secret.Name != "":
		return &secretRefDoc{Secret: secretKeyRefDoc{Name: source.Secret.Name, Key: source.Secret.Key}}, nil
	case source.ConfigMap != nil && source.ConfigMap.Name != "":
		return nil, fmt.Errorf("TLS cert references a ConfigMap; GMP requires a Secret")
	default:
		return nil, nil
	}
}

// validateNamespaceSelector rejects selectors that a namespaced PodMonitoring
// cannot express.
func validateNamespaceSelector(selector prometheusoperatorv1.NamespaceSelector) error {
	if selector.Any {
		return fmt.Errorf("cross-namespace selection is not representable in a namespaced GMP PodMonitoring")
	}
	for _, namespace := range selector.MatchNames {
		if namespace != "" && namespace != placeholderNamespace {
			return fmt.Errorf("namespace %q is outside the hosted control plane namespace; GMP PodMonitoring is namespace-scoped", namespace)
		}
	}
	return nil
}

func copyStringMap(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	copied := make(map[string]string, len(source))
	for key, value := range source {
		copied[key] = value
	}
	return copied
}

// templateExpression matches the Go template expressions used by the templated
// component assets (for example etcd's {{ .Name }}).
var templateExpression = regexp.MustCompile(`\{\{[^{}]*\}\}`)

// templatePlaceholders protects Go template expressions while the asset is
// parsed as YAML and restores them in the generated output, so a generated asset
// keeps the exact templating the source asset used.
type templatePlaceholders struct {
	byExpression  map[string]string
	byPlaceholder map[string]string
}

func newTemplatePlaceholders() *templatePlaceholders {
	return &templatePlaceholders{
		byExpression:  map[string]string{},
		byPlaceholder: map[string]string{},
	}
}

func (p *templatePlaceholders) protect(raw []byte) []byte {
	return templateExpression.ReplaceAllFunc(raw, func(expression []byte) []byte {
		key := string(expression)
		placeholder, ok := p.byExpression[key]
		if !ok {
			// The trailing delimiter keeps one placeholder from being a prefix of
			// another (zzgmpgentemplate1zz vs zzgmpgentemplate10zz).
			placeholder = fmt.Sprintf("zzgmpgentemplate%dzz", len(p.byExpression))
			p.byExpression[key] = placeholder
			p.byPlaceholder[placeholder] = key
		}
		return []byte(placeholder)
	})
}

func (p *templatePlaceholders) restore(raw []byte) []byte {
	placeholders := make([]string, 0, len(p.byPlaceholder))
	for placeholder := range p.byPlaceholder {
		placeholders = append(placeholders, placeholder)
	}
	sort.Strings(placeholders)
	for _, placeholder := range placeholders {
		raw = bytes.ReplaceAll(raw, []byte(placeholder), []byte(p.byPlaceholder[placeholder]))
	}
	return raw
}

// PodMonitoring document types. They mirror the subset of the GMP API the
// generator emits and give the YAML encoder full control over field order.
type podMonitoringDoc struct {
	APIVersion string      `yaml:"apiVersion"`
	Kind       string      `yaml:"kind"`
	Metadata   metadataDoc `yaml:"metadata"`
	Spec       specDoc     `yaml:"spec"`
}

type metadataDoc struct {
	Name        string            `yaml:"name"`
	Namespace   string            `yaml:"namespace,omitempty"`
	Labels      map[string]string `yaml:"labels,omitempty"`
	Annotations map[string]string `yaml:"annotations,omitempty"`
}

type specDoc struct {
	Selector     labelSelectorDoc `yaml:"selector"`
	TargetLabels *targetLabelsDoc `yaml:"targetLabels,omitempty"`
	Endpoints    []endpointDoc    `yaml:"endpoints"`
}

type targetLabelsDoc struct {
	Metadata []string `yaml:"metadata"`
}

type labelSelectorDoc struct {
	MatchLabels      map[string]string             `yaml:"matchLabels,omitempty"`
	MatchExpressions []labelSelectorRequirementDoc `yaml:"matchExpressions,omitempty"`
}

// labelSelectorRequirementDoc spells out the YAML field names rather than
// relying on the encoder's default lowercasing of the Kubernetes types.
type labelSelectorRequirementDoc struct {
	Key      string   `yaml:"key"`
	Operator string   `yaml:"operator"`
	Values   []string `yaml:"values,omitempty"`
}

type endpointDoc struct {
	Port             portRef             `yaml:"port"`
	Scheme           string              `yaml:"scheme,omitempty"`
	Path             string              `yaml:"path,omitempty"`
	Interval         string              `yaml:"interval"`
	Timeout          string              `yaml:"timeout,omitempty"`
	Params           map[string][]string `yaml:"params,omitempty"`
	ProxyURL         string              `yaml:"proxyUrl,omitempty"`
	TLS              *tlsDoc             `yaml:"tls,omitempty"`
	MetricRelabeling []relabelingRuleDoc `yaml:"metricRelabeling,omitempty"`
}

type relabelingRuleDoc struct {
	SourceLabels []string `yaml:"sourceLabels,omitempty"`
	Separator    string   `yaml:"separator,omitempty"`
	TargetLabel  string   `yaml:"targetLabel,omitempty"`
	Regex        string   `yaml:"regex,omitempty"`
	Modulus      uint64   `yaml:"modulus,omitempty"`
	Replacement  string   `yaml:"replacement,omitempty"`
	Action       string   `yaml:"action,omitempty"`
}

type tlsDoc struct {
	ServerName         string        `yaml:"serverName,omitempty"`
	InsecureSkipVerify bool          `yaml:"insecureSkipVerify,omitempty"`
	MinVersion         string        `yaml:"minVersion,omitempty"`
	MaxVersion         string        `yaml:"maxVersion,omitempty"`
	CA                 *secretRefDoc `yaml:"ca,omitempty"`
	Cert               *secretRefDoc `yaml:"cert,omitempty"`
	Key                *secretRefDoc `yaml:"key,omitempty"`
}

type secretRefDoc struct {
	Secret secretKeyRefDoc `yaml:"secret"`
}

type secretKeyRefDoc struct {
	Name string `yaml:"name"`
	Key  string `yaml:"key,omitempty"`
}

// portRef renders an IntOrString as either a number or a string.
type portRef struct {
	Value intstr.IntOrString
}

// UnmarshalYAML accepts both forms the generator emits, so a generated document
// can be parsed back for validation.
func (p *portRef) UnmarshalYAML(value *yamlv3.Node) error {
	var number int32
	if err := value.Decode(&number); err == nil {
		p.Value = intstr.FromInt32(number)
		return nil
	}
	var name string
	if err := value.Decode(&name); err != nil {
		return fmt.Errorf("port must be a number or a string: %w", err)
	}
	p.Value = intstr.FromString(name)
	return nil
}

//nolint:unparam // yaml.Marshaler requires an error return
func (p portRef) MarshalYAML() (any, error) {
	if p.Value.Type == intstr.Int {
		return p.Value.IntVal, nil
	}
	return p.Value.StrVal, nil
}

// newYAMLEncoder returns an encoder with the two-space indentation used by the
// committed assets.
func newYAMLEncoder(w io.Writer) *yamlv3.Encoder {
	encoder := yamlv3.NewEncoder(w)
	encoder.SetIndent(2)
	return encoder
}

// strictUnmarshal rejects fields the target API type does not know. For CoreOS
// sources, known fields the generator does not translate are rejected
// separately by rejectUntranslated.
func strictUnmarshal(raw []byte, into any) error {
	return yaml.UnmarshalStrict(raw, into)
}
