package powervs

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPredicate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		hcp      *hyperv1.HostedControlPlane
		expected bool
	}{
		{
			name: "When platform type is PowerVS, it should return true",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.PowerVSPlatform,
					},
				},
			},
			expected: true,
		},
		{
			name: "When platform type is AWS, it should return false",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
					},
				},
			},
			expected: false,
		},
		{
			name: "When platform type is Azure, it should return false",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AzurePlatform,
					},
				},
			},
			expected: false,
		},
		{
			name: "When platform type is KubeVirt, it should return false",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.KubevirtPlatform,
					},
				},
			},
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			cpContext := component.WorkloadContext{
				HCP: tc.hcp,
			}

			result, err := predicate(cpContext)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(result).To(Equal(tc.expected))
		})
	}
}

func TestPowerVSOptions(t *testing.T) {
	t.Parallel()

	t.Run("When IsRequestServing is called, it should return false", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		opts := &powervsOptions{}
		g.Expect(opts.IsRequestServing()).To(BeFalse())
	})

	t.Run("When MultiZoneSpread is called, it should return true", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		opts := &powervsOptions{}
		g.Expect(opts.MultiZoneSpread()).To(BeTrue())
	})

	t.Run("When NeedsManagementKASAccess is called, it should return false", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		opts := &powervsOptions{}
		g.Expect(opts.NeedsManagementKASAccess()).To(BeFalse())
	})
}
