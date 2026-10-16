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
	"maps"
	"sort"
	"strconv"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/nodepool/instancetype"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	infrav1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capiazure "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
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

// setScaleFromZeroAnnotationsOnObject reconciles scheduling metadata on MachineDeployment or MachineSet.
// Resource-capacity workarounds are only needed when native capacity is absent
// and an instance-type provider is configured. Labels and taints are always needed.
func setScaleFromZeroAnnotationsOnObject(ctx context.Context, provider instancetype.Provider, nodePool *hyperv1.NodePool, object client.Object, machineTemplate interface{}) error {
	annotations := maps.Clone(object.GetAnnotations())
	if annotations == nil {
		annotations = make(map[string]string)
	}

	var instanceType string
	var statusCapacity map[corev1.ResourceName]resource.Quantity
	architecture := nodePool.Spec.Arch
	if architecture == "" {
		architecture = hyperv1.ArchitectureAMD64
	}

	switch template := machineTemplate.(type) {
	case *infrav1.AWSMachineTemplate:
		instanceType = template.Spec.Template.Spec.InstanceType
		statusCapacity = template.Status.Capacity
		if template.Status.NodeInfo != nil && template.Status.NodeInfo.Architecture != "" {
			architecture = string(template.Status.NodeInfo.Architecture)
		}
	case *capiazure.AzureMachineTemplate:
		instanceType = template.Spec.Template.Spec.VMSize
		statusCapacity = template.Status.Capacity
		if template.Status.NodeInfo != nil && template.Status.NodeInfo.Architecture != "" {
			architecture = string(template.Status.NodeInfo.Architecture)
		}
	default:
		return fmt.Errorf("unsupported machine template type: %T", machineTemplate)
	}

	// Native capacity replaces only the resource workarounds, not scheduling metadata.
	if len(statusCapacity) > 0 {
		for _, key := range []string{cpuKey, memoryKey, gpuKey} {
			delete(annotations, key)
		}
	} else if provider != nil {
		if instanceType == "" {
			return fmt.Errorf("instanceType is empty in machine template")
		}
		instanceInfo, err := provider.GetInstanceTypeInfo(ctx, instanceType)
		if err != nil {
			return fmt.Errorf("failed to get instance type information for %q: %w", instanceType, err)
		}
		if instanceInfo.CPUArchitecture != "" {
			architecture = instanceInfo.CPUArchitecture
		}
		annotations[cpuKey] = strconv.FormatInt(int64(instanceInfo.VCPU), 10)
		annotations[memoryKey] = strconv.FormatInt(instanceInfo.MemoryMb, 10)
		if instanceInfo.GPU > 0 {
			annotations[gpuKey] = strconv.FormatInt(int64(instanceInfo.GPU), 10)
		} else {
			delete(annotations, gpuKey)
		}
	} else {
		// No source remains to refresh capacity from a previously configured provider.
		for _, key := range []string{cpuKey, memoryKey, gpuKey} {
			delete(annotations, key)
		}
	}

	// Scheduling metadata is independent of native capacity or legacy provider configuration.
	labelsMap := map[string]string{}
	for k, v := range nodePool.Spec.NodeLabels {
		labelsMap[k] = v
	}
	// Ensure architecture reflects the real instance type (don't allow NodeLabels to override it)
	labelsMap[archLabelKey] = architecture

	labels := make([]string, 0, len(labelsMap))
	for k, v := range labelsMap {
		labels = append(labels, fmt.Sprintf("%s=%s", k, v))
	}
	sort.Strings(labels)
	annotations[labelsKey] = strings.Join(labels, ",")

	if len(nodePool.Spec.Taints) > 0 {
		taintsAnnotation := taintsToAnnotation(nodePool.Spec.Taints)
		annotations[taintsKey] = taintsAnnotation
	} else {
		// Remove taints annotation if there are no taints
		delete(annotations, taintsKey)
	}

	object.SetAnnotations(annotations)

	return nil
}
