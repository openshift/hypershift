package pki

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/openshift/hypershift/certs"
)

func ReconcileRootCA(rootCASecret *corev1.Secret) error {
	rootCASecret.Type = corev1.SecretTypeOpaque
	if !hasKeys(rootCASecret, CASignerKeyMapKey, CASignerKeyMapKey) {
		cfg := &certs.CertCfg{
			Subject:   pkix.Name{CommonName: "root-ca", OrganizationalUnit: []string{"openshift"}},
			KeyUsages: x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
			Validity:  certs.ValidityTenYears,
			IsCA:      true,
		}

		key, crt, err := certs.GenerateSelfSignedCertificate(cfg)
		if err != nil {
			return fmt.Errorf("failed to generate root CA: %w", err)
		}
		if rootCASecret.Data == nil {
			rootCASecret.Data = map[string][]byte{}
		}
		rootCASecret.Data[CASignerCertMapKey] = certs.CertToPem(crt)
		rootCASecret.Data[CASignerKeyMapKey] = certs.PrivateKeyToPem(key)
	}
	return nil
}
