package controlplanecomponent

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
)

func AdaptPodDisruptionBudget() option {
	return WithAdaptFunction(func(cpContext WorkloadContext, pdb *policyv1.PodDisruptionBudget) error {
		var minAvailable *intstr.IntOrString
		var maxUnavailable *intstr.IntOrString
		switch cpContext.HCP.Spec.ControllerAvailabilityPolicy {
		case hyperv1.SingleReplica:
			minAvailable = ptr.To(intstr.FromInt32(1))
		case hyperv1.HighlyAvailable:
			maxUnavailable = ptr.To(intstr.FromInt32(1))
		}

		pdb.Spec.MinAvailable = minAvailable
		pdb.Spec.MaxUnavailable = maxUnavailable
		return nil
	})
}

// DisableIfAnnotationExist is a helper predicte for the common use case of disabling a resource when an annotation exists.
func DisableIfAnnotationExist(annotation string) option {
	return WithPredicate(func(cpContext WorkloadContext) bool {
		if _, exists := cpContext.HCP.Annotations[annotation]; exists {
			return false
		}
		return true
	})
}

// EnableForPlatform is a helper predicte for the common use case of only enabling a resource for a specific platfrom.
func EnableForPlatform(platform hyperv1.PlatformType) option {
	return WithPredicate(func(cpContext WorkloadContext) bool {
		return cpContext.HCP.Spec.Platform.Type == platform
	})
}
