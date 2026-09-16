package util

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"time"

	"github.com/openshift/hypershift/support/certs"
)

// GenerateCustomCertificate generates a self-signed certificate for the given DNS names.
func GenerateCustomCertificate(dnsNames []string, validity time.Duration) ([]byte, []byte, error) {
	if len(dnsNames) == 0 {
		return nil, nil, fmt.Errorf("no DNS names provided")
	}

	cfg := &certs.CertCfg{
		Subject:      pkix.Name{CommonName: dnsNames[0], Organization: []string{"kubernetes"}, OrganizationalUnit: []string{"test"}},
		KeyUsages:    x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		Validity:     validity,
		DNSNames:     dnsNames,
		IsCA:         false,
	}

	key, crt, err := certs.GenerateSelfSignedCertificate(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate self-signed certificate: %w", err)
	}

	return certs.CertToPem(crt), certs.PrivateKeyToPem(key), nil
}
