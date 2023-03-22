package kubevirt

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apiresource "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubevirtv1 "kubevirt.io/api/core/v1"
	"kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
	capikubevirt "sigs.k8s.io/cluster-api-provider-kubevirt/api/v1alpha1"
)

func TestKubevirtMachineTemplate(t *testing.T) {
	testCases := []struct {
		name     string
		nodePool *hyperv1.NodePool
		hcluster *hyperv1.HostedCluster
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
					Replicas:    nil,
					Config:      nil,
					Management:  hyperv1.NodePoolManagement{},
					AutoScaling: nil,
					Platform: hyperv1.NodePoolPlatform{
						Type:     hyperv1.KubevirtPlatform,
						Kubevirt: generateKubevirtPlatform("5Gi", 4, "testimage", "32Gi"),
					},
					Release: hyperv1.Release{},
				},
			},
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-hostedcluster",
					Namespace: "clusters",
				},
				Spec: hyperv1.HostedClusterSpec{
					InfraID: "1234",
				},
			},

			expected: &capikubevirt.KubevirtMachineTemplateSpec{
				Template: capikubevirt.KubevirtMachineTemplateResource{
					Spec: capikubevirt.KubevirtMachineSpec{
						VirtualMachineTemplate: *generateNodeTemplate("5Gi", 4, "docker://testimage", "32Gi"),
					},
				},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			err := PlatformValidation(tc.nodePool)
			g.Expect(err).ToNot(HaveOccurred())

			result := MachineTemplateSpec("", tc.nodePool, tc.hcluster)
			if !equality.Semantic.DeepEqual(tc.expected, result) {
				t.Errorf(cmp.Diff(tc.expected, result))
			}
		})
	}
}

func generateKubevirtPlatform(memory string, cores uint32, image string, volumeSize string) *hyperv1.KubevirtNodePoolPlatform {
	memoryQuantity := apiresource.MustParse(memory)
	volumeSizeQuantity := apiresource.MustParse(volumeSize)

	exampleTemplate := &hyperv1.KubevirtNodePoolPlatform{
		RootVolume: &hyperv1.KubevirtRootVolume{
			Image: &hyperv1.KubevirtDiskImage{
				ContainerDiskImage: &image,
			},
			KubevirtVolume: hyperv1.KubevirtVolume{
				Type: hyperv1.KubevirtVolumeTypePersistent,
				Persistent: &hyperv1.KubevirtPersistentVolume{
					Size:         &volumeSizeQuantity,
					StorageClass: nil,
				},
			},
		},
		Compute: &hyperv1.KubevirtCompute{
			Memory: &memoryQuantity,
			Cores:  &cores,
		},
	}

	return exampleTemplate
}

func generateNodeTemplate(memory string, cpu uint32, image string, volumeSize string) *capikubevirt.VirtualMachineTemplateSpec {
	runAlways := kubevirtv1.RunStrategyAlways
	guestQuantity := apiresource.MustParse(memory)
	volumeSizeQuantity := apiresource.MustParse(volumeSize)
	nodePoolNameLabelKey := hyperv1.NodePoolNameLabel
	infraIDLabelKey := hyperv1.InfraIDLabel
	pullMethod := v1beta1.RegistryPullNode

	return &capikubevirt.VirtualMachineTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				nodePoolNameLabelKey: "my-pool",
				infraIDLabelKey:      "1234",
			},
		},
		Spec: kubevirtv1.VirtualMachineSpec{
			RunStrategy: &runAlways,
			DataVolumeTemplates: []kubevirtv1.DataVolumeTemplateSpec{
				{
					TypeMeta: metav1.TypeMeta{},
					ObjectMeta: metav1.ObjectMeta{
						Name: "rhcos",
					},
					Spec: v1beta1.DataVolumeSpec{
						Source: &v1beta1.DataVolumeSource{
							Registry: &v1beta1.DataVolumeSourceRegistry{
								URL:        &image,
								PullMethod: &pullMethod,
							},
						},
						Storage: &v1beta1.StorageSpec{
							Resources: corev1.ResourceRequirements{
								Requests: map[corev1.ResourceName]apiresource.Quantity{
									corev1.ResourceStorage: volumeSizeQuantity,
								},
							},
						},
					},
				},
			},
			Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						nodePoolNameLabelKey: "my-pool",
						infraIDLabelKey:      "1234",
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
									Name: "rhcos",
									DiskDevice: kubevirtv1.DiskDevice{
										Disk: &kubevirtv1.DiskTarget{
											Bus: "virtio",
										},
									},
								},
							},
							Interfaces: []kubevirtv1.Interface{
								{
									Name: "default",
									InterfaceBindingMethod: kubevirtv1.InterfaceBindingMethod{
										Bridge: &kubevirtv1.InterfaceBridge{},
									},
								},
							},
						},
					},

					Networks: []kubevirtv1.Network{
						{
							Name: "default",
							NetworkSource: kubevirtv1.NetworkSource{
								Pod: &kubevirtv1.PodNetwork{},
							},
						},
					},
					Volumes: []kubevirtv1.Volume{
						{
							Name: "rhcos",
							VolumeSource: kubevirtv1.VolumeSource{
								DataVolume: &kubevirtv1.DataVolumeSource{
									Name: "rhcos",
								},
							},
						},
					},
				},
			},
		},
	}
}
