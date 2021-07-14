package pki

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"net"

	corev1 "k8s.io/api/core/v1"

	"github.com/openshift/hypershift/certs"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
)

var (
	X509UsageClientAuth       = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	X509UsageServerAuth       = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	X509UsageClientServerAuth = append(X509UsageClientAuth, X509UsageServerAuth...)

	X509DefaultUsage = x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature
	X509SignerUsage  = X509DefaultUsage | x509.KeyUsageCertSign
)

func reconcileSignedCert(secret *corev1.Secret, ca *corev1.Secret, ownerRef config.OwnerRef, cn, org string, usage x509.KeyUsage, extUsages []x509.ExtKeyUsage) error {
	return reconcileSignedCertWithKeys(secret, ca, ownerRef, cn, org, usage, extUsages, corev1.TLSCertKey, corev1.TLSPrivateKeyKey, CASignerCertMapKey)
}

func reconcileSignedCertWithKeys(secret, ca *corev1.Secret, ownerRef config.OwnerRef, cn, org string, usage x509.KeyUsage, extUsages []x509.ExtKeyUsage, crtKey, keyKey, caKey string) error {
	return reconcileSignedCertWithKeysAndAddresses(secret, ca, ownerRef, cn, org, usage, extUsages, crtKey, keyKey, caKey, nil, nil)
}

func reconcileSignedCertWithAddresses(secret, ca *corev1.Secret, ownerRef config.OwnerRef, cn, org string, usage x509.KeyUsage, extUsages []x509.ExtKeyUsage, dnsNames []string, ips []string) error {
	return reconcileSignedCertWithKeysAndAddresses(secret, ca, ownerRef, cn, org, usage, extUsages, corev1.TLSCertKey, corev1.TLSPrivateKeyKey, CASignerCertMapKey, dnsNames, ips)
}

func reconcileSignedCertWithKeysAndAddresses(secret *corev1.Secret, ca *corev1.Secret, ownerRef config.OwnerRef, cn, org string, usage x509.KeyUsage, extUsages []x509.ExtKeyUsage, crtKey, keyKey, caKey string, dnsNames []string, ips []string) error {
	ownerRef.ApplyTo(secret)
	if !ValidCA(ca) {
		return fmt.Errorf("invalid CA signer secret %s for cert(cn=%s,o=%s)", ca.Name, cn, org)
	}
	expectedKeys := []string{crtKey, keyKey, caKey}
	secret.Type = corev1.SecretTypeOpaque
	if SignedSecretUpToDate(secret, ca, expectedKeys) {
		return nil
	}
	var ipAddresses []net.IP
	for _, ip := range ips {
		address := net.ParseIP(ip)
		if address == nil {
			return fmt.Errorf("invalid IP address %s for cert(cn=%s,o=%s)", ip, cn, org)
		}
		ipAddresses = append(ipAddresses, address)
	}

	cfg := &certs.CertCfg{
		Subject:      pkix.Name{CommonName: cn, Organization: []string{org}},
		KeyUsages:    x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsages: extUsages,
		Validity:     certs.ValidityOneYear,
		DNSNames:     dnsNames,
		IPAddresses:  ipAddresses,
	}
	certBytes, keyBytes, caBytes, err := SignCertificate(cfg, ca)
	if err != nil {
		return fmt.Errorf("error signing cert(cn=%s,o=%s): %w", cn, org, err)
	}
	if secret.Data == nil {
		secret.Data = map[string][]byte{}
	}
	secret.Data[crtKey] = certBytes
	secret.Data[keyKey] = keyBytes
	secret.Data[caKey] = caBytes
	AnnotateWithCA(secret, ca)
	return nil
}
