package util

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
)

func ReconcilePodDisruptionBudget(pdb *policyv1.PodDisruptionBudget, availability hyperv1.AvailabilityPolicy) {
	var minAvailable *intstr.IntOrString
	var maxUnavailable *intstr.IntOrString
	switch availability {
	case hyperv1.SingleReplica:
		minAvailable = ptr.To(intstr.FromInt32(1))
	case hyperv1.HighlyAvailable:
		maxUnavailable = ptr.To(intstr.FromInt32(1))
	}
	pdb.Spec.MinAvailable = minAvailable
	pdb.Spec.MaxUnavailable = maxUnavailable
}

// IsPDBDisabled checks if PDB is disabled either globally or for a specific component.
// It checks both the global disable-pdb-all annotation and the component-specific annotation.
func IsPDBDisabled(annotations map[string]string, componentAnnotation string) bool {
	if annotations == nil {
		return false
	}

	// Check global disable annotation
	if val, exists := annotations[DisablePDBsAllAnnotation]; exists && val == "true" {
		return true
	}

	// Check component-specific annotation
	if componentAnnotation != "" {
		if val, exists := annotations[componentAnnotation]; exists && val == "true" {
			return true
		}
	}

	return false
}
