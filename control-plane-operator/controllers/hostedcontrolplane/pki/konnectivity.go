package pki

import (
	"fmt"

	"github.com/openshift/hypershift/support/config"
	supportpki "github.com/openshift/hypershift/support/pki"

	corev1 "k8s.io/api/core/v1"
)

func ReconcileKonnectivitySignerSecret(secret *corev1.Secret, ownerRef config.OwnerRef) error {
	return reconcileSelfSignedCA(secret, ownerRef, "konnectivity-signer", "kubernetes")
}

func ReconcileKonnectivityServerSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	dnsNames := []string{
		"localhost",
		"konnectivity-server-local",
		fmt.Sprintf("konnectivity-server-local.%s.svc", secret.Namespace),
		fmt.Sprintf("konnectivity-server-local.%s.svc.cluster.local", secret.Namespace),
	}
	ips := []string{
		"127.0.0.1",
	}
	return reconcileSignedCertWithAddresses(secret, ca, ownerRef, "konnectivity-server-local", []string{"kubernetes"}, X509UsageServerAuth, dnsNames, ips)
}

func ReconcileKonnectivityClusterSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef, externalKconnectivityAddress string) error {
	dnsNames := []string{
		"konnectivity-server",
		fmt.Sprintf("konnectivity-server.%s.svc", secret.Namespace),
		fmt.Sprintf("konnectivity-server.%s.svc.cluster.local", secret.Namespace),
	}
	ips := []string{}
	if supportpki.IsNumericIP(externalKconnectivityAddress) {
		ips = append(ips, externalKconnectivityAddress)
	} else {
		dnsNames = append(dnsNames, externalKconnectivityAddress)
	}
	return reconcileSignedCertWithAddresses(secret, ca, ownerRef, "konnectivity-server", []string{"kubernetes"}, X509UsageServerAuth, dnsNames, ips)
}

func ReconcileKonnectivityClientSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	return reconcileSignedCert(secret, ca, ownerRef, "konnectivity-client", []string{"kubernetes"}, X509UsageClientAuth)
}

func ReconcileKonnectivityAgentSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef) error {
	return reconcileSignedCert(secret, ca, ownerRef, "konnectivity-agent", []string{"kubernetes"}, X509UsageClientAuth)
}
