package pki

import (
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/openshift/hypershift/support/config"
)

func ReconcileOVNControlPlaneMetricsServingCertSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef, validity time.Duration) error {
	dnsNames := []string{
		fmt.Sprintf("ovnkube-control-plane.%s.svc", secret.Namespace),
		fmt.Sprintf("ovnkube-control-plane.%s.svc.cluster.local", secret.Namespace),
		"ovnkube-control-plane",
		"localhost",
	}
	return reconcileSignedCertWithAddressesAndSecretType(secret, ca, ownerRef, "ovnkube-control-plane", []string{"openshift"}, X509UsageClientServerAuth, dnsNames, nil, corev1.SecretTypeTLS, validity)
}
