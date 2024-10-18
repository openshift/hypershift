package util

import (
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
)

func ReconcilePodDisruptionBudget(pdb *policyv1.PodDisruptionBudget) {
	pdb.Spec.MinAvailable = ptr.To(intstr.FromInt(0))
	pdb.Spec.MaxUnavailable = ptr.To(intstr.FromInt(1))
}
