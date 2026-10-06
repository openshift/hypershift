package controlplanecomponent

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/k8sutil"

	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// AdaptPodDisruptionBudget configures a PDB for HighlyAvailable control planes
// (maxUnavailable=1) and skips the PDB entirely for SingleReplica.
//
// A SingleReplica PDB with minAvailable=1 can never permit a disruption
// (disruptionsAllowed stays 0), so it blocks node drain indefinitely rather
// than expressing a budget. ControllerAvailabilityPolicy is immutable, so
// there is no later transition that would need the PDB added.
// Existing SingleReplica PDBs are deleted by the predicate-false path.
func AdaptPodDisruptionBudget() option {
	return func(ga *genericAdapter) {
		WithPredicate(func(cpContext WorkloadContext) bool {
			return cpContext.HCP.Spec.ControllerAvailabilityPolicy != hyperv1.SingleReplica
		})(ga)
		WithAdaptFunction(func(cpContext WorkloadContext, pdb *policyv1.PodDisruptionBudget) error {
			// YAML assets ship minAvailable: 1; clear it so HA can use maxUnavailable.
			pdb.Spec.MinAvailable = nil
			pdb.Spec.MaxUnavailable = nil
			if cpContext.HCP.Spec.ControllerAvailabilityPolicy == hyperv1.HighlyAvailable {
				pdb.Spec.MaxUnavailable = ptr.To(intstr.FromInt32(1))
			}
			pdb.Spec.UnhealthyPodEvictionPolicy = ptr.To(policyv1.AlwaysAllow)
			return nil
		})(ga)
	}
}

// SetHostedClusterAnnotation is a helper function to set the HostedCluster annotation on a resource.
// This is useful for resources created by the HostedCluster controller, so external changes can be detected and reconciled.
func SetHostedClusterAnnotation() option {
	return func(ga *genericAdapter) {
		ga.adapt = func(cpContext WorkloadContext, resource client.Object) error {
			annotations := resource.GetAnnotations()
			if annotations == nil {
				annotations = map[string]string{}
			}
			annotations[k8sutil.HostedClusterAnnotation] = cpContext.HCP.Annotations[k8sutil.HostedClusterAnnotation]
			resource.SetAnnotations(annotations)
			return nil
		}
	}
}

// DisableIfAnnotationExist is a helper predicate for the common use case of disabling a resource when an annotation exists.
func DisableIfAnnotationExist(annotation string) option {
	return WithPredicate(func(cpContext WorkloadContext) bool {
		if _, exists := cpContext.HCP.Annotations[annotation]; exists {
			return false
		}
		return true
	})
}

// EnableForPlatform is a helper predicate for the common use case of only enabling a resource for a specific platform.
func EnableForPlatform(platform hyperv1.PlatformType) option {
	return WithPredicate(func(cpContext WorkloadContext) bool {
		return cpContext.HCP.Spec.Platform.Type == platform
	})
}

// IsStorageAndCSIManaged returns true if storage and CSI components should be managed for the given platform.
// IBMCloud and PowerVS platforms do not support managed storage/CSI.
func IsStorageAndCSIManaged(platform hyperv1.PlatformType) bool {
	if platform == hyperv1.IBMCloudPlatform || platform == hyperv1.PowerVSPlatform {
		return false
	}
	return true
}
