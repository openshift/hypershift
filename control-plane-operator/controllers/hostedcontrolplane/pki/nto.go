package pki

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/openshift/hypershift/support/config"
)

func ReconcileNodeTuningOperatorServingCertSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	dnsNames := []string{
		"node-tuning-operator",
		fmt.Sprintf("node-tuning-operator.%s.svc", secret.Namespace),
		fmt.Sprintf("node-tuning-operator.%s.svc.cluster.local", secret.Namespace),
	}
	return reconcileSignedCertWithAddresses(secret, ca, ownerRef, "node-tuning-operator", []string{"openshift"}, X509UsageClientServerAuth, dnsNames, nil)
}
