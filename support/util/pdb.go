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
