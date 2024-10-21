package controlplanecomponent

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

// DisableIfAnnotationExist is a helper predicte for the common use case of disabling a resource when an annotation exists.
func DisableIfAnnotationExist(annotation string) option {
	return func(ga *genericAdapter) {
		ga.predicate = func(cpContext ControlPlaneContext) bool {
			if _, exists := cpContext.HCP.Annotations[annotation]; exists {
				return false
			}
			return true
		}
	}
}

// EnableForPlatform is a helper predicte for the common use case of only enabling a resource for a specific platfrom.
func EnableForPlatform(platform hyperv1.PlatformType) option {
	return func(ga *genericAdapter) {
		ga.predicate = func(cpContext ControlPlaneContext) bool {
			return cpContext.HCP.Spec.Platform.Type == platform
		}
	}
}
