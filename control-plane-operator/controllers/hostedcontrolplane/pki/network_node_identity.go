package pki

import (
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/openshift/hypershift/support/config"
)

func ReconcileNetworkNodeIdentityServingCertSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef, validity time.Duration) error {
	dnsNames := []string{
		fmt.Sprintf("network-node-identity.%s.svc", secret.Namespace),
		fmt.Sprintf("network-node-identity.%s.svc.cluster.local", secret.Namespace),
		"network-node-identity",
		"localhost",
	}
	return reconcileSignedCertWithAddressesAndSecretType(secret, ca, ownerRef, "network-node-identity", []string{"openshift"}, X509UsageClientServerAuth, dnsNames, nil, corev1.SecretTypeTLS, validity)
}
