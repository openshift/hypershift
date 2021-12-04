package pki

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/openshift/hypershift/support/config"
)

func ReconcileIngressCert(secret, ca *corev1.Secret, ownerRef config.OwnerRef, ingressSubdomain string) error {
	ingressHostNames := []string{fmt.Sprintf("*.%s", ingressSubdomain)}
	return reconcileSignedCertWithAddresses(secret, ca, ownerRef, "openshift-ingress", []string{"openshift"}, X509UsageClientServerAuth, ingressHostNames, nil)
}
