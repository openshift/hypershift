package kubevirt

import (
	"fmt"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	suppconfig "github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/releaseinfo"

	corev1 "k8s.io/api/core/v1"
	apiresource "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	capikubevirt "sigs.k8s.io/cluster-api-provider-kubevirt/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"github.com/google/go-cmp/cmp"
	"go.uber.org/zap/zaptest"
	kubevirtv1 "kubevirt.io/api/core/v1"
	"kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
)

const (
	namespace              = "my-ns"
	poolName               = "my-pool"
	clusterName            = "my-cluster"
	infraId                = "infraId123"
	hostedClusterNamespace = "hostedClusterNamespace"
	bootImageName          = "https://rhcos.mirror.openshift.com/art/storage/releases/rhcos-4.12/412.86.202209302317-0/x86_64/rhcos-412.86.202209302317-0-openstack.x86_64.qcow2.gz"
	imageHash              = "imageHash"
)

func TestKubevirtMachineTemplate(t *testing.T) {
	testCases := []struct {
		name                    string
		nodePool                *hyperv1.NodePool
		hcluster                *hyperv1.HostedCluster
		expected                *capikubevirt.KubevirtMachineTemplateSpec
		expectedValidationError string
	}{
		{
			name: "happy flow",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      poolName,
					Namespace: namespace,
				},
				Spec: hyperv1.NodePoolSpec{
					ClusterName: clusterName,
					Replicas:    nil,
					Config:      nil,
					Management:  hyperv1.NodePoolManagement{},
					AutoScaling: nil,
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.KubevirtPlatform,
						Kubevirt: generateKubevirtPlatform(
							memoryNPOption("5Gi"),
							coresNPOption(4),
							imageNPOption("testimage"),
							volumeNPOption("32Gi"),
						),
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
						BootstrapCheckSpec: capikubevirt.VirtualMachineBootstrapCheckSpec{CheckStrategy: "none"},
						VirtualMachineTemplate: *generateNodeTemplate(
							memoryTmpltOpt("5Gi"),
							cpuTmpltOpt(4),
							storageTmpltOpt("32Gi"),
						),
					},
				},
			},
		},
		{
			name: "happy flow - QoS CLass Guaranteed",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      poolName,
					Namespace: namespace,
				},
				Spec: hyperv1.NodePoolSpec{
					ClusterName: clusterName,
					Replicas:    nil,
					Config:      nil,
					Management:  hyperv1.NodePoolManagement{},
					AutoScaling: nil,
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.KubevirtPlatform,
						Kubevirt: generateKubevirtPlatform(
							memoryNPOption("5Gi"),
							coresNPOption(4),
							imageNPOption("testimage"),
							volumeNPOption("32Gi"),
							qosClassGuaranteedNPOption(),
						),
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
						BootstrapCheckSpec: capikubevirt.VirtualMachineBootstrapCheckSpec{CheckStrategy: "none"},
						VirtualMachineTemplate: *generateNodeTemplate(
							storageTmpltOpt("32Gi"),
							guaranteedResourcesOpt(4, "5Gi"),
						),
					},
				},
			},
		},
		{
			name: "NetworkInterfaceMultiQueue is Disable",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      poolName,
					Namespace: namespace,
				},
				Spec: hyperv1.NodePoolSpec{
					ClusterName: clusterName,
					Replicas:    nil,
					Config:      nil,
					Management:  hyperv1.NodePoolManagement{},
					AutoScaling: nil,
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.KubevirtPlatform,
						Kubevirt: generateKubevirtPlatform(
							memoryNPOption("5Gi"),
							coresNPOption(4),
							imageNPOption("testimage"),
							volumeNPOption("32Gi"),
							multiQueueNPOption(hyperv1.MultiQueueDisable),
						),
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
						BootstrapCheckSpec: capikubevirt.VirtualMachineBootstrapCheckSpec{CheckStrategy: "none"},
						VirtualMachineTemplate: *generateNodeTemplate(
							memoryTmpltOpt("5Gi"),
							cpuTmpltOpt(4),
							storageTmpltOpt("32Gi"),
						),
					},
				},
			},
		},
		{
			name: "NetworkInterfaceMultiQueue is Enabled",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      poolName,
					Namespace: namespace,
				},
				Spec: hyperv1.NodePoolSpec{
					ClusterName: clusterName,
					Replicas:    nil,
					Config:      nil,
					Management:  hyperv1.NodePoolManagement{},
					AutoScaling: nil,
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.KubevirtPlatform,
						Kubevirt: generateKubevirtPlatform(
							memoryNPOption("5Gi"),
							coresNPOption(4),
							imageNPOption("testimage"),
							volumeNPOption("32Gi"),
							multiQueueNPOption(hyperv1.MultiQueueEnable),
						),
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
						BootstrapCheckSpec: capikubevirt.VirtualMachineBootstrapCheckSpec{CheckStrategy: "none"},
						VirtualMachineTemplate: *generateNodeTemplate(
							memoryTmpltOpt("5Gi"),
							cpuTmpltOpt(4),
							storageTmpltOpt("32Gi"),
							networkInterfaceMultiQueueTmpltOpt(),
						),
					},
				},
			},
		},
		{
			name: "Additional networks are configured",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      poolName,
					Namespace: namespace,
				},
				Spec: hyperv1.NodePoolSpec{
					ClusterName: clusterName,
					Replicas:    nil,
					Config:      nil,
					Management:  hyperv1.NodePoolManagement{},
					AutoScaling: nil,
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.KubevirtPlatform,
						Kubevirt: generateKubevirtPlatform(
							memoryNPOption("5Gi"),
							coresNPOption(4),
							imageNPOption("testimage"),
							volumeNPOption("32Gi"),
							additionalNetworksNPOption([]hyperv1.KubevirtNetwork{
								{
									Name: "ns1/nad1",
								},
								{
									Name: "ns2/nad2",
								},
							}),
						),
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
						BootstrapCheckSpec: capikubevirt.VirtualMachineBootstrapCheckSpec{CheckStrategy: "none"},
						VirtualMachineTemplate: *generateNodeTemplate(
							memoryTmpltOpt("5Gi"),
							cpuTmpltOpt(4),
							storageTmpltOpt("32Gi"),
							interfacesTmpltOpt([]kubevirtv1.Interface{
								{
									Name: "default",
									InterfaceBindingMethod: kubevirtv1.InterfaceBindingMethod{
										Bridge: &kubevirtv1.InterfaceBridge{},
									},
								},
								{
									Name: "iface1_ns1-nad1",
									InterfaceBindingMethod: kubevirtv1.InterfaceBindingMethod{
										Bridge: &kubevirtv1.InterfaceBridge{},
									},
								},
								{
									Name: "iface2_ns2-nad2",
									InterfaceBindingMethod: kubevirtv1.InterfaceBindingMethod{
										Bridge: &kubevirtv1.InterfaceBridge{},
									},
								},
							}),
							networksTmpltOpt([]kubevirtv1.Network{
								{
									Name: "default",
									NetworkSource: kubevirtv1.NetworkSource{
										Pod: &kubevirtv1.PodNetwork{},
									},
								},
								{
									Name: "iface1_ns1-nad1",
									NetworkSource: kubevirtv1.NetworkSource{
										Multus: &kubevirtv1.MultusNetwork{
											NetworkName: "ns1/nad1",
										},
									},
								},
								{
									Name: "iface2_ns2-nad2",
									NetworkSource: kubevirtv1.NetworkSource{
										Multus: &kubevirtv1.MultusNetwork{
											NetworkName: "ns2/nad2",
										},
									},
								},
							}),
						),
					},
				},
			},
		},
		{
			name: "Additional networks are configured excluding default one",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      poolName,
					Namespace: namespace,
				},
				Spec: hyperv1.NodePoolSpec{
					ClusterName: clusterName,
					Replicas:    nil,
					Config:      nil,
					Management:  hyperv1.NodePoolManagement{},
					AutoScaling: nil,
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.KubevirtPlatform,
						Kubevirt: generateKubevirtPlatform(
							memoryNPOption("5Gi"),
							coresNPOption(4),
							imageNPOption("testimage"),
							volumeNPOption("32Gi"),
							attachDefaultNetworkNPOption(false),
							additionalNetworksNPOption([]hyperv1.KubevirtNetwork{
								{
									Name: "ns1/nad1",
								},
								{
									Name: "ns2/nad2",
								},
							}),
						),
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
						BootstrapCheckSpec: capikubevirt.VirtualMachineBootstrapCheckSpec{CheckStrategy: "none"},
						VirtualMachineTemplate: *generateNodeTemplate(
							memoryTmpltOpt("5Gi"),
							cpuTmpltOpt(4),
							storageTmpltOpt("32Gi"),
							interfacesTmpltOpt([]kubevirtv1.Interface{
								{
									Name: "iface1_ns1-nad1",
									InterfaceBindingMethod: kubevirtv1.InterfaceBindingMethod{
										Bridge: &kubevirtv1.InterfaceBridge{},
									},
								},
								{
									Name: "iface2_ns2-nad2",
									InterfaceBindingMethod: kubevirtv1.InterfaceBindingMethod{
										Bridge: &kubevirtv1.InterfaceBridge{},
									},
								},
							}),
							networksTmpltOpt([]kubevirtv1.Network{
								{
									Name: "iface1_ns1-nad1",
									NetworkSource: kubevirtv1.NetworkSource{
										Multus: &kubevirtv1.MultusNetwork{
											NetworkName: "ns1/nad1",
										},
									},
								},
								{
									Name: "iface2_ns2-nad2",
									NetworkSource: kubevirtv1.NetworkSource{
										Multus: &kubevirtv1.MultusNetwork{
											NetworkName: "ns2/nad2",
										},
									},
								},
							}),
						),
					},
				},
			},
		},
		{
			name: "Excluding default network with additional ones should fail validation",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      poolName,
					Namespace: namespace,
				},
				Spec: hyperv1.NodePoolSpec{
					ClusterName: clusterName,
					Replicas:    nil,
					Config:      nil,
					Management:  hyperv1.NodePoolManagement{},
					AutoScaling: nil,
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.KubevirtPlatform,
						Kubevirt: generateKubevirtPlatform(
							memoryNPOption("5Gi"),
							coresNPOption(4),
							imageNPOption("testimage"),
							volumeNPOption("32Gi"),
							attachDefaultNetworkNPOption(false),
						),
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
			expectedValidationError: "default network cannot be disabled when no additional networks are configured",
		},
		{
			name: "Host Devices are configured properly",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      poolName,
					Namespace: namespace,
				},
				Spec: hyperv1.NodePoolSpec{
					ClusterName: clusterName,
					Replicas:    nil,
					Config:      nil,
					Management:  hyperv1.NodePoolManagement{},
					AutoScaling: nil,
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.KubevirtPlatform,
						Kubevirt: generateKubevirtPlatform(
							memoryNPOption("5Gi"),
							coresNPOption(4),
							imageNPOption("testimage"),
							volumeNPOption("32Gi"),
							hostDevicesOption([]hyperv1.KubevirtHostDevice{
								{
									DeviceName: "example.com/my-fast-gpu",
									Count:      2,
								},
							}),
						),
					},
					Release: hyperv1.Release{},
				},
			},
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-hostedcluster-gpu",
					Namespace: "clusters",
				},
				Spec: hyperv1.HostedClusterSpec{
					InfraID: "1234",
				},
			},

			expected: &capikubevirt.KubevirtMachineTemplateSpec{
				Template: capikubevirt.KubevirtMachineTemplateResource{
					Spec: capikubevirt.KubevirtMachineSpec{
						BootstrapCheckSpec: capikubevirt.VirtualMachineBootstrapCheckSpec{CheckStrategy: "none"},
						VirtualMachineTemplate: *generateNodeTemplate(
							memoryTmpltOpt("5Gi"),
							cpuTmpltOpt(4),
							storageTmpltOpt("32Gi"),
							hostDevicesTmpltOpt([]kubevirtv1.HostDevice{
								{
									Name:       "hostdevice-1",
									DeviceName: "example.com/my-fast-gpu",
								},
								{
									Name:       "hostdevice-2",
									DeviceName: "example.com/my-fast-gpu",
								},
							}),
						),
					},
				},
			},
		},
		{
			name: "Host Devices count has an invalid value",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      poolName,
					Namespace: namespace,
				},
				Spec: hyperv1.NodePoolSpec{
					ClusterName: clusterName,
					Replicas:    nil,
					Config:      nil,
					Management:  hyperv1.NodePoolManagement{},
					AutoScaling: nil,
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.KubevirtPlatform,
						Kubevirt: generateKubevirtPlatform(
							memoryNPOption("5Gi"),
							coresNPOption(4),
							imageNPOption("testimage"),
							volumeNPOption("32Gi"),
							hostDevicesOption([]hyperv1.KubevirtHostDevice{
								{
									DeviceName: "example.com/my-fast-gpu",
									Count:      -7,
								},
							}),
						),
					},
					Release: hyperv1.Release{},
				},
			},
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-hostedcluster-gpu",
					Namespace: "clusters",
				},
				Spec: hyperv1.HostedClusterSpec{
					InfraID: "1234",
				},
			},
			expectedValidationError: "host device count must be greater than or equal to 1. received: -7",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			if tc.expectedValidationError != "" {
				g.Expect(PlatformValidation(tc.nodePool)).To(MatchError(tc.expectedValidationError))
				return
			}
			g.Expect(PlatformValidation(tc.nodePool)).To(Succeed())

			np := &hyperv1.NodePool{
				Status: hyperv1.NodePoolStatus{
					Platform: &hyperv1.NodePoolPlatformStatus{
						KubeVirt: &hyperv1.KubeVirtNodePoolStatus{
							CacheName: bootImageNamePrefix + "12345",
						},
					},
				},
			}

			bootImage := newCachedBootImage(bootImageName, imageHash, hostedClusterNamespace, false, np)
			result, err := MachineTemplateSpec(tc.nodePool, tc.hcluster, &releaseinfo.ReleaseImage{}, bootImage)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(result).To(Equal(tc.expected), "Comparison failed\n%v", cmp.Diff(tc.expected, result))
		})
	}
}

func TestCacheImage(t *testing.T) {
	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      poolName,
			Namespace: namespace,
		},
		Spec: hyperv1.NodePoolSpec{
			ClusterName: clusterName,
			Replicas:    nil,
			Config:      nil,
			Management:  hyperv1.NodePoolManagement{},
			AutoScaling: nil,
			Platform: hyperv1.NodePoolPlatform{
				Type: hyperv1.KubevirtPlatform,
				Kubevirt: generateKubevirtPlatform(
					memoryNPOption("5Gi"),
					coresNPOption(4),
					imageNPOption("testimage"),
					volumeNPOption("32Gi"),
				),
			},
			Release: hyperv1.Release{},
		},
	}

	testCases := []struct {
		name              string
		nodePool          *hyperv1.NodePool
		existingResources []client.Object
		asserFunc         func(Gomega, []v1beta1.DataVolume, string, *cachedBootImage)
		errExpected       bool
		dvNamePrefix      string
	}{
		{
			name:         "happy flow - no existing PVC",
			nodePool:     nodePool,
			errExpected:  false,
			dvNamePrefix: bootImageNamePrefix,
			asserFunc:    assertDV,
		},
		{
			name:        "happy flow - PVC already exists",
			nodePool:    nodePool,
			errExpected: false,
			existingResources: []client.Object{
				&v1beta1.DataVolume{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: bootImageNamePrefix,
						Name:         bootImageNamePrefix + "already-exists",
						Namespace:    hostedClusterNamespace,
						Annotations: map[string]string{
							bootImageDVAnnotationHash: imageHash,
						},
						Labels: map[string]string{
							bootImageDVLabelRoleName: bootImageDVLabelRoleValue,
							bootImageDVLabelUID:      infraId,
						},
					},
				},
			},
			dvNamePrefix: bootImageNamePrefix + "already-exists",
			asserFunc:    assertDV,
		},
		{
			name:        "cleanup - different hash",
			nodePool:    nodePool,
			errExpected: false,
			existingResources: []client.Object{
				&v1beta1.DataVolume{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: bootImageNamePrefix,
						Name:         bootImageNamePrefix + "another-one",
						Namespace:    hostedClusterNamespace,
						Annotations: map[string]string{
							bootImageDVAnnotationHash: "otherImageHash",
							bootImageDVLabelUID:       infraId,
						},
						Labels: map[string]string{
							bootImageDVLabelRoleName: bootImageDVLabelRoleValue,
							bootImageDVLabelUID:      infraId,
						},
					},
				},
			},
			dvNamePrefix: bootImageNamePrefix,
			asserFunc:    assertDV,
		},
		{
			name:        "cleanup - different cluster - should not clean",
			nodePool:    nodePool,
			errExpected: false,
			existingResources: []client.Object{
				&v1beta1.DataVolume{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: bootImageNamePrefix,
						Name:         bootImageNamePrefix + "another-one",
						Namespace:    hostedClusterNamespace,
						Annotations: map[string]string{
							bootImageDVAnnotationHash: imageHash,
							bootImageDVLabelUID:       "not-the-same-cluster",
						},
						Labels: map[string]string{
							bootImageDVLabelRoleName: bootImageDVLabelRoleValue,
						},
					},
				},
			},
			dvNamePrefix: bootImageNamePrefix,
			asserFunc: func(g Gomega, dvs []v1beta1.DataVolume, expectedDVNamePrefix string, bootImage *cachedBootImage) {
				g.ExpectWithOffset(1, dvs).Should(HaveLen(2), "should not clear the other DV")
				for _, dv := range dvs {
					if dv.Name != bootImageNamePrefix+"another-one" {
						g.ExpectWithOffset(1, dv.Name).Should(HavePrefix(expectedDVNamePrefix))
						g.ExpectWithOffset(1, bootImage.dvName).Should(Equal(dv.Name), "failed to set the dvName filed in the cacheImage object")
					}
				}
			},
		},
	}

	ctx := logr.NewContext(t.Context(), zapr.NewLogger(zaptest.NewLogger(t)))
	for _, tc := range testCases {
		t.Run(tc.name, func(tst *testing.T) {
			g := NewWithT(tst)
			scheme := runtime.NewScheme()
			_ = corev1.AddToScheme(scheme)
			_ = v1beta1.AddToScheme(scheme)
			cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tc.existingResources...).Build()

			bootImage := newCachedBootImage(bootImageName, imageHash, hostedClusterNamespace, false, nil)
			err := bootImage.CacheImage(ctx, cl, tc.nodePool, infraId)

			if tc.errExpected != (err != nil) {
				if tc.errExpected {
					g.Fail("should return error, but it didn't")
				} else {
					g.Fail(fmt.Sprintf(`should not return error, but it did; the error is: "%v"`, err))
				}
			}

			dvs := v1beta1.DataVolumeList{}
			g.Expect(cl.List(ctx, &dvs)).To(Succeed())
			tc.asserFunc(g, dvs.Items, tc.dvNamePrefix, bootImage)
		})
	}
}

func TestJsonPatch(t *testing.T) {
	testCases := []struct {
		name     string
		nodePool *hyperv1.NodePool
		hcluster *hyperv1.HostedCluster
		expected *capikubevirt.KubevirtMachineTemplateSpec
	}{
		{
			name: "single json patch in the nodepool",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      poolName,
					Namespace: namespace,
					Annotations: map[string]string{
						hyperv1.JSONPatchAnnotation: `[{"op": "add","path": "/spec/template/spec/networks/-","value": {"name": "secondary", "multus": {"networkName": "mynetwork"}}}]`,
					},
				},
				Spec: hyperv1.NodePoolSpec{
					ClusterName: clusterName,
					Replicas:    nil,
					Config:      nil,
					Management:  hyperv1.NodePoolManagement{},
					AutoScaling: nil,
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.KubevirtPlatform,
						Kubevirt: generateKubevirtPlatform(
							memoryNPOption("5Gi"),
							coresNPOption(4),
							imageNPOption("testimage"),
							volumeNPOption("32Gi"),
						),
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
						BootstrapCheckSpec: capikubevirt.VirtualMachineBootstrapCheckSpec{CheckStrategy: "none"},
						VirtualMachineTemplate: *generateNodeTemplate(
							memoryTmpltOpt("5Gi"),
							cpuTmpltOpt(4),
							storageTmpltOpt("32Gi"),
							addNetworkOpt(kubevirtv1.Network{Name: "secondary", NetworkSource: kubevirtv1.NetworkSource{Multus: &kubevirtv1.MultusNetwork{NetworkName: "mynetwork"}}}),
						),
					},
				},
			},
		},
		{
			name: "several json patches in the nodepool",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      poolName,
					Namespace: namespace,
					Annotations: map[string]string{
						hyperv1.JSONPatchAnnotation: `[
                            {
                                "op": "add",
                                "path": "/spec/template/spec/networks/-",
                                "value": {"name": "secondary", "multus": {"networkName": "mynetwork"}}
                            },
                            {
                                "op": "replace",
                                "path": "/spec/template/spec/domain/cpu/cores",
                                "value": 5
                            }
                        ]`,
					},
				},
				Spec: hyperv1.NodePoolSpec{
					ClusterName: clusterName,
					Replicas:    nil,
					Config:      nil,
					Management:  hyperv1.NodePoolManagement{},
					AutoScaling: nil,
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.KubevirtPlatform,
						Kubevirt: generateKubevirtPlatform(
							memoryNPOption("5Gi"),
							coresNPOption(4),
							imageNPOption("testimage"),
							volumeNPOption("32Gi"),
						),
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
						BootstrapCheckSpec: capikubevirt.VirtualMachineBootstrapCheckSpec{CheckStrategy: "none"},
						VirtualMachineTemplate: *generateNodeTemplate(
							memoryTmpltOpt("5Gi"),
							cpuTmpltOpt(5),
							storageTmpltOpt("32Gi"),
							addNetworkOpt(kubevirtv1.Network{Name: "secondary", NetworkSource: kubevirtv1.NetworkSource{Multus: &kubevirtv1.MultusNetwork{NetworkName: "mynetwork"}}}),
						),
					},
				},
			},
		},
		{
			name: "single json patch in the hosted cluster",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      poolName,
					Namespace: namespace,
				},
				Spec: hyperv1.NodePoolSpec{
					ClusterName: clusterName,
					Replicas:    nil,
					Config:      nil,
					Management:  hyperv1.NodePoolManagement{},
					AutoScaling: nil,
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.KubevirtPlatform,
						Kubevirt: generateKubevirtPlatform(
							memoryNPOption("5Gi"),
							coresNPOption(4),
							imageNPOption("testimage"),
							volumeNPOption("32Gi"),
						),
					},
					Release: hyperv1.Release{},
				},
			},
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-hostedcluster",
					Namespace: "clusters",
					Annotations: map[string]string{
						hyperv1.JSONPatchAnnotation: `[{"op": "add","path": "/spec/template/spec/networks/1","value": {"name": "secondary", "multus": {"networkName": "mynetwork"}}}]`,
					},
				},
				Spec: hyperv1.HostedClusterSpec{
					InfraID: "1234",
				},
			},

			expected: &capikubevirt.KubevirtMachineTemplateSpec{
				Template: capikubevirt.KubevirtMachineTemplateResource{
					Spec: capikubevirt.KubevirtMachineSpec{
						BootstrapCheckSpec: capikubevirt.VirtualMachineBootstrapCheckSpec{CheckStrategy: "none"},
						VirtualMachineTemplate: *generateNodeTemplate(
							memoryTmpltOpt("5Gi"),
							cpuTmpltOpt(4),
							storageTmpltOpt("32Gi"),
							addNetworkOpt(kubevirtv1.Network{Name: "secondary", NetworkSource: kubevirtv1.NetworkSource{Multus: &kubevirtv1.MultusNetwork{NetworkName: "mynetwork"}}}),
						),
					},
				},
			},
		},
		{
			name: "several json patches in the hosted cluster",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      poolName,
					Namespace: namespace,
				},
				Spec: hyperv1.NodePoolSpec{
					ClusterName: clusterName,
					Replicas:    nil,
					Config:      nil,
					Management:  hyperv1.NodePoolManagement{},
					AutoScaling: nil,
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.KubevirtPlatform,
						Kubevirt: generateKubevirtPlatform(
							memoryNPOption("5Gi"),
							coresNPOption(4),
							imageNPOption("testimage"),
							volumeNPOption("32Gi"),
						),
					},
					Release: hyperv1.Release{},
				},
			},
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-hostedcluster",
					Namespace: "clusters",
					Annotations: map[string]string{
						hyperv1.JSONPatchAnnotation: `[
                            {
                                "op": "add",
                                "path": "/spec/template/spec/networks/-",
                                "value": {"name": "secondary", "multus": {"networkName": "mynetwork"}}
                            },
                            {
                                "op": "replace",
                                "path": "/spec/template/spec/domain/cpu/cores",
                                "value": 5
                            }
                        ]`,
					},
				},
				Spec: hyperv1.HostedClusterSpec{
					InfraID: "1234",
				},
			},

			expected: &capikubevirt.KubevirtMachineTemplateSpec{
				Template: capikubevirt.KubevirtMachineTemplateResource{
					Spec: capikubevirt.KubevirtMachineSpec{
						BootstrapCheckSpec: capikubevirt.VirtualMachineBootstrapCheckSpec{CheckStrategy: "none"},
						VirtualMachineTemplate: *generateNodeTemplate(
							memoryTmpltOpt("5Gi"),
							cpuTmpltOpt(5),
							storageTmpltOpt("32Gi"),
							addNetworkOpt(kubevirtv1.Network{Name: "secondary", NetworkSource: kubevirtv1.NetworkSource{Multus: &kubevirtv1.MultusNetwork{NetworkName: "mynetwork"}}}),
						),
					},
				},
			},
		},
		{
			name: "json patches both in the hosted cluster and the nodepool",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      poolName,
					Namespace: namespace,
					Annotations: map[string]string{
						hyperv1.JSONPatchAnnotation: `[
                            {
                                "op": "replace",
                                "path": "/spec/template/spec/domain/cpu/cores",
                                "value": 5
                            }
                        ]`,
					},
				},
				Spec: hyperv1.NodePoolSpec{
					ClusterName: clusterName,
					Replicas:    nil,
					Config:      nil,
					Management:  hyperv1.NodePoolManagement{},
					AutoScaling: nil,
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.KubevirtPlatform,
						Kubevirt: generateKubevirtPlatform(
							memoryNPOption("5Gi"),
							coresNPOption(4),
							imageNPOption("testimage"),
							volumeNPOption("32Gi"),
						),
					},
					Release: hyperv1.Release{},
				},
			},
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-hostedcluster",
					Namespace: "clusters",
					Annotations: map[string]string{
						hyperv1.JSONPatchAnnotation: `[
                            {
                                "op": "add",
                                "path": "/spec/template/spec/networks/-",
                                "value": {"name": "secondary", "multus": {"networkName": "mynetwork"}}
                            }
                        ]`,
					},
				},
				Spec: hyperv1.HostedClusterSpec{
					InfraID: "1234",
				},
			},

			expected: &capikubevirt.KubevirtMachineTemplateSpec{
				Template: capikubevirt.KubevirtMachineTemplateResource{
					Spec: capikubevirt.KubevirtMachineSpec{
						BootstrapCheckSpec: capikubevirt.VirtualMachineBootstrapCheckSpec{CheckStrategy: "none"},
						VirtualMachineTemplate: *generateNodeTemplate(
							memoryTmpltOpt("5Gi"),
							cpuTmpltOpt(5),
							storageTmpltOpt("32Gi"),
							addNetworkOpt(kubevirtv1.Network{Name: "secondary", NetworkSource: kubevirtv1.NetworkSource{Multus: &kubevirtv1.MultusNetwork{NetworkName: "mynetwork"}}}),
						),
					},
				},
			},
		},
		{
			name: "json patches in the hosted cluster, overrode by the one in the nodepool",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      poolName,
					Namespace: namespace,
					Annotations: map[string]string{
						hyperv1.JSONPatchAnnotation: `[
                            {
                                "op": "replace",
                                "path": "/spec/template/spec/domain/cpu/cores",
                                "value": 5
                            }
                        ]`,
					},
				},
				Spec: hyperv1.NodePoolSpec{
					ClusterName: clusterName,
					Replicas:    nil,
					Config:      nil,
					Management:  hyperv1.NodePoolManagement{},
					AutoScaling: nil,
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.KubevirtPlatform,
						Kubevirt: generateKubevirtPlatform(
							memoryNPOption("5Gi"),
							coresNPOption(4),
							imageNPOption("testimage"),
							volumeNPOption("32Gi"),
						),
					},
					Release: hyperv1.Release{},
				},
			},
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-hostedcluster",
					Namespace: "clusters",
					Annotations: map[string]string{
						hyperv1.JSONPatchAnnotation: `[
                            {
                                "op": "replace",
                                "path": "/spec/template/spec/domain/cpu/cores",
                                "value": 6
                            }
                        ]`,
					},
				},
				Spec: hyperv1.HostedClusterSpec{
					InfraID: "1234",
				},
			},

			expected: &capikubevirt.KubevirtMachineTemplateSpec{
				Template: capikubevirt.KubevirtMachineTemplateResource{
					Spec: capikubevirt.KubevirtMachineSpec{
						BootstrapCheckSpec: capikubevirt.VirtualMachineBootstrapCheckSpec{CheckStrategy: "none"},
						VirtualMachineTemplate: *generateNodeTemplate(
							memoryTmpltOpt("5Gi"),
							cpuTmpltOpt(5),
							storageTmpltOpt("32Gi"),
						),
					},
				},
			},
		},
		{
			name: "remove annotation in the nodepool",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      poolName,
					Namespace: namespace,
					Annotations: map[string]string{
						hyperv1.JSONPatchAnnotation: `[{"op": "remove","path": "/spec/template/metadata/annotations/kubevirt.io~1allow-pod-bridge-network-live-migration"}]`,
					},
				},
				Spec: hyperv1.NodePoolSpec{
					ClusterName: clusterName,
					Replicas:    nil,
					Config:      nil,
					Management:  hyperv1.NodePoolManagement{},
					AutoScaling: nil,
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.KubevirtPlatform,
						Kubevirt: generateKubevirtPlatform(
							memoryNPOption("5Gi"),
							coresNPOption(4),
							imageNPOption("testimage"),
							volumeNPOption("32Gi"),
						),
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
						BootstrapCheckSpec: capikubevirt.VirtualMachineBootstrapCheckSpec{CheckStrategy: "none"},
						VirtualMachineTemplate: *generateNodeTemplate(
							memoryTmpltOpt("5Gi"),
							cpuTmpltOpt(4),
							storageTmpltOpt("32Gi"),
							annotationsTmpltOpt(map[string]string{
								suppconfig.PodSafeToEvictLocalVolumesKey: strings.Join(LocalStorageVolumes, ","),
							}),
						),
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			g.Expect(PlatformValidation(tc.nodePool)).To(Succeed())

			bootImage := newCachedBootImage(bootImageName, imageHash, hostedClusterNamespace, false, nil)
			bootImage.dvName = bootImageNamePrefix + "12345"
			result, err := MachineTemplateSpec(tc.nodePool, tc.hcluster, &releaseinfo.ReleaseImage{}, bootImage)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(result).To(Equal(tc.expected), "Comparison failed\n%v", cmp.Diff(tc.expected, result))
		})
	}
}

func assertDV(g Gomega, dvs []v1beta1.DataVolume, expectedDVNamePrefix string, bootImage *cachedBootImage) {
	g.ExpectWithOffset(1, dvs).Should(HaveLen(1), "failed to read the DataVolume back; No matched DataVolume")
	g.ExpectWithOffset(1, dvs[0].Name).Should(HavePrefix(expectedDVNamePrefix))
	g.ExpectWithOffset(1, bootImage.dvName).Should(Equal(dvs[0].Name), "failed to set the dvName filed in the cacheImage object")
}

type nodePoolOption func(kvNodePool *hyperv1.KubevirtNodePoolPlatform)

func memoryNPOption(memory string) nodePoolOption {
	return func(kvNodePool *hyperv1.KubevirtNodePoolPlatform) {
		if kvNodePool.Compute == nil {
			kvNodePool.Compute = &hyperv1.KubevirtCompute{}
		}

		memoryQuantity := apiresource.MustParse(memory)
		kvNodePool.Compute.Memory = &memoryQuantity
	}
}

func coresNPOption(cores uint32) nodePoolOption {
	return func(kvNodePool *hyperv1.KubevirtNodePoolPlatform) {
		if kvNodePool.Compute == nil {
			kvNodePool.Compute = &hyperv1.KubevirtCompute{}
		}

		kvNodePool.Compute.Cores = &cores
	}
}

func qosClassGuaranteedNPOption() nodePoolOption {
	return func(kvNodePool *hyperv1.KubevirtNodePoolPlatform) {
		if kvNodePool.Compute == nil {
			kvNodePool.Compute = &hyperv1.KubevirtCompute{}
		}

		qosClassGuaranteed := hyperv1.QoSClassGuaranteed
		kvNodePool.Compute.QosClass = &qosClassGuaranteed
	}
}

func imageNPOption(image string) nodePoolOption {
	return func(kvNodePool *hyperv1.KubevirtNodePoolPlatform) {
		if kvNodePool.RootVolume == nil {
			kvNodePool.RootVolume = &hyperv1.KubevirtRootVolume{}
		}

		kvNodePool.RootVolume.Image = &hyperv1.KubevirtDiskImage{
			ContainerDiskImage: &image,
		}
	}
}

func volumeNPOption(volumeSize string) nodePoolOption {
	volumeSizeQuantity := apiresource.MustParse(volumeSize)

	return func(kvNodePool *hyperv1.KubevirtNodePoolPlatform) {
		if kvNodePool.RootVolume == nil {
			kvNodePool.RootVolume = &hyperv1.KubevirtRootVolume{}
		}

		kvNodePool.RootVolume.KubevirtVolume = hyperv1.KubevirtVolume{
			Type: hyperv1.KubevirtVolumeTypePersistent,
			Persistent: &hyperv1.KubevirtPersistentVolume{
				Size:         &volumeSizeQuantity,
				StorageClass: nil,
			},
		}
	}
}

func multiQueueNPOption(multiQueue hyperv1.MultiQueueSetting) nodePoolOption {
	return func(kvNodePool *hyperv1.KubevirtNodePoolPlatform) {
		kvNodePool.NetworkInterfaceMultiQueue = &multiQueue
	}
}

func additionalNetworksNPOption(additionalNetworks []hyperv1.KubevirtNetwork) nodePoolOption {
	return func(kvNodePool *hyperv1.KubevirtNodePoolPlatform) {
		kvNodePool.AdditionalNetworks = additionalNetworks
	}
}

func attachDefaultNetworkNPOption(attach bool) nodePoolOption {
	return func(kvNodePool *hyperv1.KubevirtNodePoolPlatform) {
		kvNodePool.AttachDefaultNetwork = &attach
	}
}

func hostDevicesOption(hostDevices []hyperv1.KubevirtHostDevice) nodePoolOption {
	return func(kvNodePool *hyperv1.KubevirtNodePoolPlatform) {
		kvNodePool.KubevirtHostDevices = hostDevices
	}
}

func generateKubevirtPlatform(options ...nodePoolOption) *hyperv1.KubevirtNodePoolPlatform {
	exampleTemplate := &hyperv1.KubevirtNodePoolPlatform{}

	for _, opt := range options {
		opt(exampleTemplate)
	}

	return exampleTemplate
}

type nodeTemplateOption func(template *capikubevirt.VirtualMachineTemplateSpec)

func cpuTmpltOpt(cores uint32) nodeTemplateOption {
	return func(template *capikubevirt.VirtualMachineTemplateSpec) {
		template.Spec.Template.Spec.Domain.CPU = &kubevirtv1.CPU{Cores: cores}
	}
}

func memoryTmpltOpt(memory string) nodeTemplateOption {
	guestQuantity := apiresource.MustParse(memory)

	return func(template *capikubevirt.VirtualMachineTemplateSpec) {
		template.Spec.Template.Spec.Domain.Memory = &kubevirtv1.Memory{Guest: &guestQuantity}
	}
}

func storageTmpltOpt(volumeSize string) nodeTemplateOption {
	storage := &v1beta1.StorageSpec{
		Resources: corev1.VolumeResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceStorage: apiresource.MustParse(volumeSize),
			},
		},
	}

	return func(template *capikubevirt.VirtualMachineTemplateSpec) {
		if len(template.Spec.DataVolumeTemplates) == 1 {
			template.Spec.DataVolumeTemplates[0].Spec.Storage = storage
		}
	}
}

func networkInterfaceMultiQueueTmpltOpt() nodeTemplateOption {
	return func(template *capikubevirt.VirtualMachineTemplateSpec) {
		template.Spec.Template.Spec.Domain.Devices.NetworkInterfaceMultiQueue = ptr.To(true)
	}
}

func hostDevicesTmpltOpt(hostDevices []kubevirtv1.HostDevice) nodeTemplateOption {
	return func(template *capikubevirt.VirtualMachineTemplateSpec) {
		template.Spec.Template.Spec.Domain.Devices.HostDevices = hostDevices
	}
}

func interfacesTmpltOpt(interfaces []kubevirtv1.Interface) nodeTemplateOption {
	return func(template *capikubevirt.VirtualMachineTemplateSpec) {
		template.Spec.Template.Spec.Domain.Devices.Interfaces = interfaces
	}
}
func networksTmpltOpt(networks []kubevirtv1.Network) nodeTemplateOption {
	return func(template *capikubevirt.VirtualMachineTemplateSpec) {
		template.Spec.Template.Spec.Networks = networks
	}
}

func guaranteedResourcesOpt(cores uint32, memory string) nodeTemplateOption {
	memReq := apiresource.MustParse(memory)
	coresReq := *apiresource.NewQuantity(int64(cores), apiresource.DecimalSI)

	return func(template *capikubevirt.VirtualMachineTemplateSpec) {
		if len(template.Spec.Template.Spec.Domain.Resources.Requests) == 0 {
			template.Spec.Template.Spec.Domain.Resources.Requests = make(corev1.ResourceList)
		}

		if len(template.Spec.Template.Spec.Domain.Resources.Limits) == 0 {
			template.Spec.Template.Spec.Domain.Resources.Limits = make(corev1.ResourceList)
		}

		template.Spec.Template.Spec.Domain.Resources.Requests[corev1.ResourceMemory] = memReq
		template.Spec.Template.Spec.Domain.Resources.Limits[corev1.ResourceMemory] = memReq

		template.Spec.Template.Spec.Domain.Resources.Requests[corev1.ResourceCPU] = coresReq
		template.Spec.Template.Spec.Domain.Resources.Limits[corev1.ResourceCPU] = coresReq
	}
}

func addNetworkOpt(nw kubevirtv1.Network) nodeTemplateOption {
	return func(template *capikubevirt.VirtualMachineTemplateSpec) {
		template.Spec.Template.Spec.Networks = append(template.Spec.Template.Spec.Networks, nw)
	}
}

func annotationsTmpltOpt(annotations map[string]string) nodeTemplateOption {
	return func(template *capikubevirt.VirtualMachineTemplateSpec) {
		template.Spec.Template.ObjectMeta.Annotations = annotations
	}
}

func generateNodeTemplate(options ...nodeTemplateOption) *capikubevirt.VirtualMachineTemplateSpec {
	runAlways := kubevirtv1.RunStrategyAlways

	template := &capikubevirt.VirtualMachineTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				hyperv1.NodePoolNameLabel: "my-pool",
				hyperv1.InfraIDLabel:      "1234",
			},
		},
		Spec: kubevirtv1.VirtualMachineSpec{
			RunStrategy: &runAlways,
			DataVolumeTemplates: []kubevirtv1.DataVolumeTemplateSpec{
				{
					TypeMeta: metav1.TypeMeta{},
					ObjectMeta: metav1.ObjectMeta{
						Name: "rhcos",
						Labels: map[string]string{
							hyperv1.IsKubeVirtRHCOSVolumeLabelName: "true",
						},
					},
					Spec: v1beta1.DataVolumeSpec{
						Source: &v1beta1.DataVolumeSource{
							PVC: &v1beta1.DataVolumeSourcePVC{
								Namespace: hostedClusterNamespace,
								Name:      bootImageNamePrefix + "12345",
							},
						},
					},
				},
			},
			Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						hyperv1.NodePoolNameLabel: "my-pool",
						hyperv1.InfraIDLabel:      "1234",
					},
					Annotations: map[string]string{
						suppconfig.PodSafeToEvictLocalVolumesKey:              strings.Join(LocalStorageVolumes, ","),
						"kubevirt.io/allow-pod-bridge-network-live-migration": "",
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
													Key:      hyperv1.NodePoolNameLabel,
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

	for _, opt := range options {
		opt(template)
	}

	return template
}
