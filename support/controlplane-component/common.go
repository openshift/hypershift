package controlplanecomponent

// DisableIfAnnotationExist is a helper predicte for the common use case of disabling a resource when an annotation exists.
func DisableIfAnnotationExist(annotation string) Predicate {
	return func(cpContext ControlPlaneContext) bool {
		if _, exists := cpContext.HCP.Annotations[annotation]; exists {
			return false
		}
		return true
	}
}
