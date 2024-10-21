package kas

import (
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	policyv1 "k8s.io/api/policy/v1"
)

func adaptPodDisruptionBudget(cpContext component.ControlPlaneContext, pdb *policyv1.PodDisruptionBudget) error {
	util.ReconcilePodDisruptionBudget(pdb, cpContext.HCP.Spec.ControllerAvailabilityPolicy)
	return nil
}
