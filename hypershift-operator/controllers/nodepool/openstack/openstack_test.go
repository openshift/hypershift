package openstack

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	capiopenstack "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
)

const flavor = "m1.xlarge"
const imageName = "rhcos"

func TestOpenStackMachineTemplate(t *testing.T) {
	testCases := []struct {
		name                string
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
			name: "additional port for SR-IOV",
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
						AdditionalPorts: []hyperv1.PortOpts{
							{
								Network: &hyperv1.NetworkParam{
									ID: ptr.To("123"),
								},
								ResolvedPortSpecFields: hyperv1.ResolvedPortSpecFields{
									VNICType: "direct",
								},
							},
						},
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
						Ports: []capiopenstack.PortOpts{
							{},
							{
								Description: ptr.To("Additional port for Hypershift node pool "),
								Network: &capiopenstack.NetworkParam{
									ID: ptr.To("123"),
								},
								ResolvedPortSpecFields: capiopenstack.ResolvedPortSpecFields{
									VNICType: ptr.To("direct"),
								},
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
			if tc.nodePool.Platform.OpenStack == nil {
				tc.nodePool.Platform.OpenStack = &hyperv1.OpenStackNodePoolPlatform{}
			}
			hc := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "clusters",
				},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						OpenStack: &hyperv1.OpenStackPlatformSpec{},
					},
					InfraID: "123",
				},
			}
			result, err := MachineTemplateSpec(
				hc,
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
				t.Error(cmp.Diff(tc.expected, result))
			}
		})
	}
}
