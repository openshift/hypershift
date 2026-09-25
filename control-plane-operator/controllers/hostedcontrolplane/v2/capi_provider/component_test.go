package capiprovider

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/support/upsert"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
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

func TestCAPIProviderOptions_IsRequestServing(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	capi := &CAPIProviderOptions{}
	g.Expect(capi.IsRequestServing()).To(BeFalse())
}

func TestCAPIProviderOptions_MultiZoneSpread(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	capi := &CAPIProviderOptions{}
	g.Expect(capi.MultiZoneSpread()).To(BeFalse())
}

func TestCAPIProviderOptions_NeedsManagementKASAccess(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	capi := &CAPIProviderOptions{}
	g.Expect(capi.NeedsManagementKASAccess()).To(BeTrue())
}

func TestLabels(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	result := labels()

	g.Expect(result).To(HaveKeyWithValue("control-plane", "capi-provider-controller-manager"))
	g.Expect(result).To(HaveKeyWithValue("app", "capi-provider-controller-manager"))
	g.Expect(result).To(HaveLen(2))
}

func TestNewComponent(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hcp",
			Namespace: "test-namespace",
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			Labels: map[string]string{
				"control-plane": "attacker",
				"app":           "attacker",
				"team":          "platform",
			},
		},
	}
	cpContext := component.ControlPlaneContext{
		Context:                t.Context(),
		Client:                 fake.NewClientBuilder().WithScheme(api.Scheme).Build(),
		HCP:                    hcp,
		ApplyProvider:          upsert.NewApplyProvider(false),
		ReleaseImageProvider:   testutil.FakeImageProvider(),
		SkipPredicate:          true,
		SkipCertificateSigning: true,
		OmitOwnerReference:     true,
	}
	deploymentSpec := &appsv1.DeploymentSpec{
		Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "provider", Image: "test-image:v1.0.0"}},
			},
		},
	}

	g.Expect(NewComponent(deploymentSpec, nil, hyperv1.AWSPlatform).Reconcile(cpContext)).To(Succeed())

	deployment := &appsv1.Deployment{}
	g.Expect(cpContext.Client.Get(t.Context(), client.ObjectKey{Namespace: hcp.Namespace, Name: ComponentName}, deployment)).To(Succeed())
	g.Expect(deployment.Spec.Selector.MatchLabels).To(Equal(labels()))
	g.Expect(deployment.Spec.Template.Labels).To(HaveKeyWithValue("control-plane", "capi-provider-controller-manager"))
	g.Expect(deployment.Spec.Template.Labels).To(HaveKeyWithValue("app", "capi-provider-controller-manager"))
	g.Expect(deployment.Spec.Template.Labels).To(HaveKeyWithValue("team", "platform"))
}
