package util

import (
	"context"
	"fmt"
	"math"
	"strconv"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"

	capiaws "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capiazure "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func PrepareScaleFromZeroUpgrade(nodePool *hyperv1.NodePool) (*hyperv1.NodePool, error) {
	if nodePool == nil || nodePool.Spec.Replicas == nil || *nodePool.Spec.Replicas <= 0 || *nodePool.Spec.Replicas == math.MaxInt32 {
		return nil, fmt.Errorf("upgrade preparation requires a fixed positive replica count with room to scale")
	}
	prepared := nodePool.DeepCopy()
	prepared.Spec.AutoScaling = &hyperv1.NodePoolAutoScaling{Min: ptr.To(*nodePool.Spec.Replicas), Max: *nodePool.Spec.Replicas + 1}
	prepared.Spec.Replicas = nil
	return prepared, nil
}

func SnapshotScaleFromZeroRollout(ctx context.Context, managementClient, guestClient client.Client, namespace, version string, platform hyperv1.PlatformType) (map[string]interface{}, error) {
	snapshot := map[string]interface{}{}
	resources := []schema.GroupVersionKind{
		{Group: "cluster.x-k8s.io", Version: version, Kind: "MachineDeploymentList"},
		{Group: "cluster.x-k8s.io", Version: version, Kind: "MachineSetList"},
		{Group: "cluster.x-k8s.io", Version: version, Kind: "MachineList"},
	}
	switch platform {
	case hyperv1.AWSPlatform:
		resources = append(resources, schema.GroupVersionKind{Group: "infrastructure.cluster.x-k8s.io", Version: "v1beta2", Kind: "AWSMachineTemplateList"})
	case hyperv1.AzurePlatform:
		resources = append(resources, schema.GroupVersionKind{Group: "infrastructure.cluster.x-k8s.io", Version: "v1beta1", Kind: "AzureMachineTemplateList"})
	default:
		return nil, fmt.Errorf("unsupported scale-from-zero platform %s", platform)
	}
	for _, kind := range resources {
		objects := &unstructured.UnstructuredList{}
		objects.SetGroupVersionKind(kind)
		if err := managementClient.List(ctx, objects, client.InNamespace(namespace)); err != nil {
			return nil, fmt.Errorf("snapshot %s: %w", kind.Kind, err)
		}
		for _, object := range objects.Items {
			spec, found, err := unstructured.NestedMap(object.Object, "spec")
			if err != nil || !found {
				return nil, fmt.Errorf("missing or invalid spec for %s/%s", kind.Kind, object.GetName())
			}
			snapshot[kind.Kind+"/"+object.GetName()] = map[string]interface{}{"uid": string(object.GetUID()), "spec": spec}
		}
	}
	nodes := &corev1.NodeList{}
	if err := guestClient.List(ctx, nodes); err != nil {
		return nil, fmt.Errorf("snapshot nodes: %w", err)
	}
	if len(nodes.Items) == 0 {
		return nil, fmt.Errorf("upgrade fixture must have existing workers")
	}
	for _, node := range nodes.Items {
		ready := false
		for _, condition := range node.Status.Conditions {
			if condition.Type == corev1.NodeReady && condition.Status == corev1.ConditionTrue {
				ready = true
			}
		}
		if !ready {
			return nil, fmt.Errorf("worker %s is not ready", node.Name)
		}
		snapshot["Node/"+node.Name] = string(node.UID)
	}
	return snapshot, nil
}

func GetScaleFromZeroCapacity(ctx context.Context, managementClient client.Client, namespace string, nodePool *hyperv1.NodePool, version string) (client.Object, corev1.ResourceList, error) {
	key := client.ObjectKey{Name: nodePool.Name, Namespace: namespace}
	object := &unstructured.Unstructured{}
	kind := "MachineDeployment"
	if nodePool.Spec.Management.UpgradeType == hyperv1.UpgradeTypeInPlace {
		kind = "MachineSet"
	}
	object.SetGroupVersionKind(schema.GroupVersionKind{Group: "cluster.x-k8s.io", Version: version, Kind: kind})
	if err := managementClient.Get(ctx, key, object); err != nil {
		return nil, nil, err
	}
	templateName, _, err := unstructured.NestedString(object.Object, "spec", "template", "spec", "infrastructureRef", "name")
	if err != nil {
		return nil, nil, err
	}
	if templateName == "" {
		return nil, nil, fmt.Errorf("scalable object has no infrastructure reference")
	}
	key.Name = templateName
	switch nodePool.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		template := &capiaws.AWSMachineTemplate{}
		if err := managementClient.Get(ctx, key, template); err != nil {
			return nil, nil, err
		}
		return object, template.Status.Capacity, nil
	case hyperv1.AzurePlatform:
		template := &capiazure.AzureMachineTemplate{}
		if err := managementClient.Get(ctx, key, template); err != nil {
			return nil, nil, err
		}
		return object, template.Status.Capacity, nil
	default:
		return nil, nil, fmt.Errorf("unsupported scale-from-zero platform %s", nodePool.Spec.Platform.Type)
	}
}

func ValidateScaleFromZeroCapacity(annotations map[string]string, native corev1.ResourceList, source string) error {
	const prefix = "capacity.cluster-autoscaler.kubernetes.io/"
	if source != "native" && source != "annotations" {
		return fmt.Errorf("unknown scale-from-zero capacity source %q", source)
	}
	if source == "native" {
		for _, key := range []string{"machine.openshift.io/vCPU", "machine.openshift.io/memoryMb", "machine.openshift.io/GPU", prefix + "cpu", prefix + "memory", prefix + "gpu-count", prefix + "gpu-type"} {
			if _, found := annotations[key]; found {
				return fmt.Errorf("native capacity must not be overridden by %s", key)
			}
		}
		for _, name := range []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory} {
			if quantity := native[name]; quantity.Sign() <= 0 {
				return fmt.Errorf("native capacity must provide positive %s", name)
			}
		}
	} else {
		if len(native) != 0 {
			return fmt.Errorf("annotation fallback fixture must keep native capacity absent")
		}
		for _, pair := range []struct{ current, legacy string }{{"cpu", "vCPU"}, {"memory", "memoryMb"}} {
			quantity, err := resource.ParseQuantity(annotations[prefix+pair.current])
			if err != nil || quantity.Sign() <= 0 {
				return fmt.Errorf("CAPI %s must be a positive resource quantity", pair.current)
			}
			legacy, err := strconv.ParseInt(annotations["machine.openshift.io/"+pair.legacy], 10, 64)
			if err != nil || legacy <= 0 {
				return fmt.Errorf("legacy %s must be a positive integer", pair.legacy)
			}
			legacyQuantity := *resource.NewQuantity(legacy, resource.DecimalSI)
			if pair.current == "memory" {
				legacyQuantity, err = resource.ParseQuantity(strconv.FormatInt(legacy, 10) + "Mi")
				if err != nil {
					return fmt.Errorf("parse legacy memory: %w", err)
				}
			}
			if quantity.Cmp(legacyQuantity) != 0 {
				return fmt.Errorf("CAPI %s differs from legacy capacity", pair.current)
			}
		}
		legacyGPU, hasLegacyGPU := annotations["machine.openshift.io/GPU"]
		gpuCount, hasGPUCount := annotations[prefix+"gpu-count"]
		gpuType, hasGPUType := annotations[prefix+"gpu-type"]
		if hasLegacyGPU || hasGPUCount || hasGPUType {
			count, err := strconv.ParseInt(gpuCount, 10, 64)
			legacyCount, legacyErr := strconv.ParseInt(legacyGPU, 10, 64)
			if err != nil || legacyErr != nil || count <= 0 || count != legacyCount || gpuType != "nvidia.com/gpu" {
				return fmt.Errorf("GPU capacity requires matching positive legacy/CAPI counts and NVIDIA resource type")
			}
		}
	}
	nodeLabels, err := labels.ConvertSelectorToLabelsMap(annotations[prefix+"labels"])
	if err != nil || nodeLabels["kubernetes.io/arch"] == "" {
		return fmt.Errorf("capacity labels must include a usable architecture")
	}
	if architecture := nodeLabels["kubernetes.io/arch"]; architecture != hyperv1.ArchitectureAMD64 && architecture != hyperv1.ArchitectureARM64 {
		return fmt.Errorf("unsupported scale-from-zero architecture %q", architecture)
	}
	return nil
}
