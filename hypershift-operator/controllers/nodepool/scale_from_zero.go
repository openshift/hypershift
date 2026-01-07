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

	infrav1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
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
// Format: "key=value:effect,key2=value2:effect2"
// See: https://github.com/kubernetes-sigs/cluster-api/blob/main/docs/proposals/20210310-opt-in-autoscaling-from-zero.md
func taintsToAnnotation(taints []hyperv1.Taint) string {
	if len(taints) == 0 {
		return ""
	}

	var parts []string
	for _, t := range taints {
		parts = append(parts, fmt.Sprintf("%s=%s:%s", t.Key, t.Value, t.Effect))
	}
	// Sort for deterministic output
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// setScaleFromZeroAnnotationsOnObject sets scale-from-zero workaround annotations on MachineDeployment or MachineSet.
// This is called during resource creation/update to provide capacity information
// for cluster-autoscaler when CAPA doesn't support Status.Capacity natively.
func setScaleFromZeroAnnotationsOnObject(ctx context.Context, provider instancetype.Provider, nodePool *hyperv1.NodePool, annotations map[string]string, awsMachineTemplate *infrav1.AWSMachineTemplate) error {
	// 0. Check if CAPA already provides Status.Capacity (prefer native support)
	if len(awsMachineTemplate.Status.Capacity) > 0 {
		// Clean up workaround annotations (if they exist)
		annotationKeys := []string{cpuKey, memoryKey, gpuKey, labelsKey, taintsKey}
		for _, key := range annotationKeys {
			delete(annotations, key)
		}

		// No need to add annotations, CAPA provides Status.Capacity
		return nil
	}

	// 1. Skip if provider is not configured
	if provider == nil {
		// No provider configured, skip setting scale-from-zero annotations
		return nil
	}

	// 2. Extract instance type
	instanceType := awsMachineTemplate.Spec.Template.Spec.InstanceType
	if instanceType == "" {
		return fmt.Errorf("instanceType is empty in AWSMachineTemplate")
	}

	// 3. Get instance type information from provider
	instanceInfo, err := provider.GetInstanceTypeInfo(ctx, instanceType)
	if err != nil {
		return err
	}

	// 4. Set workaround annotations
	annotations[cpuKey] = strconv.FormatInt(int64(instanceInfo.VCPU), 10)
	annotations[memoryKey] = strconv.FormatInt(instanceInfo.MemoryMb, 10)
	annotations[gpuKey] = strconv.FormatInt(int64(instanceInfo.GPU), 10)

	// 5. Set labels (including architecture and NodePool.Spec.NodeLabels)
	labelsMap := map[string]string{
		archLabelKey: string(instanceInfo.CPUArchitecture),
	}
	for k, v := range nodePool.Spec.NodeLabels {
		labelsMap[k] = v
	}
	labels := make([]string, 0, len(labelsMap))
	for k, v := range labelsMap {
		labels = append(labels, fmt.Sprintf("%s=%s", k, v))
	}
	sort.Strings(labels)
	annotations[labelsKey] = strings.Join(labels, ",")

	// 6. Set taints
	if len(nodePool.Spec.Taints) > 0 {
		taintsAnnotation := taintsToAnnotation(nodePool.Spec.Taints)
		annotations[taintsKey] = taintsAnnotation
	} else {
		// Remove taints annotation if there are no taints
		delete(annotations, taintsKey)
	}

	return nil
}
