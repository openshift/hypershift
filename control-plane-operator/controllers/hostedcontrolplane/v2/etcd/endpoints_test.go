package etcd

import (
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestMemberClientEndpoints(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name          string
		availability  hyperv1.AvailabilityPolicy
		expectedCount int
	}{
		{
			name:          "When HighlyAvailable, it should return one endpoint per member",
			availability:  hyperv1.HighlyAvailable,
			expectedCount: 3,
		},
		{
			name:          "When SingleReplica, it should return a single member endpoint",
			availability:  hyperv1.SingleReplica,
			expectedCount: 1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewGomegaWithT(t)

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-hcp-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					ControllerAvailabilityPolicy: tc.availability,
				},
			}

			endpoints := MemberClientEndpoints(hcp)
			g.Expect(endpoints).To(HaveLen(tc.expectedCount))
			for i, endpoint := range endpoints {
				g.Expect(endpoint).To(Equal(fmt.Sprintf("https://etcd-%d.etcd-discovery.test-hcp-namespace.svc:2379", i)))
			}
		})
	}
}
