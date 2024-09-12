package core

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestValidateHostedClusterPayloadSupportsNodePoolCPUArch(t *testing.T) {
	for _, testCase := range []struct {
		name                     string
		hc                       *hyperv1.HostedCluster
		nodePoolCPUArch          string
		buildHostedClusterObject bool
		expectedErr              bool
	}{
		{
			name: "when a valid HC exists and the payload type is Multi, then there are no errors",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hc",
					Namespace: "clusters",
				},
				Status: hyperv1.HostedClusterStatus{
					PayloadArch: hyperv1.Multi,
				},
			},
			buildHostedClusterObject: true,
			expectedErr:              false,
		},
		{
			name: "when a valid HC exists and the payload type is AMD64 and the NodePool CPU arch is AMD64, then there are no errors",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hc",
					Namespace: "clusters",
				},
				Status: hyperv1.HostedClusterStatus{
					PayloadArch: hyperv1.AMD64,
				},
			},
			nodePoolCPUArch:          hyperv1.ArchitectureAMD64,
			buildHostedClusterObject: true,
			expectedErr:              false,
		},
		{
			name: "when a valid HC exists and the payload type is AMD64 and the NodePool CPU arch is ARM64, then there is an error",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hc",
					Namespace: "clusters",
				},
				Status: hyperv1.HostedClusterStatus{
					PayloadArch: hyperv1.AMD64,
				},
			},
			nodePoolCPUArch:          hyperv1.ArchitectureARM64,
			buildHostedClusterObject: true,
			expectedErr:              true,
		},
		{
			name: "when a valid HC does not exist, then there are no errors",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hc",
					Namespace: "clusters",
				},
				Status: hyperv1.HostedClusterStatus{
					PayloadArch: hyperv1.AMD64,
				},
			},
			buildHostedClusterObject: false,
			expectedErr:              false,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			g := NewWithT(t)

			var objs []client.Object

			if testCase.buildHostedClusterObject {
				objs = append(objs, testCase.hc)
			}

			c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(objs...).Build()

			err := validateHostedClusterPayloadSupportsNodePoolCPUArch(context.TODO(), c, testCase.hc.Name, testCase.hc.Namespace, testCase.nodePoolCPUArch)
			if testCase.expectedErr {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}
		})
	}
}
