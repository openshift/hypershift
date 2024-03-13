package oauth

import (
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func ReconcilePodDisruptionBudget(pdb *policyv1.PodDisruptionBudget, p *OAuthServerParams) error {
	if pdb.CreationTimestamp.IsZero() {
		pdb.Spec.Selector = &metav1.LabelSelector{
			MatchLabels: oauthLabels(),
		}
	}

	p.OwnerRef.ApplyTo(pdb)

	maxUnavailable := 1
	pdb.Spec.MaxUnavailable = &intstr.IntOrString{Type: intstr.Int, IntVal: int32(maxUnavailable)}

	return nil
}
