package ignitionserverproxy

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPredicate(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name        string
		platform    hyperv1.PlatformType
		annotations map[string]string
		expected    bool
	}{
		{
			name:     "When platform is AWS and ignition is not disabled, it should return true",
			platform: hyperv1.AWSPlatform,
			expected: true,
		},
		{
			name:     "When platform is Azure and ignition is not disabled, it should return true",
			platform: hyperv1.AzurePlatform,
			expected: true,
		},
		{
			name:     "When platform is IBMCloud, it should return false",
			platform: hyperv1.IBMCloudPlatform,
			expected: false,
		},
		{
			name:     "When platform is PowerVS, it should return true",
			platform: hyperv1.PowerVSPlatform,
			expected: true,
		},
		{
			name:     "When platform is KubeVirt, it should return true",
			platform: hyperv1.KubevirtPlatform,
			expected: true,
		},
		{
			name:     "When platform is Agent, it should return true",
			platform: hyperv1.AgentPlatform,
			expected: true,
		},
		{
			name:     "When platform is OpenStack, it should return true",
			platform: hyperv1.OpenStackPlatform,
			expected: true,
		},
		{
			name:     "When DisableIgnitionServerAnnotation is set, it should return false",
			platform: hyperv1.AWSPlatform,
			annotations: map[string]string{
				hyperv1.DisableIgnitionServerAnnotation: "true",
			},
			expected: false,
		},
		{
			name:     "When DisableIgnitionServerAnnotation is set on IBMCloud, it should return false",
			platform: hyperv1.IBMCloudPlatform,
			annotations: map[string]string{
				hyperv1.DisableIgnitionServerAnnotation: "true",
			},
			expected: false,
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
					Annotations: tc.annotations,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: tc.platform,
					},
				},
			}

			cpContext := component.WorkloadContext{
				HCP: hcp,
			}

			result, err := predicate(cpContext)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(result).To(Equal(tc.expected))
		})
	}
}

func TestIsRequestServing(t *testing.T) {
	t.Parallel()

	g := NewWithT(t)

	proxy := &ignitionServerProxy{}
	g.Expect(proxy.IsRequestServing()).To(BeTrue())
}

func TestMultiZoneSpread(t *testing.T) {
	t.Parallel()

	g := NewWithT(t)

	proxy := &ignitionServerProxy{}
	g.Expect(proxy.MultiZoneSpread()).To(BeTrue())
}

func TestNeedsManagementKASAccess(t *testing.T) {
	t.Parallel()

	g := NewWithT(t)

	proxy := &ignitionServerProxy{}
	g.Expect(proxy.NeedsManagementKASAccess()).To(BeFalse())
}
