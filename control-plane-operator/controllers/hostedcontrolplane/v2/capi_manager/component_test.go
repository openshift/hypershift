package capimanager

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/k8sutil"
	"github.com/openshift/hypershift/support/podspec"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/support/upsert"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

func TestPredicate(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name           string
		hcpAnnotations map[string]string
		expected       bool
	}{
		{
			name:     "When HCP has no annotations, it should return true",
			expected: true,
		},
		{
			name: "When HCP has DisableMachineManagement annotation, it should return false",
			hcpAnnotations: map[string]string{
				hyperv1.DisableMachineManagement: "true",
			},
			expected: false,
		},
		{
			name: "When HCP has other annotations but not DisableMachineManagement, it should return true",
			hcpAnnotations: map[string]string{
				"some.other/annotation": "value",
			},
			expected: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-hcp",
					Namespace:   "test-namespace",
					Annotations: tc.hcpAnnotations,
				},
			}

			cpContext := component.WorkloadContext{
				Context: t.Context(),
				HCP:     hcp,
			}

			result, err := predicate(cpContext)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(result).To(Equal(tc.expected))
		})
	}
}

func TestNewComponent(t *testing.T) {
	t.Parallel()

	const (
		hcpNamespace  = "clusters-example"
		clusterID     = "6f1d2a4e-3c5b-4f7a-9e8d-1b2c3d4e5f60"
		hostedCluster = "clusters/example"
	)

	existingPodMonitor := func() *prometheusoperatorv1.PodMonitor {
		return &prometheusoperatorv1.PodMonitor{
			ObjectMeta: metav1.ObjectMeta{
				Name:      ComponentName,
				Namespace: hcpNamespace,
			},
			Spec: prometheusoperatorv1.PodMonitorSpec{
				Selector: metav1.LabelSelector{MatchLabels: map[string]string{"app": ComponentName}},
				PodMetricsEndpoints: []prometheusoperatorv1.PodMetricsEndpoint{
					{Port: ptr.To("metrics")},
				},
			},
		}
	}

	testCases := []struct {
		name             string
		hcpAnnotations   map[string]string
		existingObjects  []crclient.Object
		expectPodMonitor bool
		expectDeployment bool
	}{
		{
			name: "When machine management is enabled, it should reconcile the cluster-api PodMonitor and the metrics port",
			hcpAnnotations: map[string]string{
				k8sutil.HostedClusterAnnotation: hostedCluster,
			},
			expectPodMonitor: true,
			expectDeployment: true,
		},
		{
			name: "When DisableMachineManagement annotation is set and a cluster-api PodMonitor exists, it should delete the PodMonitor",
			hcpAnnotations: map[string]string{
				k8sutil.HostedClusterAnnotation:  hostedCluster,
				hyperv1.DisableMachineManagement: "true",
			},
			existingObjects:  []crclient.Object{existingPodMonitor()},
			expectPodMonitor: false,
			expectDeployment: false,
		},
		{
			name: "When DisableMachineManagement annotation is set and no PodMonitor exists, it should not create one",
			hcpAnnotations: map[string]string{
				k8sutil.HostedClusterAnnotation:  hostedCluster,
				hyperv1.DisableMachineManagement: "true",
			},
			expectPodMonitor: false,
			expectDeployment: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "hcp",
					Namespace:   hcpNamespace,
					Annotations: tc.hcpAnnotations,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					ClusterID: clusterID,
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithObjects(tc.existingObjects...).
				Build()

			// Mirror how the HyperShift Operator reconciles the CAPI manager: no owner reference
			// and the component predicate evaluated.
			cpContext := component.ControlPlaneContext{
				Context:                t.Context(),
				Client:                 fakeClient,
				ApplyProvider:          upsert.NewApplyProvider(false),
				ReleaseImageProvider:   testutil.FakeImageProvider(),
				HCP:                    hcp,
				OmitOwnerReference:     true,
				SkipCertificateSigning: true,
			}

			g.Expect(NewComponent("").Reconcile(cpContext)).To(Succeed())

			podMonitor := &prometheusoperatorv1.PodMonitor{}
			err := fakeClient.Get(t.Context(), crclient.ObjectKey{Namespace: hcpNamespace, Name: ComponentName}, podMonitor)
			if !tc.expectPodMonitor {
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "expected cluster-api PodMonitor to be absent, got err=%v", err)
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(podMonitor.Spec.Selector.MatchLabels).To(Equal(map[string]string{"app": ComponentName}))
				g.Expect(podMonitor.Spec.NamespaceSelector.MatchNames).To(ConsistOf(hcpNamespace))
				g.Expect(podMonitor.Spec.PodMetricsEndpoints).To(HaveLen(1))
				endpoint := podMonitor.Spec.PodMetricsEndpoints[0]
				g.Expect(endpoint.Port).To(Equal(ptr.To("metrics")))
				g.Expect(endpoint.Path).To(Equal("/metrics"))
				g.Expect(endpoint.Interval).To(Equal(prometheusoperatorv1.Duration("30s")))
				g.Expect(endpoint.Scheme).To(Equal(ptr.To(prometheusoperatorv1.Scheme("http"))))
				clusterIDRelabel := prometheusoperatorv1.RelabelConfig{
					TargetLabel: "_id",
					Action:      "replace",
					Replacement: ptr.To(clusterID),
				}
				g.Expect(endpoint.RelabelConfigs).To(ConsistOf(clusterIDRelabel))
				g.Expect(endpoint.MetricRelabelConfigs).To(ConsistOf(clusterIDRelabel))
				g.Expect(podMonitor.Annotations).To(HaveKeyWithValue(k8sutil.HostedClusterAnnotation, hostedCluster))
			}

			deployment := &appsv1.Deployment{}
			err = fakeClient.Get(t.Context(), crclient.ObjectKey{Namespace: hcpNamespace, Name: ComponentName}, deployment)
			if !tc.expectDeployment {
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "expected cluster-api Deployment to be absent, got err=%v", err)
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				manager := podspec.FindContainer("manager", deployment.Spec.Template.Spec.Containers)
				g.Expect(manager).ToNot(BeNil())
				g.Expect(manager.Ports).To(ContainElement(corev1.ContainerPort{
					Name:          "metrics",
					ContainerPort: 8080,
					Protocol:      corev1.ProtocolTCP,
				}))
				g.Expect(manager.Args).To(ContainElements("--diagnostics-address=:8080", "--insecure-diagnostics"))
			}
		})
	}
}

func TestCAPIManagerOptions_IsRequestServing(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	capi := &CAPIManagerOptions{}
	g.Expect(capi.IsRequestServing()).To(BeFalse())
}

func TestCAPIManagerOptions_MultiZoneSpread(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	capi := &CAPIManagerOptions{}
	g.Expect(capi.MultiZoneSpread()).To(BeFalse())
}

func TestCAPIManagerOptions_NeedsManagementKASAccess(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	capi := &CAPIManagerOptions{}
	g.Expect(capi.NeedsManagementKASAccess()).To(BeTrue())
}
