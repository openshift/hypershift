package nodepool

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apiresource "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubevirtv1 "kubevirt.io/api/core/v1"
	capikubevirt "sigs.k8s.io/cluster-api-provider-kubevirt/api/v1alpha1"
)

func TestKubevirtMachineTemplate(t *testing.T) {
	testCases := []struct {
		name     string
		nodePool *hyperv1.NodePool
		expected *capikubevirt.KubevirtMachineTemplateSpec
	}{
		{
			name: "happy flow",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name: "my-pool",
				},
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

			expected: &capikubevirt.KubevirtMachineTemplateSpec{
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
			result := kubevirtMachineTemplateSpec(tc.nodePool)
			if !equality.Semantic.DeepEqual(tc.expected, result) {
				t.Errorf(cmp.Diff(tc.expected, result))
			}
		})
	}
}

func generateNodeTemplate(memory string, cpu uint32, image string) *capikubevirt.VirtualMachineTemplateSpec {
	runAlways := kubevirtv1.RunStrategyAlways
	guestQuantity := apiresource.MustParse(memory)
	nodePoolNameLabelKey := "hypershift.kubevirt.io/node-pool-name"
	return &capikubevirt.VirtualMachineTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				nodePoolNameLabelKey: "my-pool",
			},
		},
		Spec: kubevirtv1.VirtualMachineSpec{
			RunStrategy: &runAlways,
			Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						nodePoolNameLabelKey: "my-pool",
					},
				},
				Spec: kubevirtv1.VirtualMachineInstanceSpec{

					Affinity: &corev1.Affinity{
						PodAntiAffinity: &corev1.PodAntiAffinity{
							PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{
								{
									Weight: int32(100),
									PodAffinityTerm: corev1.PodAffinityTerm{
										LabelSelector: &metav1.LabelSelector{
											MatchExpressions: []metav1.LabelSelectorRequirement{
												{
													Key:      nodePoolNameLabelKey,
													Operator: metav1.LabelSelectorOpIn,
													Values:   []string{"my-pool"},
												},
											},
										},
										TopologyKey: "kubernetes.io/hostname",
									},
								},
							},
						},
					},

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
