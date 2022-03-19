package pki

import (
	"bytes"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/config"
)

func reconcileSelfSignedCA(secret *corev1.Secret, ownerRef config.OwnerRef, cn, ou string) error {
	ownerRef.ApplyTo(secret)
	secret.Type = corev1.SecretTypeOpaque
	if hasKeys(secret, CASignerKeyMapKey, CASignerKeyMapKey) {
		return nil
	}
	cfg := &certs.CertCfg{
		Subject:   pkix.Name{CommonName: cn, OrganizationalUnit: []string{ou}},
		KeyUsages: x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		Validity:  certs.ValidityTenYears,
		IsCA:      true,
	}
	key, crt, err := certs.GenerateSelfSignedCertificate(cfg)
	if err != nil {
		return fmt.Errorf("failed to generate CA (cn=%s,ou=%s): %w", cn, ou, err)
	}
	privKeyPem, err := certs.PrivateKeyToPem(key)
	if err != nil {
		return fmt.Errorf("failed to serialize private key to PEM: %w", err)
	}
	if secret.Data == nil {
		secret.Data = map[string][]byte{}
	}
	secret.Data[CASignerCertMapKey] = certs.CertToPem(crt)
	secret.Data[CASignerKeyMapKey] = privKeyPem
	return nil
}

func reconcileAggregateCA(configMap *corev1.ConfigMap, ownerRef config.OwnerRef, sources ...*corev1.Secret) error {
	ownerRef.ApplyTo(configMap)
	combined := &bytes.Buffer{}
	for _, src := range sources {
		ca_bytes := src.Data[CASignerCertMapKey]
		fmt.Fprintf(combined, "%s", string(ca_bytes))
	}
	if configMap.Data == nil {
		configMap.Data = map[string]string{}
	}
	configMap.Data[CASignerCertMapKey] = combined.String()
	return nil
}

func ReconcileRootCA(secret *corev1.Secret, ownerRef config.OwnerRef) error {
	return reconcileSelfSignedCA(secret, ownerRef, "root-ca", "openshift")
}

func ReconcileClusterSignerCA(secret *corev1.Secret, ownerRef config.OwnerRef) error {
	return reconcileSelfSignedCA(secret, ownerRef, "cluster-signer", "openshift")
}

func ReconcileCombinedCA(cm *corev1.ConfigMap, ownerRef config.OwnerRef, rootCA, signerCA *corev1.Secret) error {
	return reconcileAggregateCA(cm, ownerRef, rootCA, signerCA)
}
