package controlplanecomponent

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"slices"
	"strconv"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	schedulingv1alpha1 "github.com/openshift/hypershift/api/scheduling/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

const containerResourcePolicyHashAnnotation = "hypershift.openshift.io/container-resource-policy-hash"

type containerKey struct{ workload, container string }

// ApplyContainerResourcePolicy projects the policy onto every regular and init
// container, after manifest adaptation, sidecar injection and legacy overrides.
// This only affects the supplied template, not workloads created by downstream operators.
// Callers must supply a freshly generated template: environment defaults are not
// recovered from a previously projected template. An invalid policy leaves the
// template unchanged. An absent annotation disables projection.
func ApplyContainerResourcePolicy(workloadName string, template *corev1.PodTemplateSpec, annotations map[string]string) error {
	if template == nil {
		return fmt.Errorf("container resource policy: pod template is nil")
	}
	encoded, present := annotations[hyperv1.ContainerResourcePolicyAnnotation]
	if !present {
		delete(template.Annotations, containerResourcePolicyHashAnnotation)
		return nil
	}
	var policy schedulingv1alpha1.ContainerResourcePolicy
	if err := json.Unmarshal([]byte(encoded), &policy); err != nil {
		return fmt.Errorf("invalid container resource policy: %w", err)
	}
	entries, err := validateContainerResourcePolicy(policy)
	if err != nil {
		return err
	}

	desired := template.DeepCopy()
	for _, containers := range [][]corev1.Container{desired.Spec.Containers, desired.Spec.InitContainers} {
		for i := range containers {
			container := &containers[i]
			requests, percent := policy.DefaultRequests, policy.GoMemoryLimitPercent
			entry, matched := entries[containerKey{workloadName, container.Name}]
			if matched {
				requests = entry.Requests
				if entry.GoMemoryLimitPercent != nil {
					percent = *entry.GoMemoryLimitPercent
				}
			}
			if container.Resources.Requests == nil {
				container.Resources.Requests = corev1.ResourceList{}
			}
			if container.Resources.Limits == nil {
				container.Resources.Limits = corev1.ResourceList{}
			}
			container.Resources.Requests[corev1.ResourceCPU] = requests.CPU.DeepCopy()
			container.Resources.Requests[corev1.ResourceMemory] = requests.Memory.DeepCopy()
			memoryBytes := requests.Memory.Value()
			memoryLimit, _ := memoryLimitForRequest(policy, requests.Memory) // All requests were validated before mutation.
			container.Resources.Limits[corev1.ResourceMemory] = memoryLimit
			delete(container.Resources.Limits, corev1.ResourceCPU)
			if policy.CPULimitPolicy == schedulingv1alpha1.CPULimitPolicyEqualsRequest {
				container.Resources.Limits[corev1.ResourceCPU] = requests.CPU.DeepCopy()
			}

			container.Env = slices.DeleteFunc(container.Env, func(env corev1.EnvVar) bool {
				return env.Name == "GOMEMLIMIT" || (entry.GoMaxProcs != 0 && env.Name == "GOMAXPROCS")
			})
			if percent != 0 {
				// Divide first so even MaxInt64 bytes can be scaled without overflowing.
				goMemoryBytes := memoryBytes/100*int64(percent) + memoryBytes%100*int64(percent)/100
				container.Env = append(container.Env, corev1.EnvVar{Name: "GOMEMLIMIT", Value: strconv.FormatInt(goMemoryBytes, 10)})
			}
			if entry.GoMaxProcs != 0 {
				container.Env = append(container.Env, corev1.EnvVar{Name: "GOMAXPROCS", Value: strconv.FormatInt(int64(entry.GoMaxProcs), 10)})
			}
		}
	}
	canonical, err := json.Marshal(policy)
	if err != nil {
		return fmt.Errorf("encoding container resource policy: %w", err)
	}
	if desired.Annotations == nil {
		desired.Annotations = map[string]string{}
	}
	desired.Annotations[containerResourcePolicyHashAnnotation] = fmt.Sprintf("%x", sha256.Sum256(canonical))
	*template = *desired
	return nil
}

func validateContainerResourcePolicy(policy schedulingv1alpha1.ContainerResourcePolicy) (map[containerKey]schedulingv1alpha1.ContainerResources, error) {
	if (policy.MemoryLimitMultiplier == 0) == (policy.MemoryLimitPercent == 0) {
		return nil, fmt.Errorf("container resource policy: exactly one of memoryLimitMultiplier or memoryLimitPercent is required")
	}
	if policy.MemoryLimitMultiplier < 0 || policy.MemoryLimitMultiplier > 16 {
		return nil, fmt.Errorf("container resource policy: memoryLimitMultiplier must be between 1 and 16")
	}
	if policy.MemoryLimitPercent != 0 && (policy.MemoryLimitPercent < 100 || policy.MemoryLimitPercent > 1600) {
		return nil, fmt.Errorf("container resource policy: memoryLimitPercent must be between 100 and 1600")
	}
	if policy.GoMemoryLimitPercent < 1 || policy.GoMemoryLimitPercent > 100 {
		return nil, fmt.Errorf("container resource policy: goMemoryLimitPercent must be between 1 and 100")
	}
	switch policy.CPULimitPolicy {
	case "", schedulingv1alpha1.CPULimitPolicyNone, schedulingv1alpha1.CPULimitPolicyEqualsRequest:
	default:
		return nil, fmt.Errorf("container resource policy: unknown cpuLimitPolicy %q, must be None or EqualsRequest", policy.CPULimitPolicy)
	}
	validateRequests := func(requests schedulingv1alpha1.ContainerRequests) error {
		if requests.CPU.Sign() <= 0 || requests.Memory.Sign() <= 0 {
			return fmt.Errorf("CPU and memory requests must be positive")
		}
		_, err := memoryLimitForRequest(policy, requests.Memory)
		return err
	}
	if err := validateRequests(policy.DefaultRequests); err != nil {
		return nil, fmt.Errorf("container resource policy defaultRequests: %w", err)
	}
	entries := make(map[containerKey]schedulingv1alpha1.ContainerResources, len(policy.Containers))
	for _, entry := range policy.Containers {
		key := containerKey{entry.Workload, entry.Container}
		if key.workload == "" || key.container == "" {
			return nil, fmt.Errorf("container resource policy: workload and container names are required")
		}
		if _, exists := entries[key]; exists {
			return nil, fmt.Errorf("container resource policy: duplicate entry for %s/%s", key.workload, key.container)
		}
		if err := validateRequests(entry.Requests); err != nil {
			return nil, fmt.Errorf("container resource policy %s/%s: %w", key.workload, key.container, err)
		}
		if entry.GoMemoryLimitPercent != nil && (*entry.GoMemoryLimitPercent < 0 || *entry.GoMemoryLimitPercent > 100) {
			return nil, fmt.Errorf("container resource policy %s/%s: goMemoryLimitPercent must be between 0 and 100", key.workload, key.container)
		}
		if entry.GoMaxProcs < 0 || entry.GoMaxProcs > 1024 {
			return nil, fmt.Errorf("container resource policy %s/%s: goMaxProcs must be between 1 and 1024", key.workload, key.container)
		}
		entries[key] = entry
	}

	return entries, nil
}

// memoryLimitForRequest requires a valid memory mode and a positive request.
func memoryLimitForRequest(policy schedulingv1alpha1.ContainerResourcePolicy, memory resource.Quantity) (resource.Quantity, error) {
	if policy.MemoryLimitPercent == 0 {
		maxMemory := resource.NewQuantity(math.MaxInt64/int64(policy.MemoryLimitMultiplier), resource.DecimalSI)
		if memory.Cmp(*maxMemory) > 0 {
			return resource.Quantity{}, fmt.Errorf("memory limit overflows int64 bytes")
		}
		limit := memory.DeepCopy()
		limit.Mul(int64(policy.MemoryLimitMultiplier))
		return limit, nil
	}
	// Exact rational arithmetic avoids truncation and overflow before rounding up to MiB.
	value, _ := new(big.Rat).SetString(memory.AsDec().String())
	value.Mul(value, big.NewRat(int64(policy.MemoryLimitPercent), 100*(1<<20)))
	mib, remainder := new(big.Int), new(big.Int)
	mib.QuoRem(value.Num(), value.Denom(), remainder)
	if remainder.Sign() != 0 {
		mib.Add(mib, big.NewInt(1))
	}
	bytes := mib.Mul(mib, big.NewInt(1<<20))
	if !bytes.IsInt64() {
		return resource.Quantity{}, fmt.Errorf("memory limit overflows int64 bytes")
	}
	return *resource.NewQuantity(bytes.Int64(), resource.BinarySI), nil
}
