package pki

import (
	"fmt"

	"github.com/openshift/hypershift/support/config"

	corev1 "k8s.io/api/core/v1"
)

func ReconcileKCMServerSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	dnsNames := []string{
		fmt.Sprintf("kube-controller-manager.%s.svc", secret.Namespace),
		fmt.Sprintf("kube-controller-manager.%s.svc.cluster.local", secret.Namespace),
		"kube-controller-manager",
		"localhost",
	}
	return reconcileSignedCertWithAddresses(secret, ca, ownerRef, "kube-controller-manager", []string{"openshift"}, X509UsageClientServerAuth, dnsNames, nil)
}
