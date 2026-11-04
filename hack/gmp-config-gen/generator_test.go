package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComponentAssets_convertServiceMonitor(t *testing.T) {
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
				assert.Equal(t, defaultScrapeInterval, doc.Spec.Endpoints[0].Interval)
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
				monitor.Spec.Endpoints = []prometheusoperatorv1.Endpoint{{TargetPort: ptr(intstr.FromString("metrics"))}}
				return monitor
			}(),
			validateDocument: func(t *testing.T, doc *podMonitoringDoc) {
				assert.Equal(t, portRef{Value: intstr.FromString("metrics")}, doc.Spec.Endpoints[0].Port)
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
			name: "When the endpoint CA is a ConfigMap, it should reference the component's CA-only Secret",
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
								LocalObjectReference: corev1.LocalObjectReference{Name: "kube-controller-manager-metrics-client-ca"},
								Key:                  "ca.crt",
							},
						},
					},
				}
				return monitor
			}(),
			validateDocument: func(t *testing.T, doc *podMonitoringDoc) {
				require.NotNil(t, doc.Spec.Endpoints[0].TLS)
				assert.Equal(t, &secretRefDoc{Secret: secretKeyRefDoc{Name: "kube-controller-manager-metrics-client-ca", Key: "ca.crt"}}, doc.Spec.Endpoints[0].TLS.CA)
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
			name: "When the monitor sets jobLabel, it should fail generation",
			assets: &componentAssets{
				services:       []*corev1.Service{baseService()},
				containerPorts: []corev1.ContainerPort{{Name: "metrics", ContainerPort: 10257}},
			},
			monitor: func() *prometheusServiceMonitor {
				monitor := baseMonitor()
				monitor.Spec.JobLabel = "app"
				return monitor
			}(),
			expectError: "jobLabel is not representable",
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
				assert.Equal(t, []relabelingRuleDoc{{Action: "keep", Regex: "up"}}, doc.Spec.Endpoints[0].MetricRelabeling)
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

func TestComponentAssets_convertPodMonitor(t *testing.T) {
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
				monitor.Spec.PodMetricsEndpoints[0].BearerTokenSecret = &corev1.SecretKeySelector{}
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
		})
	}
}

func TestComponentAssets_convertTLS(t *testing.T) {
	minVersion := prometheusoperatorv1.TLSVersion12

	testCases := []struct {
		name         string
		componentDir string
		monitorName  string
		config       *prometheusoperatorv1.SafeTLSConfig
		expectError  string
		validate     func(t *testing.T, tls *tlsDoc)
	}{
		{
			name:        "When the CA is a Secret, it should pass the Secret reference through",
			monitorName: "etcd",
			config: &prometheusoperatorv1.SafeTLSConfig{
				CA: prometheusoperatorv1.SecretOrConfigMap{
					Secret: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "etcd-metrics-client-ca"},
						Key:                  "ca.crt",
					},
				},
			},
			validate: func(t *testing.T, tls *tlsDoc) {
				assert.Equal(t, &secretRefDoc{Secret: secretKeyRefDoc{Name: "etcd-metrics-client-ca", Key: "ca.crt"}}, tls.CA)
			},
		},
		{
			name:         "When the component overrides the CA Secret name, it should use the override for a ConfigMap CA",
			componentDir: filepath.Join("assets", "etcd"),
			monitorName:  "etcd-shard-0",
			config: &prometheusoperatorv1.SafeTLSConfig{
				CA: prometheusoperatorv1.SecretOrConfigMap{
					ConfigMap: &corev1.ConfigMapKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "etcd-metrics-client-ca"},
						Key:                  "ca.crt",
					},
				},
			},
			validate: func(t *testing.T, tls *tlsDoc) {
				assert.Equal(t, &secretRefDoc{Secret: secretKeyRefDoc{Name: "etcd-metrics-client-ca", Key: "ca.crt"}}, tls.CA)
			},
		},
		{
			name:         "When there is no override, it should derive the CA Secret name from the monitor name",
			componentDir: filepath.Join("assets", "cluster-version-operator"),
			monitorName:  "cluster-version-operator",
			config: &prometheusoperatorv1.SafeTLSConfig{
				CA: prometheusoperatorv1.SecretOrConfigMap{
					ConfigMap: &corev1.ConfigMapKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "cluster-version-operator-metrics-client-ca"},
						Key:                  "ca.crt",
					},
				},
			},
			validate: func(t *testing.T, tls *tlsDoc) {
				assert.Equal(t, &secretRefDoc{Secret: secretKeyRefDoc{Name: "cluster-version-operator-metrics-client-ca", Key: "ca.crt"}}, tls.CA)
			},
		},
		{
			name:        "When the TLS config sets versions and server name, it should carry them into the PodMonitoring",
			monitorName: "etcd",
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
			name:        "When a client certificate has no key, it should fail generation",
			monitorName: "etcd",
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
			name:        "When the client certificate comes from a ConfigMap, it should fail generation",
			monitorName: "etcd",
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
			assets := &componentAssets{dir: tc.componentDir}
			if assets.dir == "" {
				assets.dir = filepath.Join("assets", tc.monitorName)
			}

			tls, err := assets.convertTLS(tc.config, tc.monitorName)
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
		tls, err := (&componentAssets{}).convertTLS(nil, "etcd")
		require.NoError(t, err)
		assert.Nil(t, tls)
	})
}

func TestComponentAssets_containerPortRef(t *testing.T) {
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
			Regex:        "(.*)",
			Replacement:  &replacement,
			Action:       "Replace",
		}})
		require.NoError(t, err)
		assert.Equal(t, []relabelingRuleDoc{{
			SourceLabels: []string{"__name__", "job"}, Separator: ",", TargetLabel: "_id",
			Regex: "(.*)", Replacement: "cluster-$1", Action: "replace",
		}}, actual)
	})
	t.Run("When CoreOS uses labelmap, it should reject unsupported GMP action", func(t *testing.T) {
		_, err := convertMetricRelabeling([]prometheusoperatorv1.RelabelConfig{{Action: "labelmap"}})
		require.ErrorContains(t, err, `action "labelmap" is not supported by GMP`)
	})
	t.Run("When CoreOS explicitly uses an empty separator, it should reject the semantic mismatch", func(t *testing.T) {
		empty := ""
		_, err := convertMetricRelabeling([]prometheusoperatorv1.RelabelConfig{{Separator: &empty}})
		require.ErrorContains(t, err, "empty separator cannot be preserved")
	})
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

		result, err := generateAll(assetsDir, generatedDir, nil)
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

		result, err := generateAll(assetsDir, generatedDir, map[string]string{"platform-specific": "test"})
		require.NoError(t, err)
		assert.Empty(t, result.generated)
		assert.Equal(t, []string{stalePath}, result.stale)
	})

	t.Run("When an excluded component has no directory, it should fail generation", func(t *testing.T) {
		root := t.TempDir()
		assetsDir := filepath.Join(root, "assets")
		require.NoError(t, os.MkdirAll(assetsDir, 0o755))
		writeComponent(t, assetsDir, "test-component")
		_, err := generateAll(assetsDir, filepath.Join(root, "gmpassets"), map[string]string{"removed-component": "test"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `excluded component "removed-component" has no directory`)
	})

	t.Run("When an excluded component has no CoreOS monitor, it should fail generation", func(t *testing.T) {
		root := t.TempDir()
		assetsDir := filepath.Join(root, "assets")
		require.NoError(t, os.MkdirAll(filepath.Join(assetsDir, "no-monitor"), 0o755))
		_, err := generateAll(assetsDir, filepath.Join(root, "gmpassets"), map[string]string{"no-monitor": "test"})
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
		_, err := generateAll(assetsDir, generatedDir, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "is hand-written")
	})

	t.Run("When output equals the active asset directory, it should fail", func(t *testing.T) {
		root := t.TempDir()
		_, err := generateAll(root, root, nil)
		require.ErrorContains(t, err, "outside the active CPO asset directory")
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
		assert.Contains(t, err.Error(), "jobLabel is not representable")
	})

	t.Run("When the ServiceMonitor is not valid YAML, it should fail generation", func(t *testing.T) {
		componentDir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(componentDir, sourceServiceMonitor), []byte("kind: [ServiceMonitor"), 0o644))

		_, err := generateComponent(componentDir, sourceServiceMonitor)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to parse")
	})
}

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
