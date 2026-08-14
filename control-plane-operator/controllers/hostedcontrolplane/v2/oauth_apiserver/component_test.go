package oapi

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"

	configv1 "github.com/openshift/api/config/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestOpenshiftOAuthAPIServerOptions(t *testing.T) {
	t.Parallel()

	options := &openshiftOAuthAPIServer{}

	t.Run("When checking IsRequestServing, it should return false", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		g.Expect(options.IsRequestServing()).To(BeFalse())
	})

	t.Run("When checking MultiZoneSpread, it should return true", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		g.Expect(options.MultiZoneSpread()).To(BeTrue())
	})

	t.Run("When checking NeedsManagementKASAccess, it should return false", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		g.Expect(options.NeedsManagementKASAccess()).To(BeFalse())
	})
}

func TestPredicate(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name          string
		hcp           *hyperv1.HostedControlPlane
		expectedValue bool
	}{
		{
			name: "When OAuth is enabled by default, it should return true",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-ns",
				},
			},
			expectedValue: true,
		},
		{
			name: "When OAuth is explicitly enabled with nil configuration, it should return true",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-ns",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Configuration: nil,
				},
			},
			expectedValue: true,
		},
		{
			name: "When OAuth is enabled with empty authentication config, it should return true",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-ns",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Configuration: &hyperv1.ClusterConfiguration{},
				},
			},
			expectedValue: true,
		},
		{
			name: "When authentication type is OIDC, it should return false",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-ns",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Configuration: &hyperv1.ClusterConfiguration{
						Authentication: &configv1.AuthenticationSpec{
							Type: configv1.AuthenticationTypeOIDC,
						},
					},
				},
			},
			expectedValue: false,
		},
		{
			name: "When authentication type is IntegratedOAuth, it should return true",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-ns",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Configuration: &hyperv1.ClusterConfiguration{
						Authentication: &configv1.AuthenticationSpec{
							Type: configv1.AuthenticationTypeIntegratedOAuth,
						},
					},
				},
			},
			expectedValue: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			cpContext := component.WorkloadContext{
				HCP: tc.hcp,
			}

			result, err := predicate(cpContext)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(result).To(Equal(tc.expectedValue))
		})
	}
}
