package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"sigs.k8s.io/yaml"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	yamlv3 "gopkg.in/yaml.v3"
)

func TestComponentAssets_ConvertServiceMonitor(t *testing.T) {
	baseService := func() *corev1.Service {
		return &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "kube-controller-manager",
				Labels: map[string]string{"app": "kube-controller-manager"},
			},
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{
					"app":                              "kube-controller-manager",
					hyperv1.ControlPlaneComponentLabel: "kube-controller-manager",
				},
				Ports: []corev1.ServicePort{{
					Name:       "metrics",
					Port:       443,
					TargetPort: intstr.FromInt(10257),
				}},
			},
		}
	}

	baseMonitor := func() *prometheusServiceMonitor {
		return &prometheusServiceMonitor{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "kube-controller-manager",
				Namespace:   placeholderNamespace,
				Annotations: map[string]string{"hypershift.openshift.io/metrics-endpoint": "kube-controller-manager"},
			},
			Spec: prometheusoperatorv1.ServiceMonitorSpec{
				Selector:  metav1.LabelSelector{MatchLabels: map[string]string{"app": "kube-controller-manager"}},
				Endpoints: []prometheusoperatorv1.Endpoint{{Port: "metrics"}},
			},
		}
	}

	httpsScheme := prometheusoperatorv1.SchemeHTTPS

	testCases := []struct {
		name             string
		assets           *componentAssets
		monitor          *prometheusServiceMonitor
		expectError      string
		validateDocument func(t *testing.T, doc *podMonitoringDoc)
	}{
		{
			name: "When the Service port targets a named container port, it should select the Service pods and name the container port",
			assets: &componentAssets{
				services:       []*corev1.Service{baseService()},
				containerPorts: []corev1.ContainerPort{{Name: "metrics", ContainerPort: 10257}},
			},
			monitor: baseMonitor(),
			validateDocument: func(t *testing.T, doc *podMonitoringDoc) {
				assert.Equal(t, gmpAPIVersion, doc.APIVersion)
				assert.Equal(t, "PodMonitoring", doc.Kind)
				assert.Equal(t, "kube-controller-manager", doc.Metadata.Name)
				assert.Equal(t, placeholderNamespace, doc.Metadata.Namespace)
				assert.Equal(t, map[string]string{"hypershift.openshift.io/metrics-endpoint": "kube-controller-manager"}, doc.Metadata.Annotations)
				// The framework-injected label must not leak into the pod selector.
				assert.Equal(t, map[string]string{"app": "kube-controller-manager"}, doc.Spec.Selector.MatchLabels)
				require.Len(t, doc.Spec.Endpoints, 1)
				assert.Equal(t, portRef{Value: intstr.FromString("metrics")}, doc.Spec.Endpoints[0].Port)
				assert.Equal(t, "http", doc.Spec.Endpoints[0].Scheme)
				assert.Equal(t, defaultMetricsPath, doc.Spec.Endpoints[0].Path)
				assert.Equal(t, "60s", doc.Spec.Endpoints[0].Interval)
				// GMP adds only the pod metadata labels prometheus-operator adds,
				// and constant rules restore its service and endpoint labels.
				assert.Equal(t, &targetLabelsDoc{Metadata: []string{"container", "pod"}}, doc.Spec.TargetLabels)
				assert.Equal(t, []relabelingRuleDoc{
					{TargetLabel: "service", Replacement: "kube-controller-manager", Action: "replace"},
					{TargetLabel: "endpoint", Replacement: "metrics", Action: "replace"},
				}, doc.Spec.Endpoints[0].MetricRelabeling)
			},
		},
		{
			name: "When the Service port has no targetPort, it should resolve the container port by the Service port number",
			assets: &componentAssets{
				services: []*corev1.Service{func() *corev1.Service {
					service := baseService()
					service.Spec.Ports[0].TargetPort = intstr.IntOrString{}
					return service
				}()},
				containerPorts: []corev1.ContainerPort{{Name: "metrics", ContainerPort: 443}},
			},
			monitor: baseMonitor(),
			validateDocument: func(t *testing.T, doc *podMonitoringDoc) {
				assert.Equal(t, portRef{Value: intstr.FromString("metrics")}, doc.Spec.Endpoints[0].Port)
			},
		},
		{
			name: "When the endpoint sets targetPort instead of a Service port name, it should use the targetPort",
			assets: &componentAssets{
				services:       []*corev1.Service{baseService()},
				containerPorts: []corev1.ContainerPort{{Name: "metrics", ContainerPort: 10257}},
			},
			monitor: func() *prometheusServiceMonitor {
				monitor := baseMonitor()
				monitor.Spec.Endpoints = []prometheusoperatorv1.Endpoint{{TargetPort: ptr(intstr.FromInt(10257))}}
				return monitor
			}(),
			validateDocument: func(t *testing.T, doc *podMonitoringDoc) {
				assert.Equal(t, portRef{Value: intstr.FromString("metrics")}, doc.Spec.Endpoints[0].Port)
				// prometheus-operator labels a targetPort endpoint with the
				// targetPort as written, not the resolved container port name.
				assert.Equal(t, []relabelingRuleDoc{
					{TargetLabel: "service", Replacement: "kube-controller-manager", Action: "replace"},
					{TargetLabel: "endpoint", Replacement: "10257", Action: "replace"},
				}, doc.Spec.Endpoints[0].MetricRelabeling)
			},
		},
		{
			name: "When the endpoint configures scheme, path, interval and timeout, it should carry them into the PodMonitoring",
			assets: &componentAssets{
				services:       []*corev1.Service{baseService()},
				containerPorts: []corev1.ContainerPort{{Name: "metrics", ContainerPort: 10257}},
			},
			monitor: func() *prometheusServiceMonitor {
				monitor := baseMonitor()
				monitor.Spec.Endpoints[0].Scheme = &httpsScheme
				monitor.Spec.Endpoints[0].Path = "/metrics/healthz"
				monitor.Spec.Endpoints[0].Interval = "15s"
				monitor.Spec.Endpoints[0].ScrapeTimeout = "10s"
				return monitor
			}(),
			validateDocument: func(t *testing.T, doc *podMonitoringDoc) {
				assert.Equal(t, "https", doc.Spec.Endpoints[0].Scheme)
				assert.Equal(t, "/metrics/healthz", doc.Spec.Endpoints[0].Path)
				assert.Equal(t, "15s", doc.Spec.Endpoints[0].Interval)
				assert.Equal(t, "10s", doc.Spec.Endpoints[0].Timeout)
			},
		},
		{
			name: "When the endpoint CA is the root-ca ConfigMap, it should reference the root-ca-cert mirror Secret",
			assets: &componentAssets{
				services:       []*corev1.Service{baseService()},
				containerPorts: []corev1.ContainerPort{{Name: "metrics", ContainerPort: 10257}},
			},
			monitor: func() *prometheusServiceMonitor {
				monitor := baseMonitor()
				monitor.Spec.Endpoints[0].TLSConfig = &prometheusoperatorv1.TLSConfig{
					SafeTLSConfig: prometheusoperatorv1.SafeTLSConfig{
						CA: prometheusoperatorv1.SecretOrConfigMap{
							ConfigMap: &corev1.ConfigMapKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{Name: "root-ca"},
								Key:                  "ca.crt",
							},
						},
					},
				}
				return monitor
			}(),
			validateDocument: func(t *testing.T, doc *podMonitoringDoc) {
				require.NotNil(t, doc.Spec.Endpoints[0].TLS)
				assert.Equal(t, &secretRefDoc{Secret: secretKeyRefDoc{Name: "root-ca-cert", Key: "ca.crt"}}, doc.Spec.Endpoints[0].TLS.CA)
				assert.Nil(t, doc.Spec.Endpoints[0].TLS.Cert)
				assert.Nil(t, doc.Spec.Endpoints[0].TLS.Key)
			},
		},
		{
			name: "When the endpoint uses file-based TLS, it should fail generation",
			assets: &componentAssets{
				services:       []*corev1.Service{baseService()},
				containerPorts: []corev1.ContainerPort{{Name: "metrics", ContainerPort: 10257}},
			},
			monitor: func() *prometheusServiceMonitor {
				monitor := baseMonitor()
				monitor.Spec.Endpoints[0].TLSConfig = &prometheusoperatorv1.TLSConfig{
					TLSFilesConfig: prometheusoperatorv1.TLSFilesConfig{CAFile: "/etc/tls/ca.crt"},
				}
				return monitor
			}(),
			expectError: "file-based TLS references are not representable",
		},
		{
			name: "When the monitor selects no Service, it should fail generation",
			assets: &componentAssets{
				services:       []*corev1.Service{baseService()},
				containerPorts: []corev1.ContainerPort{{Name: "metrics", ContainerPort: 10257}},
			},
			monitor: func() *prometheusServiceMonitor {
				monitor := baseMonitor()
				monitor.Spec.Selector = metav1.LabelSelector{MatchLabels: map[string]string{"app": "missing"}}
				return monitor
			}(),
			expectError: "selects no Service",
		},
		{
			name: "When the monitor selects more than one Service, it should fail generation",
			assets: &componentAssets{
				services: []*corev1.Service{baseService(), func() *corev1.Service {
					service := baseService()
					service.Name = "second"
					return service
				}()},
				containerPorts: []corev1.ContainerPort{{Name: "metrics", ContainerPort: 10257}},
			},
			monitor:     baseMonitor(),
			expectError: "selects 2 Services",
		},
		{
			name: "When the monitor sets a jobLabel that selects a Service label, it should fail generation",
			assets: &componentAssets{
				services:       []*corev1.Service{baseService()},
				containerPorts: []corev1.ContainerPort{{Name: "metrics", ContainerPort: 10257}},
			},
			monitor: func() *prometheusServiceMonitor {
				monitor := baseMonitor()
				monitor.Spec.JobLabel = "app"
				return monitor
			}(),
			expectError: `jobLabel "app" selects Service label value "kube-controller-manager"`,
		},
		{
			name: "When jobLabel is absent from the Service and the monitor shares its name, it should accept the prometheus-operator fallback",
			assets: &componentAssets{
				services:       []*corev1.Service{baseService()},
				containerPorts: []corev1.ContainerPort{{Name: "metrics", ContainerPort: 10257}},
			},
			monitor: func() *prometheusServiceMonitor {
				monitor := baseMonitor()
				monitor.Spec.JobLabel = "component"
				return monitor
			}(),
			validateDocument: func(t *testing.T, doc *podMonitoringDoc) {
				assert.Equal(t, portRef{Value: intstr.FromString("metrics")}, doc.Spec.Endpoints[0].Port)
			},
		},
		{
			name: "When the monitor name differs from its Service, it should name the PodMonitoring after the Service to keep the job label",
			assets: &componentAssets{
				services:       []*corev1.Service{baseService()},
				containerPorts: []corev1.ContainerPort{{Name: "metrics", ContainerPort: 10257}},
			},
			monitor: func() *prometheusServiceMonitor {
				monitor := baseMonitor()
				monitor.Name = "kube-controller-manager-monitor"
				monitor.Spec.JobLabel = "component"
				return monitor
			}(),
			validateDocument: func(t *testing.T, doc *podMonitoringDoc) {
				assert.Equal(t, "kube-controller-manager", doc.Metadata.Name)
			},
		},
		{
			name: "When the monitor selects another namespace, it should fail generation",
			assets: &componentAssets{
				services:       []*corev1.Service{baseService()},
				containerPorts: []corev1.ContainerPort{{Name: "metrics", ContainerPort: 10257}},
			},
			monitor: func() *prometheusServiceMonitor {
				monitor := baseMonitor()
				monitor.Spec.NamespaceSelector = prometheusoperatorv1.NamespaceSelector{Any: true}
				return monitor
			}(),
			expectError: "cross-namespace selection",
		},
		{
			name: "When an endpoint has target relabelings, it should fail generation",
			assets: &componentAssets{
				services:       []*corev1.Service{baseService()},
				containerPorts: []corev1.ContainerPort{{Name: "metrics", ContainerPort: 10257}},
			},
			monitor: func() *prometheusServiceMonitor {
				monitor := baseMonitor()
				monitor.Spec.Endpoints[0].RelabelConfigs = []prometheusoperatorv1.RelabelConfig{{Action: "labeldrop"}}
				return monitor
			}(),
			expectError: "target relabelings are not representable",
		},
		{
			name: "When the monitor selects Services with matchExpressions, it should not apply them to the pod selector",
			assets: &componentAssets{
				services:       []*corev1.Service{baseService()},
				containerPorts: []corev1.ContainerPort{{Name: "metrics", ContainerPort: 10257}},
			},
			monitor: func() *prometheusServiceMonitor {
				monitor := baseMonitor()
				monitor.Spec.Selector = metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{{
					Key:      "app",
					Operator: metav1.LabelSelectorOpIn,
					Values:   []string{"kube-controller-manager"},
				}}}
				return monitor
			}(),
			validateDocument: func(t *testing.T, doc *podMonitoringDoc) {
				assert.Equal(t, map[string]string{"app": "kube-controller-manager"}, doc.Spec.Selector.MatchLabels)
				assert.Empty(t, doc.Spec.Selector.MatchExpressions)
			},
		},
		{
			name: "When the monitor has metadata labels, it should carry them into the PodMonitoring",
			assets: &componentAssets{
				services:       []*corev1.Service{baseService()},
				containerPorts: []corev1.ContainerPort{{Name: "metrics", ContainerPort: 10257}},
			},
			monitor: func() *prometheusServiceMonitor {
				monitor := baseMonitor()
				monitor.Labels = map[string]string{"team": "hypershift"}
				return monitor
			}(),
			validateDocument: func(t *testing.T, doc *podMonitoringDoc) {
				assert.Equal(t, map[string]string{"team": "hypershift"}, doc.Metadata.Labels)
			},
		},
		{
			name: "When an endpoint has static metric relabelings, it should preserve them",
			assets: &componentAssets{
				services:       []*corev1.Service{baseService()},
				containerPorts: []corev1.ContainerPort{{Name: "metrics", ContainerPort: 10257}},
			},
			monitor: func() *prometheusServiceMonitor {
				monitor := baseMonitor()
				monitor.Spec.Endpoints[0].MetricRelabelConfigs = []prometheusoperatorv1.RelabelConfig{{Action: "keep", Regex: "up"}}
				return monitor
			}(),
			validateDocument: func(t *testing.T, doc *podMonitoringDoc) {
				require.Len(t, doc.Spec.Endpoints, 1)
				assert.Equal(t, []relabelingRuleDoc{
					{TargetLabel: "service", Replacement: "kube-controller-manager", Action: "replace"},
					{TargetLabel: "endpoint", Replacement: "metrics", Action: "replace"},
					{Action: "keep", Regex: "up"},
				}, doc.Spec.Endpoints[0].MetricRelabeling)
			},
		},
		{
			name: "When an endpoint sets a field the generator does not translate, it should fail generation naming the field",
			assets: &componentAssets{
				services:       []*corev1.Service{baseService()},
				containerPorts: []corev1.ContainerPort{{Name: "metrics", ContainerPort: 10257}},
			},
			monitor: func() *prometheusServiceMonitor {
				monitor := baseMonitor()
				monitor.Spec.Endpoints[0].FollowRedirects = ptr(false)
				return monitor
			}(),
			expectError: "spec.endpoints[0].followRedirects not translated to GMP",
		},
		{
			name: "When the spec sets a field the generator does not translate, it should fail generation naming the field",
			assets: &componentAssets{
				services:       []*corev1.Service{baseService()},
				containerPorts: []corev1.ContainerPort{{Name: "metrics", ContainerPort: 10257}},
			},
			monitor: func() *prometheusServiceMonitor {
				monitor := baseMonitor()
				monitor.Spec.SampleLimit = ptr(uint64(1000))
				monitor.Spec.ScrapeClassName = ptr("default")
				return monitor
			}(),
			expectError: "spec.sampleLimit, spec.scrapeClass not translated to GMP",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tc.assets.dir = filepath.Join("assets", "kube-controller-manager")

			doc, err := tc.assets.convertServiceMonitor(tc.monitor)
			if tc.expectError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectError)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, doc)
			tc.validateDocument(t, doc)
		})
	}
}

func TestComponentAssets_ConvertPodMonitor(t *testing.T) {
	baseMonitor := func() *prometheusPodMonitor {
		return &prometheusPodMonitor{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster-autoscaler", Namespace: placeholderNamespace},
			Spec: prometheusoperatorv1.PodMonitorSpec{
				Selector:            metav1.LabelSelector{MatchLabels: map[string]string{"app": "cluster-autoscaler"}},
				PodMetricsEndpoints: []prometheusoperatorv1.PodMetricsEndpoint{{Port: strPtr("metrics")}},
			},
		}
	}

	testCases := []struct {
		name         string
		assets       *componentAssets
		monitor      *prometheusPodMonitor
		expectError  string
		expectedPort intstr.IntOrString
		// expectedRelabeling is the generated metricRelabeling; the default
		// port-named endpoint gets the endpoint label rule.
		expectedRelabeling []relabelingRuleDoc
	}{
		{
			name:         "When the endpoint names a container port, it should keep the port name",
			assets:       &componentAssets{containerPorts: []corev1.ContainerPort{{Name: "metrics", ContainerPort: 8085}}},
			monitor:      baseMonitor(),
			expectedPort: intstr.FromString("metrics"),
		},
		{
			name:   "When the endpoint uses portNumber, it should rewrite it to the container port name",
			assets: &componentAssets{containerPorts: []corev1.ContainerPort{{Name: "metrics", ContainerPort: 8085}}},
			monitor: func() *prometheusPodMonitor {
				monitor := baseMonitor()
				portNumber := int32(8085)
				monitor.Spec.PodMetricsEndpoints = []prometheusoperatorv1.PodMetricsEndpoint{{PortNumber: &portNumber}}
				return monitor
			}(),
			expectedPort: intstr.FromString("metrics"),
			// prometheus-operator sets no endpoint label for a portNumber endpoint.
			expectedRelabeling: []relabelingRuleDoc{},
		},
		{
			name:   "When the endpoint names an unknown container port, it should fail generation",
			assets: &componentAssets{containerPorts: []corev1.ContainerPort{{Name: "metrics", ContainerPort: 8085}}},
			monitor: func() *prometheusPodMonitor {
				monitor := baseMonitor()
				monitor.Spec.PodMetricsEndpoints = []prometheusoperatorv1.PodMetricsEndpoint{{Port: strPtr("unknown")}}
				return monitor
			}(),
			expectError: "no container port named \"unknown\"",
		},
		{
			name: "When jobLabel is absent from the pod template, it should stay inert",
			assets: &componentAssets{
				containerPorts: []corev1.ContainerPort{{Name: "metrics", ContainerPort: 8085}},
				podLabels:      []map[string]string{{"app": "cluster-autoscaler"}},
			},
			monitor: func() *prometheusPodMonitor {
				monitor := baseMonitor()
				monitor.Spec.JobLabel = "component"
				return monitor
			}(),
			expectedPort: intstr.FromString("metrics"),
		},
		{
			name: "When jobLabel selects a real pod label, it should fail because a PodMonitoring cannot reproduce it",
			assets: &componentAssets{
				containerPorts: []corev1.ContainerPort{{Name: "metrics", ContainerPort: 8085}},
				podLabels:      []map[string]string{{"app": "cluster-autoscaler"}},
			},
			monitor: func() *prometheusPodMonitor {
				monitor := baseMonitor()
				monitor.Spec.JobLabel = "app"
				return monitor
			}(),
			expectError: `jobLabel "app" selects pod label value "cluster-autoscaler"`,
		},
		{
			name:   "When the pod selector is empty, it should fail generation",
			assets: &componentAssets{containerPorts: []corev1.ContainerPort{{Name: "metrics", ContainerPort: 8085}}},
			monitor: func() *prometheusPodMonitor {
				monitor := baseMonitor()
				monitor.Spec.Selector = metav1.LabelSelector{}
				return monitor
			}(),
			expectError: "empty pod selector",
		},
		{
			name:   "When the monitor has no podMetricsEndpoints, it should fail generation",
			assets: &componentAssets{containerPorts: []corev1.ContainerPort{{Name: "metrics", ContainerPort: 8085}}},
			monitor: func() *prometheusPodMonitor {
				monitor := baseMonitor()
				monitor.Spec.PodMetricsEndpoints = nil
				return monitor
			}(),
			expectError: "no podMetricsEndpoints",
		},
		{
			name:   "When an endpoint sets a field the generator does not translate, it should fail generation naming the field",
			assets: &componentAssets{containerPorts: []corev1.ContainerPort{{Name: "metrics", ContainerPort: 8085}}},
			monitor: func() *prometheusPodMonitor {
				monitor := baseMonitor()
				monitor.Spec.PodMetricsEndpoints[0].TrackTimestampsStaleness = ptr(true)
				return monitor
			}(),
			expectError: "spec.podMetricsEndpoints[0].trackTimestampsStaleness not translated to GMP",
		},
		{
			name:   "When the spec sets a field the generator does not translate, it should fail generation naming the field",
			assets: &componentAssets{containerPorts: []corev1.ContainerPort{{Name: "metrics", ContainerPort: 8085}}},
			monitor: func() *prometheusPodMonitor {
				monitor := baseMonitor()
				monitor.Spec.TargetLimit = ptr(uint64(10))
				return monitor
			}(),
			expectError: "spec.targetLimit not translated to GMP",
		},
		{
			name:   "When an endpoint carries an empty bearerTokenSecret placeholder, it should treat it as unset",
			assets: &componentAssets{containerPorts: []corev1.ContainerPort{{Name: "metrics", ContainerPort: 8085}}},
			monitor: func() *prometheusPodMonitor {
				monitor := baseMonitor()
				monitor.Spec.PodMetricsEndpoints[0].BearerTokenSecret = &corev1.SecretKeySelector{} //nolint:staticcheck // SA1019: exercises rejection of the legacy field
				return monitor
			}(),
			expectedPort: intstr.FromString("metrics"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tc.assets.dir = filepath.Join("assets", "cluster-autoscaler")

			doc, err := tc.assets.convertPodMonitor(tc.monitor)
			if tc.expectError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectError)
				return
			}

			require.NoError(t, err)
			require.Len(t, doc.Spec.Endpoints, 1)
			assert.Equal(t, portRef{Value: tc.expectedPort}, doc.Spec.Endpoints[0].Port)
			// PodMonitor selectors are already pod selectors, so they pass through unchanged.
			assert.Equal(t, map[string]string{"app": "cluster-autoscaler"}, doc.Spec.Selector.MatchLabels)
			assert.Equal(t, "cluster-autoscaler", doc.Metadata.Name)
			assert.Equal(t, &targetLabelsDoc{Metadata: []string{"container", "pod"}}, doc.Spec.TargetLabels)
			expectedRelabeling := tc.expectedRelabeling
			if expectedRelabeling == nil {
				expectedRelabeling = []relabelingRuleDoc{{TargetLabel: "endpoint", Replacement: "metrics", Action: "replace"}}
			}
			assert.ElementsMatch(t, expectedRelabeling, doc.Spec.Endpoints[0].MetricRelabeling)
		})
	}
}

func TestManagementMonitors(t *testing.T) {
	monitors := managementMonitors()
	require.Len(t, monitors, 2)

	testCases := []struct {
		component  string
		source     string
		selector   map[string]string
		relabeling []map[string]string
	}{
		{
			component: "operator",
			source:    "cmd/install/assets/hypershift_operator.go HyperShiftServiceMonitor",
			selector:  map[string]string{"name": "operator"},
			relabeling: []map[string]string{
				{"targetLabel": "service", "replacement": "operator", "action": "replace"},
				{"targetLabel": "endpoint", "replacement": "metrics", "action": "replace"},
			},
		},
		{
			component: "external-dns",
			source:    "cmd/install/assets/hypershift_operator.go ExternalDNSPodMonitor",
			selector:  map[string]string{"name": "external-dns"},
			relabeling: []map[string]string{
				{"targetLabel": "endpoint", "replacement": "metrics", "action": "replace"},
			},
		},
	}

	for i, tc := range testCases {
		t.Run(tc.component, func(t *testing.T) {
			monitor := monitors[i]
			assert.Equal(t, tc.component, monitor.component)

			data, err := generateManagement(monitor)
			require.NoError(t, err)

			output := string(data)
			assert.Contains(t, output, generatedHeader)
			assert.Contains(t, output, "# Source: "+tc.source)
			// The install namespace is configurable, so the staged manifest must
			// not embed one cluster's value.
			assert.Contains(t, output, "namespace: "+managementNamespacePlaceholder)
			assert.NotContains(t, output, "namespace: hypershift")

			// portRef marshals through yaml.v3, so decode the emitted document
			// with a purpose-built struct instead of round-tripping the doc type.
			var parsed struct {
				APIVersion string `json:"apiVersion"`
				Kind       string `json:"kind"`
				Metadata   struct {
					Name      string `json:"name"`
					Namespace string `json:"namespace"`
				} `json:"metadata"`
				Spec struct {
					Selector struct {
						MatchLabels map[string]string `json:"matchLabels"`
					} `json:"selector"`
					TargetLabels struct {
						Metadata []string `json:"metadata"`
					} `json:"targetLabels"`
					Endpoints []struct {
						Port             string              `json:"port"`
						Path             string              `json:"path"`
						Scheme           string              `json:"scheme"`
						Interval         string              `json:"interval"`
						MetricRelabeling []map[string]string `json:"metricRelabeling"`
					} `json:"endpoints"`
				} `json:"spec"`
			}
			require.NoError(t, yaml.UnmarshalStrict(data, &parsed))
			assert.Equal(t, gmpAPIVersion, parsed.APIVersion)
			assert.Equal(t, "PodMonitoring", parsed.Kind)
			assert.Equal(t, tc.component, parsed.Metadata.Name)
			assert.Equal(t, managementNamespacePlaceholder, parsed.Metadata.Namespace)
			assert.Equal(t, tc.selector, parsed.Spec.Selector.MatchLabels)
			require.Len(t, parsed.Spec.Endpoints, 1)
			assert.Equal(t, "metrics", parsed.Spec.Endpoints[0].Port)
			assert.Equal(t, "30s", parsed.Spec.Endpoints[0].Interval)
			assert.Equal(t, []string{"container", "pod"}, parsed.Spec.TargetLabels.Metadata)
			assert.Equal(t, tc.relabeling, parsed.Spec.Endpoints[0].MetricRelabeling)
		})
	}
}

func TestConvertTLS(t *testing.T) {
	minVersion := prometheusoperatorv1.TLSVersion12

	testCases := []struct {
		name        string
		config      *prometheusoperatorv1.SafeTLSConfig
		expectError string
		validate    func(t *testing.T, tls *tlsDoc)
	}{
		{
			name: "When the CA is a Secret, it should pass the Secret reference through",
			config: &prometheusoperatorv1.SafeTLSConfig{
				CA: prometheusoperatorv1.SecretOrConfigMap{
					Secret: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "etcd-metrics-client-tls"},
						Key:                  "ca.crt",
					},
				},
			},
			validate: func(t *testing.T, tls *tlsDoc) {
				assert.Equal(t, &secretRefDoc{Secret: secretKeyRefDoc{Name: "etcd-metrics-client-tls", Key: "ca.crt"}}, tls.CA)
			},
		},
		{
			name: "When the CA is the root-ca ConfigMap, it should point at the root-ca-cert mirror Secret",
			config: &prometheusoperatorv1.SafeTLSConfig{
				CA: prometheusoperatorv1.SecretOrConfigMap{
					ConfigMap: &corev1.ConfigMapKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "root-ca"},
						Key:                  "ca.crt",
					},
				},
			},
			validate: func(t *testing.T, tls *tlsDoc) {
				assert.Equal(t, &secretRefDoc{Secret: secretKeyRefDoc{Name: "root-ca-cert", Key: "ca.crt"}}, tls.CA)
			},
		},
		{
			name: "When the CA is the etcd-ca ConfigMap, it should point at the etcd-ca-cert mirror Secret",
			config: &prometheusoperatorv1.SafeTLSConfig{
				CA: prometheusoperatorv1.SecretOrConfigMap{
					ConfigMap: &corev1.ConfigMapKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "etcd-ca"},
						Key:                  "ca.crt",
					},
				},
			},
			validate: func(t *testing.T, tls *tlsDoc) {
				assert.Equal(t, &secretRefDoc{Secret: secretKeyRefDoc{Name: "etcd-ca-cert", Key: "ca.crt"}}, tls.CA)
			},
		},
		{
			name: "When the CA is a signer Secret, it should fail generation",
			config: &prometheusoperatorv1.SafeTLSConfig{
				CA: prometheusoperatorv1.SecretOrConfigMap{
					Secret: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "root-ca"},
						Key:                  "ca.crt",
					},
				},
			},
			expectError: `references Secret "root-ca", which holds a CA private key`,
		},
		{
			name: "When the CA ConfigMap has no Secret counterpart, it should fail generation",
			config: &prometheusoperatorv1.SafeTLSConfig{
				CA: prometheusoperatorv1.SecretOrConfigMap{
					ConfigMap: &corev1.ConfigMapKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "cluster-version-operator-metrics-client-ca"},
						Key:                  "ca.crt",
					},
				},
			},
			expectError: `TLS CA ConfigMap "cluster-version-operator-metrics-client-ca" has no Secret counterpart`,
		},
		{
			name: "When the TLS config sets versions and server name, it should carry them into the PodMonitoring",
			config: &prometheusoperatorv1.SafeTLSConfig{
				ServerName: strPtr("etcd-client"),
				MinVersion: &minVersion,
			},
			validate: func(t *testing.T, tls *tlsDoc) {
				assert.Equal(t, "etcd-client", tls.ServerName)
				assert.Equal(t, string(minVersion), tls.MinVersion)
			},
		},
		{
			name: "When the server name targets a Service, it should use the HCP_NAMESPACE placeholder",
			config: &prometheusoperatorv1.SafeTLSConfig{
				ServerName: strPtr("node-tuning-operator.clusters-mraee-karpenter.svc"),
			},
			validate: func(t *testing.T, tls *tlsDoc) {
				assert.Equal(t, "node-tuning-operator.HCP_NAMESPACE.svc", tls.ServerName)
			},
		},
		{
			name: "When the server name already uses the placeholder, it should be left unchanged",
			config: &prometheusoperatorv1.SafeTLSConfig{
				ServerName: strPtr("node-tuning-operator.HCP_NAMESPACE.svc"),
			},
			validate: func(t *testing.T, tls *tlsDoc) {
				assert.Equal(t, "node-tuning-operator.HCP_NAMESPACE.svc", tls.ServerName)
			},
		},
		{
			name: "When a client certificate has no key, it should fail generation",
			config: &prometheusoperatorv1.SafeTLSConfig{
				Cert: prometheusoperatorv1.SecretOrConfigMap{
					Secret: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "etcd-metrics-client-tls"},
						Key:                  "etcd-client.crt",
					},
				},
			},
			expectError: "client certificate and key to be provided together",
		},
		{
			name: "When the client certificate comes from a ConfigMap, it should fail generation",
			config: &prometheusoperatorv1.SafeTLSConfig{
				Cert: prometheusoperatorv1.SecretOrConfigMap{
					ConfigMap: &corev1.ConfigMapKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "etcd-metrics-client-tls"},
						Key:                  "etcd-client.crt",
					},
				},
			},
			expectError: "GMP requires a Secret",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tls, err := convertTLS(tc.config)
			if tc.expectError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectError)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, tls)
			tc.validate(t, tls)
		})
	}

	t.Run("When no TLS config is set, it should not emit a TLS section", func(t *testing.T) {
		tls, err := convertTLS(nil)
		require.NoError(t, err)
		assert.Nil(t, tls)
	})
}

func TestComponentAssets_ContainerPortRef(t *testing.T) {
	testCases := []struct {
		name           string
		containerPorts []corev1.ContainerPort
		ref            intstr.IntOrString
		expected       intstr.IntOrString
		expectError    string
	}{
		{
			name:     "When the workload has no asset, it should keep the reference unchanged",
			ref:      intstr.FromInt(8443),
			expected: intstr.FromInt(8443),
		},
		{
			name:           "When a numeric reference matches a named container port, it should rewrite the reference to the name",
			containerPorts: []corev1.ContainerPort{{Name: "metrics", ContainerPort: 8443}},
			ref:            intstr.FromInt(8443),
			expected:       intstr.FromString("metrics"),
		},
		{
			name:           "When a numeric reference matches an unnamed container port, it should keep the number",
			containerPorts: []corev1.ContainerPort{{ContainerPort: 8443}},
			ref:            intstr.FromInt(8443),
			expected:       intstr.FromInt(8443),
		},
		{
			name:           "When a numeric reference matches no container port, it should fail generation",
			containerPorts: []corev1.ContainerPort{{Name: "metrics", ContainerPort: 8443}},
			ref:            intstr.FromInt(9999),
			expectError:    "no container port 9999",
		},
		{
			name:           "When a named reference matches no container port, it should fail generation",
			containerPorts: []corev1.ContainerPort{{Name: "metrics", ContainerPort: 8443}},
			ref:            intstr.FromString("unknown"),
			expectError:    "no container port named \"unknown\"",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assets := &componentAssets{containerPorts: tc.containerPorts}

			actual, err := assets.containerPortRef(tc.ref)
			if tc.expectError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectError)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.expected, actual)
		})
	}
}

func TestConvertMetricRelabeling(t *testing.T) {
	t.Run("When static relabeling uses supported fields, it should preserve their meaning", func(t *testing.T) {
		separator := ","
		replacement := "cluster-$1"
		actual, err := convertMetricRelabeling([]prometheusoperatorv1.RelabelConfig{{
			SourceLabels: []prometheusoperatorv1.LabelName{"__name__", "job"},
			Separator:    &separator,
			TargetLabel:  "_id",
			Regex:        "cluster-(.*)",
			Replacement:  &replacement,
			Action:       "Replace",
		}})
		require.NoError(t, err)
		assert.Equal(t, []relabelingRuleDoc{{
			SourceLabels: []string{"__name__", "job"}, Separator: ",", TargetLabel: "_id",
			Regex: "cluster-(.*)", Replacement: "cluster-$1", Action: "replace",
		}}, actual)
	})
	t.Run("When CoreOS drops every metric, it should emit the GMP-safe keep-nothing filter", func(t *testing.T) {
		actual, err := convertMetricRelabeling([]prometheusoperatorv1.RelabelConfig{{
			SourceLabels: []prometheusoperatorv1.LabelName{"__name__"},
			Regex:        "(.*)",
			Action:       "drop",
		}})
		require.NoError(t, err)
		assert.Equal(t, []relabelingRuleDoc{{
			SourceLabels: []string{"__name__"}, Regex: "^$", Action: "keep",
		}}, actual)
	})
	t.Run("When CoreOS uses labelmap, it should reject unsupported GMP action", func(t *testing.T) {
		_, err := convertMetricRelabeling([]prometheusoperatorv1.RelabelConfig{{Action: "labelmap"}})
		require.ErrorContains(t, err, `action "labelmap" is not supported by GMP`)
	})
	t.Run("When CoreOS uses an action GMP does not define, it should reject it", func(t *testing.T) {
		for _, action := range []string{"lowercase", "uppercase", "keepequal", "dropequal"} {
			t.Run(action, func(t *testing.T) {
				_, err := convertMetricRelabeling([]prometheusoperatorv1.RelabelConfig{{Action: action, Regex: "up"}})
				require.ErrorContains(t, err, fmt.Sprintf("action %q is not supported by GMP", action))
			})
		}
	})
	t.Run("When CoreOS relabels onto a label GMP protects, it should reject the rule", func(t *testing.T) {
		_, err := convertMetricRelabeling([]prometheusoperatorv1.RelabelConfig{{
			TargetLabel: "namespace",
			Replacement: ptr("openshift-monitoring"),
		}})
		require.ErrorContains(t, err, `target label "namespace" is protected by GMP`)
	})
	t.Run("When CoreOS explicitly uses an empty separator, it should reject the semantic mismatch", func(t *testing.T) {
		empty := ""
		_, err := convertMetricRelabeling([]prometheusoperatorv1.RelabelConfig{{Separator: &empty}})
		require.ErrorContains(t, err, "empty separator cannot be preserved")
	})
	t.Run("When CoreOS uses labeldrop without a regex, it should reject the rule the GMP API would deny", func(t *testing.T) {
		_, err := convertMetricRelabeling([]prometheusoperatorv1.RelabelConfig{{Action: "LabelDrop"}})
		require.ErrorContains(t, err, "labeldrop must set a regex")
	})
}

func TestComponentAssets_EnsureUniqueEndpointPorts(t *testing.T) {
	service := func() *corev1.Service {
		return &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "kube-scheduler", Labels: map[string]string{"app": "kube-scheduler"}},
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{"app": "kube-scheduler"},
				Ports:    []corev1.ServicePort{{Name: "client", Port: 10259, TargetPort: intstr.FromString("client")}},
			},
		}
	}
	monitor := func() *prometheusServiceMonitor {
		return &prometheusServiceMonitor{
			ObjectMeta: metav1.ObjectMeta{Name: "kube-scheduler", Namespace: placeholderNamespace},
			Spec: prometheusoperatorv1.ServiceMonitorSpec{
				Selector: metav1.LabelSelector{MatchLabels: map[string]string{"app": "kube-scheduler"}},
				Endpoints: []prometheusoperatorv1.Endpoint{
					{Port: "client"},
					{Port: "client", Path: "/metrics/resources"},
				},
			},
		}
	}

	podSpec := corev1.PodSpec{Containers: []corev1.Container{{
		Name:  "kube-scheduler",
		Ports: []corev1.ContainerPort{{Name: "client", ContainerPort: 10259}},
	}}}

	t.Run("When two endpoints scrape the same named port, it should declare the second by its container port number", func(t *testing.T) {
		assets := &componentAssets{services: []*corev1.Service{service()}}
		assets.addContainerPorts(podSpec)
		doc, err := assets.convertServiceMonitor(monitor())
		require.NoError(t, err)
		require.Len(t, doc.Spec.Endpoints, 2)
		assert.Equal(t, portRef{Value: intstr.FromString("client")}, doc.Spec.Endpoints[0].Port)
		assert.Equal(t, portRef{Value: intstr.FromInt(10259)}, doc.Spec.Endpoints[1].Port)
		// Both endpoints keep scraping the same container port and report the
		// same endpoint label prometheus-operator derives from the targetPort.
		assert.Equal(t, defaultMetricsPath, doc.Spec.Endpoints[0].Path)
		assert.Equal(t, "/metrics/resources", doc.Spec.Endpoints[1].Path)
		for _, endpoint := range doc.Spec.Endpoints {
			assert.Contains(t, endpoint.MetricRelabeling, relabelingRuleDoc{TargetLabel: "endpoint", Replacement: "client", Action: "replace"})
		}
	})

	t.Run("When an endpoint is declared by number, it should restore the container label GMP omits", func(t *testing.T) {
		assets := &componentAssets{services: []*corev1.Service{service()}}
		assets.addContainerPorts(podSpec)
		doc, err := assets.convertServiceMonitor(monitor())
		require.NoError(t, err)
		require.Len(t, doc.Spec.Endpoints, 2)
		containerRule := relabelingRuleDoc{TargetLabel: "container", Replacement: "kube-scheduler", Action: "replace"}
		// The named endpoint gets container from GMP's pod metadata.
		assert.NotContains(t, doc.Spec.Endpoints[0].MetricRelabeling, containerRule)
		assert.Equal(t, []relabelingRuleDoc{
			{TargetLabel: "service", Replacement: "kube-scheduler", Action: "replace"},
			{TargetLabel: "endpoint", Replacement: "client", Action: "replace"},
			containerRule,
		}, doc.Spec.Endpoints[1].MetricRelabeling)
	})

	t.Run("When no single container declares the repeated port, it should return an error", func(t *testing.T) {
		assets := &componentAssets{services: []*corev1.Service{service()}}
		assets.addContainerPorts(corev1.PodSpec{Containers: []corev1.Container{
			{Name: "kube-scheduler", Ports: []corev1.ContainerPort{{Name: "client", ContainerPort: 10259}}},
			{Name: "sidecar", Ports: []corev1.ContainerPort{{Name: "client", ContainerPort: 10259}}},
		}})
		_, err := assets.convertServiceMonitor(monitor())
		require.ErrorContains(t, err, "no single container declares it")
	})

	t.Run("When the workload declares no numeric port for the repeated name, it should return an error", func(t *testing.T) {
		assets := &componentAssets{services: []*corev1.Service{service()}}
		_, err := assets.convertServiceMonitor(monitor())
		require.ErrorContains(t, err, "GMP requires unique ports")
	})
}

func TestComponentAssets_AddContainerPorts(t *testing.T) {
	testCases := []struct {
		name               string
		specs              []corev1.PodSpec
		wantPortContainers map[string]string
		wantPorts          int
	}{
		{
			name: "When each named port has one container, it should map the port to that container",
			specs: []corev1.PodSpec{{Containers: []corev1.Container{
				{Name: "app", Ports: []corev1.ContainerPort{{Name: "metrics", ContainerPort: 8443}, {ContainerPort: 9000}}},
				{Name: "proxy", Ports: []corev1.ContainerPort{{Name: "https", ContainerPort: 443}}},
			}}},
			wantPortContainers: map[string]string{"metrics": "app", "https": "proxy"},
			wantPorts:          3,
		},
		{
			name: "When two workloads declare the same port name on different containers, it should mark the name ambiguous",
			specs: []corev1.PodSpec{
				{Containers: []corev1.Container{{Name: "app", Ports: []corev1.ContainerPort{{Name: "metrics", ContainerPort: 8443}}}}},
				{Containers: []corev1.Container{{Name: "other", Ports: []corev1.ContainerPort{{Name: "metrics", ContainerPort: 8443}}}}},
			},
			wantPortContainers: map[string]string{"metrics": ""},
			wantPorts:          2,
		},
		{
			name: "When two workloads declare the same port name on the same container name, it should keep the container",
			specs: []corev1.PodSpec{
				{Containers: []corev1.Container{{Name: "etcd", Ports: []corev1.ContainerPort{{Name: "metrics", ContainerPort: 2381}}}}},
				{Containers: []corev1.Container{{Name: "etcd", Ports: []corev1.ContainerPort{{Name: "metrics", ContainerPort: 2381}}}}},
			},
			wantPortContainers: map[string]string{"metrics": "etcd"},
			wantPorts:          2,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assets := &componentAssets{}
			for _, spec := range tc.specs {
				assets.addContainerPorts(spec)
			}
			assert.Equal(t, tc.wantPortContainers, assets.portContainers)
			assert.Len(t, assets.containerPorts, tc.wantPorts)
		})
	}
}

func TestClusterIndependentServerName(t *testing.T) {
	testCases := []struct {
		name       string
		serverName string
		expected   string
	}{
		{
			name:       "When the server name is a namespaced Service DNS name, it should replace the namespace",
			serverName: "node-tuning-operator.clusters-example.svc",
			expected:   "node-tuning-operator.HCP_NAMESPACE.svc",
		},
		{
			name:       "When the server name is a fully qualified Service DNS name, it should replace only the namespace",
			serverName: "etcd-client.clusters-example.svc.cluster.local",
			expected:   "etcd-client.HCP_NAMESPACE.svc.cluster.local",
		},
		{
			name:       "When the server name has no namespace label before svc, it should leave the service name alone",
			serverName: "kube-apiserver.svc",
			expected:   "kube-apiserver.svc",
		},
		{
			name:       "When the server name is not a Service DNS name, it should leave it unchanged",
			serverName: "etcd-client",
			expected:   "etcd-client",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, clusterIndependentServerName(tc.serverName))
		})
	}
}

func TestSourceMonitorFile(t *testing.T) {
	t.Run("When a component has a ServiceMonitor, it should select it as the source", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, sourceServiceMonitor), []byte("kind: ServiceMonitor"), 0o644))

		actual, err := sourceMonitorFile(dir)
		require.NoError(t, err)
		assert.Equal(t, sourceServiceMonitor, actual)
	})

	t.Run("When a component has a PodMonitor, it should select it as the source", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, sourcePodMonitor), []byte("kind: PodMonitor"), 0o644))

		actual, err := sourceMonitorFile(dir)
		require.NoError(t, err)
		assert.Equal(t, sourcePodMonitor, actual)
	})

	t.Run("When a component has no monitor, it should return an empty source", func(t *testing.T) {
		actual, err := sourceMonitorFile(t.TempDir())
		require.NoError(t, err)
		assert.Empty(t, actual)
	})

	t.Run("When a component has both monitor types, it should fail generation", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, sourceServiceMonitor), []byte("kind: ServiceMonitor"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, sourcePodMonitor), []byte("kind: PodMonitor"), 0o644))

		_, err := sourceMonitorFile(dir)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "may only declare one monitor")
	})
}

func TestGenerateAll(t *testing.T) {
	writeComponent := func(t *testing.T, assetsDir, name string) string {
		t.Helper()
		componentDir := filepath.Join(assetsDir, name)
		require.NoError(t, os.MkdirAll(componentDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(componentDir, sourceServiceMonitor), []byte(serviceMonitorFixture("")), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(componentDir, "service.yaml"), []byte(serviceFixture()), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(componentDir, "deployment.yaml"), []byte(deploymentFixture()), 0o644))
		return componentDir
	}
	writePodMonitoring := func(t *testing.T, dir string, generated bool) string {
		t.Helper()
		require.NoError(t, os.MkdirAll(dir, 0o755))
		path := filepath.Join(dir, generatedPodMonitoring)
		content := "apiVersion: monitoring.googleapis.com/v1\n"
		if generated {
			content = generatedHeader + content
		}
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
		return path
	}

	t.Run("When components have CoreOS monitors, it should generate them outside active assets and flag removed components", func(t *testing.T) {
		root := t.TempDir()
		assetsDir := filepath.Join(root, "assets")
		generatedDir := filepath.Join(root, "gmpassets")
		require.NoError(t, os.MkdirAll(assetsDir, 0o755))
		writeComponent(t, assetsDir, "test-component")
		stalePath := writePodMonitoring(t, filepath.Join(generatedDir, "stale-component"), true)
		writePodMonitoring(t, filepath.Join(generatedDir, "hand-written-component"), false)

		result, err := generateAll(assetsDir, generatedDir, nil, nil)
		require.NoError(t, err)
		generated, ok := result.generated[filepath.Join(generatedDir, "test-component", generatedPodMonitoring)]
		require.True(t, ok)
		assert.Contains(t, string(generated), generatedHeader)
		assert.Contains(t, string(generated), "kind: PodMonitoring")
		assert.Equal(t, []string{stalePath}, result.stale)
		assert.False(t, fileExists(filepath.Join(assetsDir, "test-component", generatedPodMonitoring)))
	})

	t.Run("When a component is excluded, it should not generate it and should flag its generated file as stale", func(t *testing.T) {
		root := t.TempDir()
		assetsDir := filepath.Join(root, "assets")
		generatedDir := filepath.Join(root, "gmpassets")
		require.NoError(t, os.MkdirAll(assetsDir, 0o755))
		writeComponent(t, assetsDir, "platform-specific")
		stalePath := writePodMonitoring(t, filepath.Join(generatedDir, "platform-specific"), true)

		result, err := generateAll(assetsDir, generatedDir, map[string]string{"platform-specific": "test"}, nil)
		require.NoError(t, err)
		assert.Empty(t, result.generated)
		assert.Equal(t, []string{stalePath}, result.stale)
	})

	t.Run("When an excluded component has no directory, it should fail generation", func(t *testing.T) {
		root := t.TempDir()
		assetsDir := filepath.Join(root, "assets")
		require.NoError(t, os.MkdirAll(assetsDir, 0o755))
		writeComponent(t, assetsDir, "test-component")
		_, err := generateAll(assetsDir, filepath.Join(root, "gmpassets"), map[string]string{"removed-component": "test"}, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), `excluded component "removed-component" has no directory`)
	})

	t.Run("When an excluded component has no CoreOS monitor, it should fail generation", func(t *testing.T) {
		root := t.TempDir()
		assetsDir := filepath.Join(root, "assets")
		require.NoError(t, os.MkdirAll(filepath.Join(assetsDir, "no-monitor"), 0o755))
		_, err := generateAll(assetsDir, filepath.Join(root, "gmpassets"), map[string]string{"no-monitor": "test"}, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "remove it from excludedComponents")
	})

	t.Run("When a component with a CoreOS monitor has a hand-written PodMonitoring, it should fail instead of overwriting it", func(t *testing.T) {
		root := t.TempDir()
		assetsDir := filepath.Join(root, "assets")
		generatedDir := filepath.Join(root, "gmpassets")
		require.NoError(t, os.MkdirAll(assetsDir, 0o755))
		writeComponent(t, assetsDir, "test-component")
		writePodMonitoring(t, filepath.Join(generatedDir, "test-component"), false)
		_, err := generateAll(assetsDir, generatedDir, nil, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "is hand-written")
	})

	t.Run("When output equals the active asset directory, it should fail", func(t *testing.T) {
		root := t.TempDir()
		_, err := generateAll(root, root, nil, nil)
		require.ErrorContains(t, err, "outside the active CPO asset directory")
	})

	t.Run("When management monitors are passed, it should generate them into the output tree", func(t *testing.T) {
		root := t.TempDir()
		assetsDir := filepath.Join(root, "assets")
		generatedDir := filepath.Join(root, "gmpassets")
		require.NoError(t, os.MkdirAll(assetsDir, 0o755))

		result, err := generateAll(assetsDir, generatedDir, nil, managementMonitors())
		require.NoError(t, err)

		for _, component := range []string{"operator", "external-dns"} {
			generated, ok := result.generated[filepath.Join(generatedDir, component, generatedPodMonitoring)]
			require.True(t, ok)
			assert.Contains(t, string(generated), generatedHeader)
			assert.Contains(t, string(generated), "namespace: "+managementNamespacePlaceholder)
		}
		assert.Empty(t, result.stale)
	})

	t.Run("When a management monitor collides with an asset component, it should fail", func(t *testing.T) {
		root := t.TempDir()
		assetsDir := filepath.Join(root, "assets")
		require.NoError(t, os.MkdirAll(assetsDir, 0o755))
		writeComponent(t, assetsDir, "operator")

		_, err := generateAll(assetsDir, filepath.Join(root, "gmpassets"), nil, managementMonitors())
		require.Error(t, err)
		assert.Contains(t, err.Error(), `management monitor "operator" collides with an asset component`)
	})

	t.Run("When a management monitor output is hand-written, it should fail instead of overwriting it", func(t *testing.T) {
		root := t.TempDir()
		assetsDir := filepath.Join(root, "assets")
		generatedDir := filepath.Join(root, "gmpassets")
		require.NoError(t, os.MkdirAll(assetsDir, 0o755))
		writePodMonitoring(t, filepath.Join(generatedDir, "operator"), false)

		_, err := generateAll(assetsDir, generatedDir, nil, managementMonitors())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "is hand-written")
	})
}

func TestGenerateComponent(t *testing.T) {
	t.Run("When the source assets use template expressions, it should preserve them in the output", func(t *testing.T) {
		componentDir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(componentDir, sourceServiceMonitor), []byte(`apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: {{ .Name }}
  namespace: HCP_NAMESPACE
spec:
  selector:
    matchLabels:
      app: {{ .Name }}
  endpoints:
    - port: metrics
`), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(componentDir, "service.yaml"), []byte(`apiVersion: v1
kind: Service
metadata:
  name: {{ .Name }}
  labels:
    app: {{ .Name }}
spec:
  selector:
    app: {{ .Name }}
  ports:
    - name: metrics
      port: 2381
      targetPort: 2381
`), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(componentDir, "deployment.yaml"), []byte(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ .Name }}
spec:
  template:
    spec:
      containers:
        - name: etcd
          ports:
            - name: metrics
              containerPort: 2381
`), 0o644))

		generated, err := generateComponent(componentDir, sourceServiceMonitor)
		require.NoError(t, err)

		assert.Contains(t, string(generated), "name: {{ .Name }}")
		assert.Contains(t, string(generated), "app: {{ .Name }}")
		assert.Contains(t, string(generated), "port: metrics")
		assert.NotContains(t, string(generated), "zzgmpgentemplate")
	})

	t.Run("When the source uses a field the generator cannot translate, it should fail with the file name", func(t *testing.T) {
		componentDir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(componentDir, sourceServiceMonitor), []byte(serviceMonitorFixture("  jobLabel: app\n")), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(componentDir, "service.yaml"), []byte(serviceFixture()), 0o644))

		_, err := generateComponent(componentDir, sourceServiceMonitor)
		require.Error(t, err)
		assert.Contains(t, err.Error(), sourceServiceMonitor)
		assert.Contains(t, err.Error(), `jobLabel "app" selects Service label value "test-component"`)
	})

	t.Run("When the ServiceMonitor is not valid YAML, it should fail generation", func(t *testing.T) {
		componentDir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(componentDir, sourceServiceMonitor), []byte("kind: [ServiceMonitor"), 0o644))

		_, err := generateComponent(componentDir, sourceServiceMonitor)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to parse")
	})
}

//nolint:hypershiftlinter // testfuncstructure: the round-trip cannot be attributed to protect or restore alone
func TestTemplatePlaceholders(t *testing.T) {
	t.Run("When an expression repeats, it should reuse one placeholder and restore the original text", func(t *testing.T) {
		placeholders := newTemplatePlaceholders()

		raw := []byte("name: {{ .Name }}\nnamespace: {{ .Namespace }}\nrepeat: {{ .Name }}\n")
		protected := placeholders.protect(raw)

		assert.NotContains(t, string(protected), "{{")
		assert.Equal(t, 2, strings.Count(string(protected), "zzgmpgentemplate0zz"))
		assert.Equal(t, 0, strings.Count(string(protected), "zzgmpgentemplate2zz"))
		assert.Equal(t, string(raw), string(placeholders.restore(protected)))
	})

	t.Run("When there are more than ten expressions, it should restore every expression exactly", func(t *testing.T) {
		var raw strings.Builder
		for i := range 12 {
			fmt.Fprintf(&raw, "field%d: {{ .Value%d }}\n", i, i)
		}

		// Restore repeatedly: a prefix collision would corrupt the output for
		// some map iteration orders.
		for range 20 {
			placeholders := newTemplatePlaceholders()
			assert.Equal(t, raw.String(), string(placeholders.restore(placeholders.protect([]byte(raw.String())))))
		}
	})
}

// TestGeneratedTLSSecretsHaveProducers fails when a staged PodMonitoring points
// at a Secret nothing creates, or at a Secret that holds a CA private key. CA
// references must name a CA-only mirror declared in caConfigMapSecrets, which
// the control-plane-operator creates when GMP monitoring is enabled; client
// cert and key references must name a Secret the control-plane-operator already
// reconciles. A made-up name (for example "<component>-metrics-client-ca")
// would produce a manifest that validates but can never hand the GMP collector
// a CA.
//
//nolint:hypershiftlinter // testfuncstructure: the guard inspects the committed generated tree, which no single production function owns
func TestGeneratedTLSSecretsHaveProducers(t *testing.T) {
	repoRoot, err := resolveRepoRoot("")
	require.NoError(t, err)
	generatedDir := filepath.Join(repoRoot, generatedRelPath)

	// CA mirrors carry only public CA bytes and are created by the
	// control-plane-operator when GMP monitoring is enabled. The names must come
	// from the CPO constructors so the generated reference cannot drift into a
	// name nothing reconciles, and every mapped ConfigMap must have a mirror.
	caMirrors := map[string]struct{}{
		manifests.RootCACertSecret(placeholderNamespace).Name: {},
		manifests.EtcdCACertSecret(placeholderNamespace).Name: {},
	}
	require.Equal(t, manifests.RootCACertSecret(placeholderNamespace).Name, caConfigMapSecrets["root-ca"])
	require.Equal(t, manifests.EtcdCACertSecret(placeholderNamespace).Name, caConfigMapSecrets["etcd-ca"])
	for configMap, secretName := range caConfigMapSecrets {
		require.Contains(t, caMirrors, secretName, "CA ConfigMap %q maps to Secret %q, which the control-plane-operator does not reconcile", configMap, secretName)
	}
	// Client certificates are reconciled by the control-plane-operator into the
	// HCP namespace.
	clientCerts := map[string]struct{}{
		manifests.MetricsClientCertSecret(placeholderNamespace).Name: {},
		manifests.EtcdMetricsClientSecret(placeholderNamespace).Name: {},
	}
	// Signer Secrets hold CA private keys; the collector must never be granted
	// read access to them.
	signers := map[string]struct{}{
		manifests.RootCASecret(placeholderNamespace).Name:     {},
		manifests.EtcdSignerSecret(placeholderNamespace).Name: {},
	}

	referenced := 0
	walkErr := filepath.WalkDir(generatedDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || entry.Name() != generatedPodMonitoring {
			return nil
		}

		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		// Templated components keep their {{ }} expressions in the staged file;
		// protect them so the document parses. The TLS Secret names this test
		// checks are never templated.
		var doc podMonitoringDoc
		if err := yamlv3.Unmarshal(newTemplatePlaceholders().protect(raw), &doc); err != nil {
			return fmt.Errorf("failed to parse %s: %w", path, err)
		}
		for _, endpoint := range doc.Spec.Endpoints {
			if endpoint.TLS == nil {
				continue
			}
			checks := []struct {
				field     string
				reference *secretRefDoc
				allowed   map[string]struct{}
			}{
				{field: "ca", reference: endpoint.TLS.CA, allowed: caMirrors},
				{field: "cert", reference: endpoint.TLS.Cert, allowed: clientCerts},
				{field: "key", reference: endpoint.TLS.Key, allowed: clientCerts},
			}
			for _, check := range checks {
				if check.reference == nil {
					continue
				}
				referenced++
				name := check.reference.Secret.Name
				if _, ok := signers[name]; ok {
					t.Errorf("%s: TLS %s references signer Secret %q, which holds a CA private key", displayPath(path), check.field, name)
					continue
				}
				if _, ok := check.allowed[name]; !ok {
					t.Errorf("%s: TLS %s references Secret %q, which is neither a CA mirror in caConfigMapSecrets nor a control-plane-operator client certificate", displayPath(path), check.field, name)
				}
			}
		}
		return nil
	})
	require.NoError(t, walkErr)
	require.NotZero(t, referenced, "no generated PodMonitoring TLS reference was checked; the test would pass vacuously")
}

func serviceMonitorFixture(extraSpec string) string {
	return `apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: test-component
  namespace: HCP_NAMESPACE
  annotations:
    hypershift.openshift.io/metrics-endpoint: test-component
spec:
` + extraSpec + `  selector:
    matchLabels:
      app: test-component
  endpoints:
    - port: metrics
`
}

func serviceFixture() string {
	return `apiVersion: v1
kind: Service
metadata:
  name: test-component
  labels:
    app: test-component
spec:
  selector:
    app: test-component
    hypershift.openshift.io/control-plane-component: test-component
  ports:
    - name: metrics
      port: 2381
      targetPort: 2381
`
}

func deploymentFixture() string {
	return `apiVersion: apps/v1
kind: Deployment
metadata:
  name: test-component
spec:
  template:
    spec:
      containers:
        - name: test-component
          ports:
            - name: metrics
              containerPort: 2381
`
}

func ptr[T any](value T) *T {
	return &value
}

func strPtr(value string) *string {
	return &value
}
