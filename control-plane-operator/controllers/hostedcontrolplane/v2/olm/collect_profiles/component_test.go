package collectprofiles

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
		name           string
		platformType   hyperv1.PlatformType
		expectedResult bool
	}{
		{
			name:           "When platform is AWS, it should return true",
			platformType:   hyperv1.AWSPlatform,
			expectedResult: true,
		},
		{
			name:           "When platform is Azure, it should return true",
			platformType:   hyperv1.AzurePlatform,
			expectedResult: true,
		},
		{
			name:           "When platform is IBMCloud, it should return false",
			platformType:   hyperv1.IBMCloudPlatform,
			expectedResult: false,
		},
		{
			name:           "When platform is KubeVirt, it should return true",
			platformType:   hyperv1.KubevirtPlatform,
			expectedResult: true,
		},
		{
			name:           "When platform is None, it should return true",
			platformType:   hyperv1.NonePlatform,
			expectedResult: true,
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
					Platform: hyperv1.PlatformSpec{
						Type: tc.platformType,
					},
				},
			}

			cpContext := component.WorkloadContext{
				HCP: hcp,
			}

			result, err := predicate(cpContext)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(result).To(Equal(tc.expectedResult))
		})
	}
}
