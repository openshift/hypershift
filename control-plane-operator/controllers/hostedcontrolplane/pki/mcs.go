package pki

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
)

func ReconcileMachineConfigServerCert(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	hostNames := []string{
		"machine-config-server",
		fmt.Sprintf("machine-config-server.%s.svc", secret.Namespace),
		fmt.Sprintf("machine-config-server.%s.svc.cluster.local", secret.Namespace),
	}
	return reconcileSignedCertWithAddresses(secret, ca, ownerRef, "machine-config-server", "openshift", X509DefaultUsage, X509UsageClientServerAuth, hostNames, nil)
}
