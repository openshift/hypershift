package pki

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
)

func (p *PKIParams) ReconcileMachineConfigServerCert(secret, ca *corev1.Secret) error {
	hostNames := []string{
		"machine-config-server",
		fmt.Sprintf("machine-config-server.%s.svc", p.Namespace),
		fmt.Sprintf("machine-config-server.%s.svc.cluster.local", p.Namespace),
	}
	return p.reconcileSignedCertWithAddresses(secret, ca, "machine-config-server", "openshift", X509DefaultUsage, X509UsageClientServerAuth, hostNames, nil)
}
