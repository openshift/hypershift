package nodepool

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/equality"
	apiresource "k8s.io/apimachinery/pkg/api/resource"
	kubevirtv1 "kubevirt.io/api/core/v1"
	capikubevirt "sigs.k8s.io/cluster-api-provider-kubevirt/api/v1alpha1"
)

func TestKubevirtMachineTemplate(t *testing.T) {
	testCases := []struct {
		name     string
		nodePool *hyperv1.NodePool
		expected capikubevirt.KubevirtMachineTemplateSpec
	}{
		{
			name: "happy flow",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					ClusterName: "",
					NodeCount:   nil,
					Config:      nil,
					Management:  hyperv1.NodePoolManagement{},
					AutoScaling: nil,
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.KubevirtPlatform,
						Kubevirt: &hyperv1.KubevirtNodePoolPlatform{
							NodeTemplate: generateNodeTemplate("5Gi", 4, "testimage"),
						},
					},
					Release: hyperv1.Release{},
				},
			},

			expected: capikubevirt.KubevirtMachineTemplateSpec{
				Template: capikubevirt.KubevirtMachineTemplateResource{
					Spec: capikubevirt.KubevirtMachineSpec{
						VirtualMachineTemplate: *generateNodeTemplate("5Gi", 4, "testimage"),
					},
				},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, _ := kubevirtMachineTemplate(tc.nodePool, "testNamespace")
			if !equality.Semantic.DeepEqual(tc.expected, result.Spec) {
				t.Errorf(cmp.Diff(tc.expected, result.Spec))
			}
		})
	}
}

func generateNodeTemplate(memory string, cpu uint32, image string) *capikubevirt.VirtualMachineTemplateSpec {
	runAlways := kubevirtv1.RunStrategyAlways
	guestQuantity := apiresource.MustParse(memory)
	return &capikubevirt.VirtualMachineTemplateSpec{
		Spec: kubevirtv1.VirtualMachineSpec{
			RunStrategy: &runAlways,
			Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
				Spec: kubevirtv1.VirtualMachineInstanceSpec{
					Domain: kubevirtv1.DomainSpec{
						CPU:    &kubevirtv1.CPU{Cores: cpu},
						Memory: &kubevirtv1.Memory{Guest: &guestQuantity},
						Devices: kubevirtv1.Devices{
							Disks: []kubevirtv1.Disk{
								{
									Name: "containervolume",
									DiskDevice: kubevirtv1.DiskDevice{
										Disk: &kubevirtv1.DiskTarget{
											Bus: "virtio",
										},
									},
								},
							},
						},
					},
					Volumes: []kubevirtv1.Volume{
						{
							Name: "containervolume",
							VolumeSource: kubevirtv1.VolumeSource{
								ContainerDisk: &kubevirtv1.ContainerDiskSource{
									Image: image,
								},
							},
						},
					},
				},
			},
		},
	}
}
