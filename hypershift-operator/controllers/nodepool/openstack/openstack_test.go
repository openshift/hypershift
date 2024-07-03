package openstack

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/utils/ptr"
	capiopenstack "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
)

const flavor = "m1.xlarge"
const imageName = "rhcos"

func TestOpenStackMachineTemplate(t *testing.T) {
	testCases := []struct {
		name                string
		cluster             hyperv1.HostedClusterSpec
		clusterStatus       *hyperv1.HostedClusterStatus
		nodePool            hyperv1.NodePoolSpec
		nodePoolAnnotations map[string]string
		expected            *capiopenstack.OpenStackMachineTemplateSpec
		checkError          func(*testing.T, error)
	}{
		{
			name: "basic valid node pool",
			nodePool: hyperv1.NodePoolSpec{
				ClusterName: "",
				Replicas:    nil,
				Config:      nil,
				Management:  hyperv1.NodePoolManagement{},
				AutoScaling: nil,
				Platform: hyperv1.NodePoolPlatform{
					Type: hyperv1.OpenStackPlatform,
					OpenStack: &hyperv1.OpenStackNodePoolPlatform{
						Flavor:    flavor,
						ImageName: imageName,
					},
				},
				Release: hyperv1.Release{},
			},

			expected: &capiopenstack.OpenStackMachineTemplateSpec{
				Template: capiopenstack.OpenStackMachineTemplateResource{
					Spec: capiopenstack.OpenStackMachineSpec{
						Flavor: flavor,
						Image: capiopenstack.ImageParam{
							Filter: &capiopenstack.ImageFilter{
								Name: ptr.To(imageName),
							},
						},
					},
				},
			},
		},
		{
			name: "missing image name",
			nodePool: hyperv1.NodePoolSpec{
				ClusterName: "",
				Replicas:    nil,
				Config:      nil,
				Management:  hyperv1.NodePoolManagement{},
				AutoScaling: nil,
				Platform: hyperv1.NodePoolPlatform{
					Type: hyperv1.OpenStackPlatform,
					OpenStack: &hyperv1.OpenStackNodePoolPlatform{
						Flavor: flavor,
					},
				},
				Release: hyperv1.Release{},
			},

			checkError: func(t *testing.T, err error) {
				if err == nil {
					t.Errorf("image name is required")
				}
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.cluster.Platform.OpenStack == nil {
				tc.cluster.Platform.OpenStack = &hyperv1.OpenStackPlatformSpec{}
			}
			if tc.nodePool.Platform.OpenStack == nil {
				tc.nodePool.Platform.OpenStack = &hyperv1.OpenStackNodePoolPlatform{}
			}
			result, err := MachineTemplateSpec(
				&hyperv1.HostedCluster{Spec: tc.cluster},
				&hyperv1.NodePool{
					Spec: tc.nodePool,
				})
			if tc.checkError != nil {
				tc.checkError(t, err)
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
			if tc.expected == nil {
				return
			}
			if !equality.Semantic.DeepEqual(tc.expected, result) {
				t.Errorf(cmp.Diff(tc.expected, result))
			}
		})
	}
}
