package oapi

import (
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func ReconcilePodDisruptionBudget(pdb *policyv1.PodDisruptionBudget, p *OpenShiftAPIServerParams) error {
	if pdb.CreationTimestamp.IsZero() {
		pdb.Spec.Selector = &metav1.LabelSelector{
			MatchLabels: openShiftAPIServerLabels(),
		}
	}

	p.OwnerRef.ApplyTo(pdb)

	minAvailable := 1
	pdb.Spec.MinAvailable = &intstr.IntOrString{Type: intstr.Int, IntVal: int32(minAvailable)}

	return nil
}
