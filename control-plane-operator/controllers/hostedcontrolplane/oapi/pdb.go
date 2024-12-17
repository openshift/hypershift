package oapi

import (
	"github.com/openshift/hypershift/support/util"

	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ReconcilePodDisruptionBudget(pdb *policyv1.PodDisruptionBudget, p *OpenShiftAPIServerParams) error {
	if pdb.CreationTimestamp.IsZero() {
		pdb.Spec.Selector = &metav1.LabelSelector{
			MatchLabels: openShiftAPIServerLabels(),
		}
	}
	p.OwnerRef.ApplyTo(pdb)
	util.ReconcilePodDisruptionBudget(pdb, p.Availability)
	return nil
}
