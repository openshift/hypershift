package oapi

import (
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	//TODO: Switch to k8s.io/api/policy/v1 when all management clusters at 1.21+ OR 4.8_openshift+
	policyv1beta1 "k8s.io/api/policy/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func ReconcilePodDisruptionBudget(pdb *policyv1beta1.PodDisruptionBudget, p *OpenShiftAPIServerParams) error {
	if pdb.CreationTimestamp.IsZero() {
		pdb.Spec.Selector = &metav1.LabelSelector{
			MatchLabels: openShiftAPIServerLabels(),
		}
	}

	p.OwnerRef.ApplyTo(pdb)

	var minAvailable int
	switch p.Availability {
	case hyperv1.SingleReplica:
		minAvailable = 0
	case hyperv1.HighlyAvailable:
		minAvailable = 1
	}
	pdb.Spec.MinAvailable = &intstr.IntOrString{Type: intstr.Int, IntVal: int32(minAvailable)}

	return nil
}
