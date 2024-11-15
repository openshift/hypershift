package openstack

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	capiopenstackv1beta1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
)

const flavor = "m1.xlarge"
const imageName = "rhcos"

func TestOpenStackMachineTemplate(t *testing.T) {
	testCases := []struct {
		name                string
		nodePool            hyperv1.NodePoolSpec
		nodePoolAnnotations map[string]string
		expected            *capiopenstackv1beta1.OpenStackMachineTemplateSpec
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

			expected: &capiopenstackv1beta1.OpenStackMachineTemplateSpec{
				Template: capiopenstackv1beta1.OpenStackMachineTemplateResource{
					Spec: capiopenstackv1beta1.OpenStackMachineSpec{
						Flavor: ptr.To(flavor),
						Image: capiopenstackv1beta1.ImageParam{
							Filter: &capiopenstackv1beta1.ImageFilter{
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
						AdditionalPorts: []hyperv1.PortSpec{
							{
								Network: &hyperv1.NetworkParam{
									ID: ptr.To("123"),
								},
								VNICType:           "direct",
								PortSecurityPolicy: hyperv1.PortSecurityDisabled,
							},
						},
					},
				},
				Release: hyperv1.Release{},
			},

			expected: &capiopenstackv1beta1.OpenStackMachineTemplateSpec{
				Template: capiopenstackv1beta1.OpenStackMachineTemplateResource{
					Spec: capiopenstackv1beta1.OpenStackMachineSpec{
						Flavor: ptr.To(flavor),
						Image: capiopenstackv1beta1.ImageParam{
							Filter: &capiopenstackv1beta1.ImageFilter{
								Name: ptr.To(imageName),
							},
						},
						Ports: []capiopenstackv1beta1.PortOpts{
							{},
							{
								Description: ptr.To("Additional port for Hypershift node pool tests"),
								Network: &capiopenstackv1beta1.NetworkParam{
									ID: ptr.To("123"),
								},
								ResolvedPortSpecFields: capiopenstackv1beta1.ResolvedPortSpecFields{
									DisablePortSecurity: ptr.To(true),
									VNICType:            ptr.To("direct"),
								},
							},
						},
					},
				},
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
					ObjectMeta: metav1.ObjectMeta{
						Name: "tests",
					},
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
