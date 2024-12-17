package pki

import (
	"fmt"

	"github.com/openshift/hypershift/support/config"

	corev1 "k8s.io/api/core/v1"
)

func ReconcileCVOServerSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	dnsNames := []string{
		fmt.Sprintf("cluster-version-operator.%s.svc", secret.Namespace),
		fmt.Sprintf("cluster-version-operator.%s.svc.cluster.local", secret.Namespace),
		"cluster-version-operator",
		"localhost",
	}
	return reconcileSignedCertWithAddresses(secret, ca, ownerRef, "cluster-version-operator", []string{"openshift"}, X509UsageClientServerAuth, dnsNames, nil)
}
