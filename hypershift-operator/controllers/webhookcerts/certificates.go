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

// Keep signer validation webhook-local rather than adding it to the shared
// certs.ReconcileSignedCert helper. Other callers (e.g. konnectivity migration)
// intentionally preserve valid leaves signed by a previous CA. The webhook
// reconciler must re-sign when the CA changes because it also controls the
// trust bundles in CRDs and webhook configs, so leaf and bundle must stay
// in sync.
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
