package nodepool

import (
	"errors"
	"fmt"
	"strconv"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/nodepool/instancetype"
	"github.com/openshift/hypershift/support/api"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"

	capiaws "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capiazure "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func testScaleFromZeroCapacityTransitions(t *testing.T) {
	t.Helper()
	for _, platform := range []hyperv1.PlatformType{hyperv1.AWSPlatform, hyperv1.AzurePlatform} {
		for _, upgradeType := range []hyperv1.UpgradeType{hyperv1.UpgradeTypeReplace, hyperv1.UpgradeTypeInPlace} {
			t.Run(fmt.Sprintf("When %s uses %s, it should migrate and clean persisted capacity independently of scheduling", platform, upgradeType), func(t *testing.T) {
				g := NewWithT(t)
				nodePool := &hyperv1.NodePool{
					ObjectMeta: metav1.ObjectMeta{Name: "workers", Namespace: "clusters", Annotations: map[string]string{
						nodePoolAnnotationCurrentConfig: "unchanged-config", nodePoolAnnotationCurrentConfigVersion: "unchanged-version",
					}},
					Spec: hyperv1.NodePoolSpec{
						Arch: "arm64", Platform: hyperv1.NodePoolPlatform{Type: platform},
						Management:  hyperv1.NodePoolManagement{UpgradeType: upgradeType},
						AutoScaling: &hyperv1.NodePoolAutoScaling{Min: ptr.To[int32](0), Max: 2},
						NodeLabels:  map[string]string{"workload": "gpu", "kubernetes.io/arch": "wrong"},
						Taints:      []hyperv1.Taint{{Key: "dedicated", Value: "gpu", Effect: corev1.TaintEffectNoSchedule}},
					},
				}
				originalNodePool := nodePool.DeepCopy()
				templateMetadata := metav1.ObjectMeta{Name: "existing-template", Namespace: "clusters-guest"}
				instanceType := "m5.xlarge"
				var template client.Object = &capiaws.AWSMachineTemplate{ObjectMeta: templateMetadata, Spec: capiaws.AWSMachineTemplateSpec{
					Template: capiaws.AWSMachineTemplateResource{Spec: capiaws.AWSMachineSpec{InstanceType: instanceType}},
				}}
				if platform == hyperv1.AzurePlatform {
					instanceType = "Standard_D4s_v5"
					template = &capiazure.AzureMachineTemplate{ObjectMeta: templateMetadata, Spec: capiazure.AzureMachineTemplateSpec{
						Template: capiazure.AzureMachineTemplateResource{Spec: capiazure.AzureMachineSpec{VMSize: instanceType}},
					}}
				}
				objectMetadata := metav1.ObjectMeta{Name: nodePool.Name, Namespace: templateMetadata.Namespace, Annotations: map[string]string{
					"machine.openshift.io/vCPU": "1", "machine.openshift.io/memoryMb": "1024", "machine.openshift.io/GPU": "2",
					"custom.io/keep": "preserved", "capacity.cluster-autoscaler.kubernetes.io/ephemeral-disk": "100Gi",
				}}
				machineTemplate := capiv1.MachineTemplateSpec{Spec: capiv1.MachineSpec{
					Version: "4.22.15", InfrastructureRef: capiv1.ContractVersionedObjectReference{Name: template.GetName()},
					Bootstrap: capiv1.Bootstrap{DataSecretName: ptr.To("existing-user-data")},
				}}
				var object client.Object = &capiv1.MachineDeployment{ObjectMeta: objectMetadata, Spec: capiv1.MachineDeploymentSpec{Template: machineTemplate, Replicas: ptr.To[int32](0)}}
				if upgradeType == hyperv1.UpgradeTypeInPlace {
					object = &capiv1.MachineSet{ObjectMeta: objectMetadata, Spec: capiv1.MachineSetSpec{Template: machineTemplate, Replicas: ptr.To[int32](0)}}
				}
				storedClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(object, template, nodePool.DeepCopy()).
					WithStatusSubresource(&capiaws.AWSMachineTemplate{}, &capiazure.AzureMachineTemplate{}).Build()
				patchClient := &schedulingMetadataClient{Client: storedClient}
				capi := &CAPI{Token: &Token{ConfigGenerator: &ConfigGenerator{Client: patchClient, nodePool: nodePool, controlplaneNamespace: templateMetadata.Namespace}}}
				provider := &mockProvider{info: &instancetype.InstanceTypeInfo{VCPU: 4, MemoryMb: 16384, GPU: 1, CPUArchitecture: "arm64"}}
				reconciler := &NodePoolReconciler{InstanceTypeProvider: provider, ScaleFromZeroPlatform: platform}
				actual := object.DeepCopyObject().(client.Object)
				resourceKeys := []string{
					"machine.openshift.io/vCPU", "machine.openshift.io/memoryMb", "machine.openshift.io/GPU",
					"capacity.cluster-autoscaler.kubernetes.io/cpu", "capacity.cluster-autoscaler.kubernetes.io/memory",
					"capacity.cluster-autoscaler.kubernetes.io/gpu-count", "capacity.cluster-autoscaler.kubernetes.io/gpu-type",
				}
				for _, step := range []string{"legacy migration", "non-GPU", "orphan GPU type", "GPU restored", "native capacity", "native removed", "provider removed", "provider mismatched", "provider error", "patch error", "conflict"} {
					t.Run("When "+step+" occurs, it should preserve the capacity contract and object identity", func(t *testing.T) {
						g := NewWithT(t)
						provider.err = nil
						patchClient.patchErr = nil
						reconciler.InstanceTypeProvider = provider
						reconciler.ScaleFromZeroPlatform = platform
						if step == "non-GPU" || step == "orphan GPU type" {
							provider.info.GPU = 0
						} else {
							provider.info.GPU = 1
						}
						if step == "native capacity" || step == "native removed" {
							var capacity corev1.ResourceList
							if step == "native capacity" {
								capacity = corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("8"), corev1.ResourceMemory: resource.MustParse("32Gi")}
							}
							g.Expect(storedClient.Get(t.Context(), client.ObjectKeyFromObject(template), template)).To(Succeed())
							switch template := template.(type) {
							case *capiaws.AWSMachineTemplate:
								template.Status.Capacity = capacity
							case *capiazure.AzureMachineTemplate:
								template.Status.Capacity = capacity
							}
							g.Expect(storedClient.Status().Update(t.Context(), template)).To(Succeed())
						}
						if step == "provider removed" {
							reconciler.InstanceTypeProvider = nil
						}
						if step == "provider mismatched" {
							reconciler.ScaleFromZeroPlatform = hyperv1.OpenStackPlatform
						}
						if step == "provider mismatched" || step == "orphan GPU type" {
							g.Expect(storedClient.Get(t.Context(), client.ObjectKeyFromObject(object), actual)).To(Succeed())
							annotations := actual.GetAnnotations()
							if step == "provider mismatched" {
								for _, key := range resourceKeys {
									annotations[key] = "stale"
								}
							} else {
								annotations["capacity.cluster-autoscaler.kubernetes.io/gpu-type"] = "nvidia.com/gpu"
								delete(annotations, "capacity.cluster-autoscaler.kubernetes.io/gpu-count")
								delete(annotations, "machine.openshift.io/GPU")
							}
							actual.SetAnnotations(annotations)
							g.Expect(storedClient.Update(t.Context(), actual)).To(Succeed())
						}
						var expectedError error
						switch step {
						case "provider error":
							expectedError = fmt.Errorf("instance discovery unavailable")
							provider.err = expectedError
						case "patch error":
							expectedError = fmt.Errorf("patch unavailable")
							patchClient.patchErr = expectedError
						case "conflict":
							expectedError = apierrors.NewConflict(schema.GroupResource{Group: capiv1.GroupVersion.Group, Resource: "machinesets"}, object.GetName(), fmt.Errorf("resource version changed"))
							patchClient.patchErr = expectedError
						}
						g.Expect(storedClient.Get(t.Context(), client.ObjectKeyFromObject(object), actual)).To(Succeed())
						before := actual.DeepCopyObject().(client.Object)
						patchCount := len(patchClient.patches)
						providerCalls := len(provider.instanceTypes)
						err := reconciler.reconcileScaleFromZeroAnnotations(t.Context(), nodePool, capi)
						if expectedError != nil {
							g.Expect(err).To(MatchError(ContainSubstring(expectedError.Error())))
							g.Expect(errors.Is(err, expectedError)).To(BeTrue())
						} else {
							g.Expect(err).NotTo(HaveOccurred())
						}
						g.Expect(storedClient.Get(t.Context(), client.ObjectKeyFromObject(object), actual)).To(Succeed())
						if expectedError != nil {
							g.Expect(actual).To(Equal(before))
							return
						}
						annotations := actual.GetAnnotations()
						expectScaleFromZeroResourceAnnotations(t, annotations, step, resourceKeys, provider, providerCalls, instanceType)
						g.Expect(annotations).To(HaveKeyWithValue(labelsKey, "kubernetes.io/arch=arm64,workload=gpu"))
						g.Expect(annotations).To(HaveKeyWithValue(taintsKey, "dedicated=gpu:NoSchedule"))
						g.Expect(annotations).To(HaveKeyWithValue("custom.io/keep", "preserved"))
						g.Expect(annotations).To(HaveKeyWithValue("capacity.cluster-autoscaler.kubernetes.io/ephemeral-disk", "100Gi"))
						switch actual := actual.(type) {
						case *capiv1.MachineDeployment:
							g.Expect(actual.Spec).To(Equal(object.(*capiv1.MachineDeployment).Spec))
						case *capiv1.MachineSet:
							g.Expect(actual.Spec).To(Equal(object.(*capiv1.MachineSet).Spec))
						}
						for _, fields := range patchClient.patches[patchCount:] {
							g.Expect(fields).To(HaveLen(1))
							metadata := fields["metadata"].(map[string]interface{})
							g.Expect(metadata).To(HaveLen(2))
							g.Expect(metadata).To(HaveKeyWithValue("resourceVersion", before.GetResourceVersion()))
							g.Expect(metadata).To(HaveKey("annotations"))
						}
						patchCount = len(patchClient.patches)
						resourceVersion := actual.GetResourceVersion()
						g.Expect(reconciler.reconcileScaleFromZeroAnnotations(t.Context(), nodePool, capi)).To(Succeed())
						g.Expect(patchClient.patches).To(HaveLen(patchCount))
						g.Expect(storedClient.Get(t.Context(), client.ObjectKeyFromObject(object), actual)).To(Succeed())
						g.Expect(actual.GetResourceVersion()).To(Equal(resourceVersion))
					})
				}
				g.Expect(nodePool).To(Equal(originalNodePool))
				persistedNodePool := &hyperv1.NodePool{}
				g.Expect(storedClient.Get(t.Context(), client.ObjectKeyFromObject(nodePool), persistedNodePool)).To(Succeed())
				g.Expect(persistedNodePool.Spec).To(Equal(originalNodePool.Spec))
				g.Expect(persistedNodePool.Annotations).To(Equal(originalNodePool.Annotations))
			})
		}
	}
}

func expectScaleFromZeroResourceAnnotations(t *testing.T, annotations map[string]string, step string, resourceKeys []string, provider *mockProvider, providerCalls int, instanceType string) {
	t.Helper()
	g := NewWithT(t)
	if step == "native capacity" || step == "provider removed" || step == "provider mismatched" {
		for _, key := range resourceKeys {
			g.Expect(annotations).NotTo(HaveKey(key))
		}
		g.Expect(provider.instanceTypes).To(HaveLen(providerCalls))
		return
	}
	g.Expect(provider.instanceTypes[providerCalls:]).To(Equal([]string{instanceType}))
	cpu, parseErr := resource.ParseQuantity(annotations["capacity.cluster-autoscaler.kubernetes.io/cpu"])
	g.Expect(parseErr).NotTo(HaveOccurred())
	g.Expect(cpu.Cmp(resource.MustParse("4"))).To(BeZero())
	legacyCPU, parseErr := resource.ParseQuantity(annotations["machine.openshift.io/vCPU"])
	g.Expect(parseErr).NotTo(HaveOccurred())
	g.Expect(cpu.Cmp(legacyCPU)).To(BeZero())
	memory, parseErr := resource.ParseQuantity(annotations["capacity.cluster-autoscaler.kubernetes.io/memory"])
	g.Expect(parseErr).NotTo(HaveOccurred())
	g.Expect(annotations["capacity.cluster-autoscaler.kubernetes.io/memory"]).To(Equal("16384Mi"))
	legacyMiB, parseErr := strconv.ParseInt(annotations["machine.openshift.io/memoryMb"], 10, 64)
	g.Expect(parseErr).NotTo(HaveOccurred())
	g.Expect(memory.Value()).To(Equal(legacyMiB * 1024 * 1024))
	g.Expect(memory.Cmp(resource.MustParse("16Gi"))).To(BeZero())
	if provider.info.GPU == 0 {
		for _, key := range []string{resourceKeys[2], resourceKeys[5], resourceKeys[6]} {
			g.Expect(annotations).NotTo(HaveKey(key))
		}
	} else {
		gpu, parseErr := strconv.ParseInt(annotations["capacity.cluster-autoscaler.kubernetes.io/gpu-count"], 10, 64)
		g.Expect(parseErr).NotTo(HaveOccurred())
		g.Expect(gpu).To(Equal(int64(1)))
		g.Expect(annotations["machine.openshift.io/GPU"]).To(Equal("1"))
		g.Expect(annotations["capacity.cluster-autoscaler.kubernetes.io/gpu-type"]).To(Equal("nvidia.com/gpu"))
	}
}
