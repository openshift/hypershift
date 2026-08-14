package nto

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/metrics"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

func TestAdaptServiceMonitor(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name               string
		namespace          string
		clusterID          string
		metricsSet         metrics.MetricsSet
		expectedServerName string
	}{
		{
			name:               "When service monitor is adapted with default namespace, it should set namespace selector and server name",
			namespace:          "test-namespace",
			clusterID:          "test-cluster-id",
			metricsSet:         metrics.MetricsSetTelemetry,
			expectedServerName: "node-tuning-operator.test-namespace.svc",
		},
		{
			name:               "When service monitor is adapted with different namespace, it should update server name accordingly",
			namespace:          "clusters-production",
			clusterID:          "prod-cluster",
			metricsSet:         metrics.MetricsSetSRE,
			expectedServerName: "node-tuning-operator.clusters-production.svc",
		},
		{
			name:               "When service monitor is adapted with all metrics set, it should configure correctly",
			namespace:          "clusters-dev",
			clusterID:          "dev-cluster",
			metricsSet:         metrics.MetricsSetAll,
			expectedServerName: "node-tuning-operator.clusters-dev.svc",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			g := NewWithT(t)

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: tc.namespace,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					ClusterID: tc.clusterID,
				},
			}

			cpContext := component.WorkloadContext{
				Context:    t.Context(),
				HCP:        hcp,
				MetricsSet: tc.metricsSet,
			}

			sm, _, err := assets.LoadManifest(ComponentName, "servicemonitor.yaml")
			g.Expect(err).ToNot(HaveOccurred())

			serviceMonitor, ok := sm.(*prometheusoperatorv1.ServiceMonitor)
			g.Expect(ok).To(BeTrue())
			serviceMonitor.Namespace = tc.namespace

			err = adaptServiceMonitor(cpContext, serviceMonitor)
			g.Expect(err).ToNot(HaveOccurred())

			// Verify namespace selector
			g.Expect(serviceMonitor.Spec.NamespaceSelector.MatchNames).To(Equal([]string{tc.namespace}))

			// Verify TLS server name
			g.Expect(serviceMonitor.Spec.Endpoints).ToNot(BeEmpty())
			g.Expect(serviceMonitor.Spec.Endpoints[0].TLSConfig).ToNot(BeNil())
			g.Expect(serviceMonitor.Spec.Endpoints[0].TLSConfig.ServerName).To(Equal(ptr.To(tc.expectedServerName)))

			// Verify metric relabel configs are set based on metrics set
			// ApplyClusterIDLabel appends cluster ID to MetricRelabelConfigs, so we need to account for that
			actualMetricRelabelConfigs := serviceMonitor.Spec.Endpoints[0].MetricRelabelConfigs
			expectedBaseConfigs := metrics.NTORelabelConfigs(tc.metricsSet)

			// The last entry should be the cluster ID label
			g.Expect(actualMetricRelabelConfigs).ToNot(BeEmpty())
			lastConfig := actualMetricRelabelConfigs[len(actualMetricRelabelConfigs)-1]
			g.Expect(lastConfig.TargetLabel).To(Equal("_id"))
			g.Expect(*lastConfig.Replacement).To(Equal(tc.clusterID))

			// Check the configs before the cluster ID match the expected
			configsBeforeClusterID := actualMetricRelabelConfigs[:len(actualMetricRelabelConfigs)-1]
			if expectedBaseConfigs == nil {
				g.Expect(configsBeforeClusterID).To(BeEmpty())
			} else {
				g.Expect(configsBeforeClusterID).To(Equal(expectedBaseConfigs))
			}

			// Verify cluster ID label is also applied to RelabelConfigs
			foundClusterIDInRelabelConfigs := false
			for _, relabel := range serviceMonitor.Spec.Endpoints[0].RelabelConfigs {
				if relabel.TargetLabel == "_id" {
					foundClusterIDInRelabelConfigs = true
					g.Expect(*relabel.Replacement).To(Equal(tc.clusterID))
				}
			}
			g.Expect(foundClusterIDInRelabelConfigs).To(BeTrue(), "cluster ID label should be applied to RelabelConfigs")
		})
	}
}

func TestAdaptServiceMonitorErrorCases(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name        string
		modifySM    func(*prometheusoperatorv1.ServiceMonitor)
		expectedErr string
	}{
		{
			name: "When service monitor has no endpoints, it should return error",
			modifySM: func(sm *prometheusoperatorv1.ServiceMonitor) {
				sm.Spec.Endpoints = []prometheusoperatorv1.Endpoint{}
			},
			expectedErr: "has no endpoints defined",
		},
		{
			name: "When service monitor endpoint has no TLS config, it should return error",
			modifySM: func(sm *prometheusoperatorv1.ServiceMonitor) {
				sm.Spec.Endpoints[0].TLSConfig = nil
			},
			expectedErr: "has no TLSConfig",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			g := NewWithT(t)

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					ClusterID: "test-cluster",
				},
			}

			cpContext := component.WorkloadContext{
				Context:    t.Context(),
				HCP:        hcp,
				MetricsSet: metrics.MetricsSetTelemetry,
			}

			sm, _, err := assets.LoadManifest(ComponentName, "servicemonitor.yaml")
			g.Expect(err).ToNot(HaveOccurred())

			serviceMonitor, ok := sm.(*prometheusoperatorv1.ServiceMonitor)
			g.Expect(ok).To(BeTrue())
			serviceMonitor.Namespace = "test-namespace"

			// Apply the modification
			tc.modifySM(serviceMonitor)

			err = adaptServiceMonitor(cpContext, serviceMonitor)
			g.Expect(err).To(HaveOccurred())
			g.Expect(err.Error()).To(ContainSubstring(tc.expectedErr))
		})
	}
}

func TestAdaptServiceMonitorMetricsSetConfigurations(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name                      string
		metricsSet                metrics.MetricsSet
		validateMetricRelabelings func(*GomegaWithT, []prometheusoperatorv1.RelabelConfig)
	}{
		{
			name:       "When metrics set is Telemetry, it should filter to specific NTO metrics",
			metricsSet: metrics.MetricsSetTelemetry,
			validateMetricRelabelings: func(g *GomegaWithT, relabelConfigs []prometheusoperatorv1.RelabelConfig) {
				// Should have 2 configs: the NTO filter + cluster ID label
				g.Expect(relabelConfigs).To(HaveLen(2))
				g.Expect(relabelConfigs[0].Action).To(Equal("keep"))
				g.Expect(relabelConfigs[0].Regex).To(Equal("nto_profile_calculated_total"))
				g.Expect(relabelConfigs[1].TargetLabel).To(Equal("_id"))
			},
		},
		{
			name:       "When metrics set is All, it should not filter metrics",
			metricsSet: metrics.MetricsSetAll,
			validateMetricRelabelings: func(g *GomegaWithT, relabelConfigs []prometheusoperatorv1.RelabelConfig) {
				// Should only have cluster ID label, no filtering
				g.Expect(relabelConfigs).To(HaveLen(1))
				g.Expect(relabelConfigs[0].TargetLabel).To(Equal("_id"))
			},
		},
		{
			name:       "When metrics set is SRE, it should use SRE configuration",
			metricsSet: metrics.MetricsSetSRE,
			validateMetricRelabelings: func(g *GomegaWithT, relabelConfigs []prometheusoperatorv1.RelabelConfig) {
				// SRE metrics set configuration is loaded dynamically,
				// Last entry should be cluster ID
				g.Expect(relabelConfigs).ToNot(BeEmpty())
				lastIdx := len(relabelConfigs) - 1
				g.Expect(relabelConfigs[lastIdx].TargetLabel).To(Equal("_id"))

				// Configs before cluster ID should match SRE config
				expectedConfigs := metrics.NTORelabelConfigs(metrics.MetricsSetSRE)
				configsBeforeClusterID := relabelConfigs[:lastIdx]
				if expectedConfigs == nil {
					g.Expect(configsBeforeClusterID).To(BeEmpty())
				} else {
					g.Expect(configsBeforeClusterID).To(Equal(expectedConfigs))
				}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			g := NewWithT(t)

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					ClusterID: "test-cluster",
				},
			}

			cpContext := component.WorkloadContext{
				Context:    t.Context(),
				HCP:        hcp,
				MetricsSet: tc.metricsSet,
			}

			sm, _, err := assets.LoadManifest(ComponentName, "servicemonitor.yaml")
			g.Expect(err).ToNot(HaveOccurred())

			serviceMonitor, ok := sm.(*prometheusoperatorv1.ServiceMonitor)
			g.Expect(ok).To(BeTrue())
			serviceMonitor.Namespace = "test-namespace"

			err = adaptServiceMonitor(cpContext, serviceMonitor)
			g.Expect(err).ToNot(HaveOccurred())

			// Validate metric relabel configs
			tc.validateMetricRelabelings(g, serviceMonitor.Spec.Endpoints[0].MetricRelabelConfigs)

			// All configurations should have cluster ID relabel config
			foundClusterIDLabel := false
			for _, relabel := range serviceMonitor.Spec.Endpoints[0].RelabelConfigs {
				if relabel.TargetLabel == "_id" {
					foundClusterIDLabel = true
					g.Expect(*relabel.Replacement).To(Equal("test-cluster"))
				}
			}
			g.Expect(foundClusterIDLabel).To(BeTrue(), "cluster ID label should be applied for all metrics sets")
		})
	}
}
