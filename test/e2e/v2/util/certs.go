package util

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"time"

	"github.com/openshift/hypershift/support/certs"
)

// GenerateCASignedCertificate generates a CA key/cert pair and a leaf certificate
// signed by that CA for the given DNS names. Returns (caPEM, leafChainPEM, leafKeyPEM, error)
// where leafChainPEM contains the leaf followed by the CA certificate.
func GenerateCASignedCertificate(dnsNames []string, validity time.Duration) (caPEM, leafChainPEM, leafKeyPEM []byte, err error) {
	if len(dnsNames) == 0 {
		return nil, nil, nil, fmt.Errorf("no DNS names provided")
	}

	caCfg := &certs.CertCfg{
		Subject:   pkix.Name{CommonName: "e2e-test-ca", Organization: []string{"hypershift-e2e"}},
		KeyUsages: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		Validity:  validity,
		IsCA:      true,
	}
	caKey, caCert, err := certs.GenerateSelfSignedCertificate(caCfg)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to generate CA: %w", err)
	}

	leafCfg := &certs.CertCfg{
		Subject:      pkix.Name{CommonName: dnsNames[0], Organization: []string{"hypershift-e2e"}},
		KeyUsages:    x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		Validity:     validity,
		DNSNames:     dnsNames,
		IsCA:         false,
	}
	leafKey, leafCert, err := certs.GenerateSignedCertificate(caKey, caCert, leafCfg)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to generate leaf certificate: %w", err)
	}

	caPEM = certs.CertToPem(caCert)
	leafPEM := certs.CertToPem(leafCert)
	leafChainPEM = append(leafPEM, caPEM...)
	leafKeyPEM = certs.PrivateKeyToPem(leafKey)
	return caPEM, leafChainPEM, leafKeyPEM, nil
}

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
