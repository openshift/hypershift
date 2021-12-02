package pki

import (
	"net"

	"github.com/openshift/hypershift/support/config"
	corev1 "k8s.io/api/core/v1"
)

func ReconcileOAuthServerCert(secret, ca *corev1.Secret, ownerRef config.OwnerRef, externalOAuthAddress string) error {
	var dnsNames, ips []string
	oauthIP := net.ParseIP(externalOAuthAddress)
	if oauthIP != nil {
		ips = append(ips, externalOAuthAddress)
	} else {
		dnsNames = append(dnsNames, externalOAuthAddress)
	}
	return reconcileSignedCertWithAddresses(secret, ca, ownerRef, "openshift-oauth", []string{"openshift"}, X509DefaultUsage, X509UsageClientServerAuth, dnsNames, ips)
}
