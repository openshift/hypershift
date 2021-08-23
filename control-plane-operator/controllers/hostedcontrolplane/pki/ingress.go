package pki

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
)

func ReconcileIngressCert(secret, ca *corev1.Secret, ownerRef config.OwnerRef, ingressSubdomain string) error {
	ingressHostNames := []string{fmt.Sprintf("*.%s", ingressSubdomain)}
	return reconcileSignedCertWithAddresses(secret, ca, ownerRef, "openshift-ingress", "openshift", X509DefaultUsage, X509UsageClientServerAuth, ingressHostNames, nil)
}
