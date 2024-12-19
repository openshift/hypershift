package pki

import (
	"net"

	"github.com/openshift/hypershift/support/certs"
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
	return reconcileSignedCertWithAddresses(secret, ca, ownerRef, "openshift-oauth", []string{"openshift"}, X509UsageClientServerAuth, dnsNames, ips)
}

func ReconcileOAuthMasterCABundle(caBundle *corev1.ConfigMap, ownerRef config.OwnerRef, sourceCerts []*corev1.Secret) error {
	var sources []*corev1.Secret
	// Collect all sources and use the same key (ca.crt) whether original is ca.crt or tls.crt
	for _, cert := range sourceCerts {
		if _, hasKey := cert.Data[certs.CASignerCertMapKey]; hasKey {
			sources = append(sources, cert.DeepCopy())
			continue
		}
		sources = append(sources, &corev1.Secret{
			Data: map[string][]byte{
				certs.CASignerCertMapKey: cert.Data[corev1.TLSCertKey],
			},
		})
	}
	return reconcileAggregateCA(caBundle, ownerRef, sources...)
}
