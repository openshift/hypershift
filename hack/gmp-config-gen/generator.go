package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/oapi"
	kubemanifests "github.com/openshift/hypershift/pkg/manifests/cpo"
	"github.com/openshift/hypershift/support/config"

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
	// assets. The component framework rewrites it to the real HCP namespace.
	placeholderNamespace = "HCP_NAMESPACE"

	// defaultScrapeInterval matches the prometheus-operator default. GMP
	// requires an explicit interval, so it is written into every endpoint.
	defaultScrapeInterval = "30s"
	defaultMetricsPath    = "/metrics"
)

// caSecretOverrides maps a component asset directory to the CA-only Secret that
// must be used when the CoreOS monitor references its CA as a ConfigMap, for the
// cases where the default "<monitor-name>-metrics-client-ca" name does not
// apply.
var caSecretOverrides = map[string]string{
	// etcd shards share the etcd asset directory and the single CA Secret.
	"etcd": "etcd-metrics-client-ca",
	// The kube-apiserver monitor historically ships as kas-metrics-client-ca.
	"kube-apiserver": "kas-metrics-client-ca",
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
			component.containerPorts = append(component.containerPorts, containerPorts(deployment.Spec.Template.Spec)...)
		case "StatefulSet":
			statefulSet := &appsv1.StatefulSet{}
			if err := yaml.Unmarshal(prepared, statefulSet); err != nil {
				return nil, fmt.Errorf("failed to parse StatefulSet in %s: %w", entry.Name(), err)
			}
			component.containerPorts = append(component.containerPorts, containerPorts(statefulSet.Spec.Template.Spec)...)
		}
	}

	external, err := externalServices(componentDir)
	if err != nil {
		return nil, err
	}
	component.services = append(component.services, external...)
	return component, nil
}

func containerPorts(spec corev1.PodSpec) []corev1.ContainerPort {
	var ports []corev1.ContainerPort
	for _, container := range spec.Containers {
		ports = append(ports, container.Ports...)
	}
	return ports
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
	if monitor.Spec.JobLabel != "" {
		return nil, fmt.Errorf("ServiceMonitor %s: jobLabel is not representable in a GMP PodMonitoring", monitor.Name)
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
			converted.TLS, err = c.convertTLS(&endpoint.TLSConfig.SafeTLSConfig, monitor.Name)
			if err != nil {
				return nil, fmt.Errorf("ServiceMonitor %s endpoint %d: %w", monitor.Name, i, err)
			}
		}
		if err := rejectUntranslated(fmt.Sprintf("spec.endpoints[%d]", i), untranslatedServiceEndpoint(endpoint)); err != nil {
			return nil, fmt.Errorf("ServiceMonitor %s: %w", monitor.Name, err)
		}
		endpoints = append(endpoints, converted)
	}
	if err := rejectUntranslated("spec", untranslatedServiceMonitorSpec(monitor.Spec)); err != nil {
		return nil, fmt.Errorf("ServiceMonitor %s: %w", monitor.Name, err)
	}

	return &podMonitoringDoc{
		APIVersion: gmpAPIVersion,
		Kind:       "PodMonitoring",
		Metadata: metadataDoc{
			Name:        monitor.Name,
			Namespace:   monitor.Namespace,
			Labels:      copyStringMap(monitor.Labels),
			Annotations: copyStringMap(monitor.Annotations),
		},
		Spec: specDoc{
			// The monitor's own selector matches Service labels and was consumed
			// by serviceForMonitor; only the Service's pod selector applies to pods.
			Selector:  labelSelectorDoc{MatchLabels: podSelector},
			Endpoints: endpoints,
		},
	}, nil
}

// convertPodMonitor translates a PodMonitor into a PodMonitoring.
func (c *componentAssets) convertPodMonitor(monitor *prometheusPodMonitor) (*podMonitoringDoc, error) {
	if err := validateNamespaceSelector(monitor.Spec.NamespaceSelector); err != nil {
		return nil, fmt.Errorf("PodMonitor %s: %w", monitor.Name, err)
	}
	if monitor.Spec.JobLabel != "" {
		return nil, fmt.Errorf("PodMonitor %s: jobLabel is not representable in a GMP PodMonitoring", monitor.Name)
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
		if len(endpoint.RelabelConfigs) > 0 {
			return nil, fmt.Errorf("PodMonitor %s endpoint %d: target relabelings are not representable in a GMP PodMonitoring", monitor.Name, i)
		}
		if endpoint.HonorLabels || endpoint.HonorTimestamps != nil || endpoint.FilterRunning != nil {
			return nil, fmt.Errorf("PodMonitor %s endpoint %d: honorLabels, honorTimestamps and filterRunning are not translated yet", monitor.Name, i)
		}
		converted.TLS, err = c.convertTLS(endpoint.TLSConfig, monitor.Name)
		if err != nil {
			return nil, fmt.Errorf("PodMonitor %s endpoint %d: %w", monitor.Name, i, err)
		}
		if err := rejectUntranslated(fmt.Sprintf("spec.podMetricsEndpoints[%d]", i), untranslatedPodEndpoint(endpoint)); err != nil {
			return nil, fmt.Errorf("PodMonitor %s: %w", monitor.Name, err)
		}
		endpoints = append(endpoints, converted)
	}
	if err := rejectUntranslated("spec", untranslatedPodMonitorSpec(monitor.Spec)); err != nil {
		return nil, fmt.Errorf("PodMonitor %s: %w", monitor.Name, err)
	}

	return &podMonitoringDoc{
		APIVersion: gmpAPIVersion,
		Kind:       "PodMonitoring",
		Metadata: metadataDoc{
			Name:        monitor.Name,
			Namespace:   monitor.Namespace,
			Labels:      copyStringMap(monitor.Labels),
			Annotations: copyStringMap(monitor.Annotations),
		},
		Spec: specDoc{
			Selector:  labelSelectorDoc{MatchLabels: copyStringMap(monitor.Spec.Selector.MatchLabels), MatchExpressions: monitor.Spec.Selector.MatchExpressions},
			Endpoints: endpoints,
		},
	}, nil
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
	return remainder
}

func untranslatedPodMonitorSpec(spec prometheusoperatorv1.PodMonitorSpec) *prometheusoperatorv1.PodMonitorSpec {
	remainder := spec.DeepCopy()
	remainder.Selector = metav1.LabelSelector{}
	remainder.NamespaceSelector = prometheusoperatorv1.NamespaceSelector{}
	remainder.PodMetricsEndpoints = nil
	return remainder
}

func untranslatedServiceEndpoint(endpoint prometheusoperatorv1.Endpoint) *prometheusoperatorv1.Endpoint {
	remainder := endpoint.DeepCopy()
	remainder.Port = ""
	remainder.TargetPort = nil
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
	remainder.TargetPort = nil
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
		bearerTokenSecret: endpoint.BearerTokenSecret,
		authorization:     endpoint.Authorization,
		basicAuth:         endpoint.BasicAuth,
		oauth2:            endpoint.OAuth2,
		proxyURL:          stringValue(endpoint.ProxyURL),
	}
}

func endpointHTTPConfigFromPodEndpoint(endpoint prometheusoperatorv1.PodMetricsEndpoint) endpointHTTPConfigFields {
	return endpointHTTPConfigFields{
		bearerTokenSecret: endpoint.BearerTokenSecret,
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
func convertMetricRelabeling(source []prometheusoperatorv1.RelabelConfig) ([]relabelingRuleDoc, error) {
	if len(source) == 0 {
		return nil, nil
	}
	result := make([]relabelingRuleDoc, 0, len(source))
	for i, rule := range source {
		action := strings.ToLower(rule.Action)
		switch action {
		case "", "replace", "lowercase", "uppercase", "keep", "drop", "keepequal", "dropequal", "hashmod", "labeldrop", "labelkeep":
		default:
			return nil, fmt.Errorf("metric relabeling %d: action %q is not supported by GMP", i, rule.Action)
		}
		if rule.Separator != nil && *rule.Separator == "" {
			return nil, fmt.Errorf("metric relabeling %d: empty separator cannot be preserved by GMP", i)
		}
		if rule.Replacement != nil && *rule.Replacement == "" {
			return nil, fmt.Errorf("metric relabeling %d: empty replacement cannot be preserved by GMP", i)
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
		return nil, fmt.Errorf("Service %s has no pod selector", service.Name)
	}
	return selector, nil
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
			return intstr.IntOrString{}, fmt.Errorf("Service %s has no port named %q", service.Name, endpoint.Port)
		}
		ref = servicePort.TargetPort
		if (ref.Type == intstr.String && ref.StrVal == "") || (ref.Type == intstr.Int && ref.IntVal == 0) {
			ref = intstr.FromInt(int(servicePort.Port))
		}
	case endpoint.TargetPort != nil:
		ref = *endpoint.TargetPort
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
	case endpoint.TargetPort != nil:
		return c.containerPortRef(*endpoint.TargetPort)
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

// convertTLS translates a CoreOS TLS configuration. GMP only accepts Secret
// references, so a ConfigMap CA is replaced with the CA-only Secret the
// component mirrors at reconcile time.
func (c *componentAssets) convertTLS(config *prometheusoperatorv1.SafeTLSConfig, monitorName string) (*tlsDoc, error) {
	if config == nil {
		return nil, nil
	}

	tls := &tlsDoc{
		InsecureSkipVerify: config.InsecureSkipVerify != nil && *config.InsecureSkipVerify,
	}
	if config.ServerName != nil {
		tls.ServerName = *config.ServerName
	}
	if config.MinVersion != nil {
		tls.MinVersion = string(*config.MinVersion)
	}
	if config.MaxVersion != nil {
		tls.MaxVersion = string(*config.MaxVersion)
	}

	var err error
	tls.CA, err = c.convertCASecret(config.CA, monitorName)
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

func (c *componentAssets) convertCASecret(ca prometheusoperatorv1.SecretOrConfigMap, monitorName string) (*secretRefDoc, error) {
	switch {
	case ca.Secret != nil && ca.Secret.Name != "" && ca.ConfigMap != nil && ca.ConfigMap.Name != "":
		return nil, fmt.Errorf("TLS CA references both a Secret and a ConfigMap")
	case ca.Secret != nil && ca.Secret.Name != "":
		return &secretRefDoc{Secret: secretKeyRefDoc{Name: ca.Secret.Name, Key: ca.Secret.Key}}, nil
	case ca.ConfigMap != nil && ca.ConfigMap.Name != "":
		return &secretRefDoc{Secret: secretKeyRefDoc{Name: c.caSecretName(monitorName), Key: ca.ConfigMap.Key}}, nil
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

// caSecretName returns the CA-only Secret that mirrors the component's CA
// ConfigMap.
func (c *componentAssets) caSecretName(monitorName string) string {
	if override, ok := caSecretOverrides[filepath.Base(c.dir)]; ok {
		return override
	}
	return monitorName + "-metrics-client-ca"
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
	Selector  labelSelectorDoc `yaml:"selector"`
	Endpoints []endpointDoc    `yaml:"endpoints"`
}

type labelSelectorDoc struct {
	MatchLabels      map[string]string                 `yaml:"matchLabels,omitempty"`
	MatchExpressions []metav1.LabelSelectorRequirement `yaml:"matchExpressions,omitempty"`
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

// strictUnmarshal rejects fields the vendored CoreOS API types do not know.
// Known fields the generator does not translate are rejected separately by
// rejectUntranslated.
func strictUnmarshal(raw []byte, into any) error {
	return yaml.UnmarshalStrict(raw, into)
}
