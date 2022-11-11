package pki

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/openshift/hypershift/support/config"
)

func ReconcileMachineConfigServerCert(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	hostNames := []string{
		fmt.Sprintf("*.machine-config-server.%s.svc.cluster.local", secret.Namespace),
	}
	return reconcileSignedCertWithAddresses(secret, ca, ownerRef, "machine-config-server", []string{"openshift"}, X509UsageServerAuth, hostNames, nil)
}
