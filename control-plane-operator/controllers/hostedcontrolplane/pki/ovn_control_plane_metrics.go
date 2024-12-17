package pki

import (
	"fmt"

	"github.com/openshift/hypershift/support/config"

	corev1 "k8s.io/api/core/v1"
)

func ReconcileOVNControlPlaneMetricsServingCertSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	dnsNames := []string{
		fmt.Sprintf("ovnkube-control-plane.%s.svc", secret.Namespace),
		fmt.Sprintf("ovnkube-control-plane.%s.svc.cluster.local", secret.Namespace),
		"ovnkube-control-plane",
		"localhost",
	}
	return reconcileSignedCertWithAddressesAndSecretType(secret, ca, ownerRef, "ovnkube-control-plane", []string{"openshift"}, X509UsageClientServerAuth, dnsNames, nil, corev1.SecretTypeTLS)
}
