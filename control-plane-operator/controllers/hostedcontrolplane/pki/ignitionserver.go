package pki

import (
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/openshift/hypershift/support/config"
)

func ReconcileIgnitionServerCertSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef, validity time.Duration) error {
	dnsNames := []string{
		"ignition-server",
		fmt.Sprintf("ignition-server.%s.svc", secret.Namespace),
		fmt.Sprintf("ignition-server.%s.svc.cluster.local", secret.Namespace),
	}
	return reconcileSignedCertWithAddresses(secret, ca, ownerRef, "ignition-server", []string{"openshift"}, X509UsageClientServerAuth, dnsNames, nil, validity)
}
