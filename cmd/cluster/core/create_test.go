package core

import (
	"testing"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
)

// This test will make sure the order of the objects is correct
// being the HC and NP the last ones and the firts one the namespace.
func TestAsObjects(t *testing.T) {
	tests := []struct {
		name         string
		resources    *resources
		expectedFail bool
	}{
		{
			name: "All resources are present",
			resources: &resources{
				Namespace:             &corev1.Namespace{},
				AdditionalTrustBundle: &corev1.ConfigMap{},
				PullSecret:            &corev1.Secret{},
				SSHKey:                &corev1.Secret{},
				Cluster:               &hyperv1.HostedCluster{},
				Resources: []crclient.Object{
					&corev1.ConfigMap{},
					&corev1.Secret{},
				},
				NodePools: []*hyperv1.NodePool{{}, {}},
			},
			expectedFail: false,
		},
		{
			name: "Namespace resource is nil",
			resources: &resources{
				Namespace:             nil,
				AdditionalTrustBundle: &corev1.ConfigMap{},
				PullSecret:            &corev1.Secret{},
				SSHKey:                &corev1.Secret{},
				Cluster:               &hyperv1.HostedCluster{},
				Resources: []crclient.Object{
					&corev1.ConfigMap{},
					&corev1.Secret{},
				},
				NodePools: []*hyperv1.NodePool{{}, {}},
			},
			expectedFail: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			objects := tc.resources.asObjects()
			if tc.expectedFail {
				g.Expect(objects[0]).To(Not(Equal(tc.resources.Namespace)))
				return
			}
			g.Expect(objects[0]).To(Equal(tc.resources.Namespace), "Namespace should be the first object in the slice")
			hcPosition := len(objects) - len(tc.resources.NodePools) - 1
			g.Expect(objects[hcPosition]).To(Equal(tc.resources.Cluster), "HostedCluster should be the secodn-to-last object in the slice")
			g.Expect(objects[len(objects)-1]).To(Equal(tc.resources.NodePools[len(tc.resources.NodePools)-1]), "NodePools should be the last object in the slice")
		})
	}
}
