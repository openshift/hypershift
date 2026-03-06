/*
Copyright The Kubernetes Authors.
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package nodepool

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/nodepool/instancetype"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	capa "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capz "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// Annotation keys for scale-from-zero workaround
	cpuKey    = "machine.openshift.io/vCPU"
	memoryKey = "machine.openshift.io/memoryMb"
	gpuKey    = "machine.openshift.io/GPU"
	labelsKey = "capacity.cluster-autoscaler.kubernetes.io/labels"
	taintsKey = "capacity.cluster-autoscaler.kubernetes.io/taints"

	archLabelKey = "kubernetes.io/arch"
)

// taintsToAnnotation converts HyperShift taints to the CAPI format.
// Format: "key=value:effect" for taints with values, "key:effect" for taints without values
// See: https://github.com/kubernetes-sigs/cluster-api/blob/main/docs/proposals/20210310-opt-in-autoscaling-from-zero.md
func taintsToAnnotation(taints []hyperv1.Taint) string {
	if len(taints) == 0 {
		return ""
	}

	var parts []string
	for _, t := range taints {
		if t.Value == "" {
			parts = append(parts, fmt.Sprintf("%s:%s", t.Key, t.Effect))
		} else {
			parts = append(parts, fmt.Sprintf("%s=%s:%s", t.Key, t.Value, t.Effect))
		}
	}
	// Sort for deterministic output
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// setScaleFromZeroAnnotationsOnObject sets scale-from-zero workaround annotations on MachineDeployment or MachineSet.
// This is called during resource creation/update to provide capacity information
// for cluster-autoscaler when the infrastructure provider doesn't support Status.Capacity natively.
func setScaleFromZeroAnnotationsOnObject(ctx context.Context, provider instancetype.Provider, nodePool *hyperv1.NodePool, object client.Object, machineTemplate any) error {
	// 0. Get and initialize annotations
	annotations := object.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}

	// 1. Extract platform-specific fields using type switch
	var instanceType string
	var statusCapacity map[corev1.ResourceName]resource.Quantity

	switch template := machineTemplate.(type) {
	case *capa.AWSMachineTemplate:
		instanceType = template.Spec.Template.Spec.InstanceType
		statusCapacity = template.Status.Capacity
	case *capz.AzureMachineTemplate:
		instanceType = template.Spec.Template.Spec.VMSize
		// Azure CAPI provider doesn't support Status.Capacity, so statusCapacity remains nil
	default:
		return fmt.Errorf("unsupported machine template type: %T", machineTemplate)
	}

	// 2. Check if Status.Capacity is already provided (prefer native support)
	if len(statusCapacity) > 0 {
		// Clean up workaround annotations (if they exist)
		annotationKeys := []string{cpuKey, memoryKey, gpuKey, labelsKey, taintsKey}
		for _, key := range annotationKeys {
			delete(annotations, key)
		}

		// Set annotations back to the object
		object.SetAnnotations(annotations)
		// No need to add annotations, infrastructure provider provides Status.Capacity
		return nil
	}

	// 2. Skip if provider is not configured
	if provider == nil {
		// No provider configured, skip setting scale-from-zero annotations
		return nil
	}

	// 3. Validate instance type is not empty
	if instanceType == "" {
		return fmt.Errorf("instanceType is empty in machine template")
	}

	// 4. Get instance type information from provider
	instanceInfo, err := provider.GetInstanceTypeInfo(ctx, instanceType)
	if err != nil {
		return err
	}

	// 5. Set workaround annotations
	annotations[cpuKey] = strconv.FormatInt(int64(instanceInfo.VCPU), 10)
	annotations[memoryKey] = strconv.FormatInt(instanceInfo.MemoryMb, 10)

	// Set GPU annotation only if GPU > 0 (consistent with taints handling)
	if instanceInfo.GPU > 0 {
		annotations[gpuKey] = strconv.FormatInt(int64(instanceInfo.GPU), 10)
	} else {
		// Remove GPU annotation if there are no GPUs
		delete(annotations, gpuKey)
	}

	// 6. Set labels (including architecture and NodePool.Spec.NodeLabels)
	labelsMap := map[string]string{}
	for k, v := range nodePool.Spec.NodeLabels {
		labelsMap[k] = v
	}
	// Ensure architecture reflects the real instance type (don't allow NodeLabels to override it)
	labelsMap[archLabelKey] = instanceInfo.CPUArchitecture

	labels := make([]string, 0, len(labelsMap))
	for k, v := range labelsMap {
		labels = append(labels, fmt.Sprintf("%s=%s", k, v))
	}
	sort.Strings(labels)
	annotations[labelsKey] = strings.Join(labels, ",")

	// 7. Set taints
	if len(nodePool.Spec.Taints) > 0 {
		taintsAnnotation := taintsToAnnotation(nodePool.Spec.Taints)
		annotations[taintsKey] = taintsAnnotation
	} else {
		// Remove taints annotation if there are no taints
		delete(annotations, taintsKey)
	}

	// 8. Set annotations back to the object
	object.SetAnnotations(annotations)

	return nil
}
