package webhookcerts

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"time"

	"github.com/openshift/hypershift/support/certs"

	corev1 "k8s.io/api/core/v1"
)

// Keep signer validation webhook-local: konnectivity migration relies on the shared helper preserving valid leaves signed by legacy CAs.
func reconcileWebhookServingCert(secret, ca *corev1.Secret, namespace, serviceName string) error {
	keyPair, err := tls.X509KeyPair(ca.Data[certs.CASignerCertMapKey], ca.Data[certs.CASignerKeyMapKey])
	if err != nil {
		return fmt.Errorf("invalid webhook CA key pair: %w", err)
	}
	signer, err := x509.ParseCertificate(keyPair.Certificate[0])
	if err != nil {
		return fmt.Errorf("invalid webhook CA certificate: %w", err)
	}
	if !signer.IsCA || !signer.BasicConstraintsValid || signer.KeyUsage&x509.KeyUsageCertSign == 0 {
		return fmt.Errorf("webhook CA is not a certificate signer")
	}
	if now := time.Now(); now.Before(signer.NotBefore) || !now.Before(signer.NotAfter) {
		return fmt.Errorf("webhook CA is outside its validity period")
	}
	leaf, err := certs.PemToCertificate(secret.Data[corev1.TLSCertKey])
	if err != nil || !bytes.Equal(leaf.RawIssuer, signer.RawSubject) || leaf.CheckSignatureFrom(signer) != nil {
		delete(secret.Data, corev1.TLSCertKey)
		delete(secret.Data, corev1.TLSPrivateKeyKey)
	}
	return certs.ReconcileSignedCert(secret, ca, "hypershift-operator", []string{"openshift"},
		[]x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, corev1.TLSCertKey, corev1.TLSPrivateKeyKey, "",
		webhookDNSNames(serviceName, namespace), nil)
}
