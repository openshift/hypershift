package mirrorregistry

import (
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"strings"
	"time"

	"github.com/openshift/hypershift/support/certs"

	"k8s.io/apimachinery/pkg/util/sets"
)

const (
	caCommonName          = "hypershift-e2e-mirror-registry-ca"
	servingCertificateTTL = 30 * 24 * time.Hour
)

func generateCA() (KeyPair, error) {
	key, certificate, err := certs.GenerateSelfSignedCertificate(&certs.CertCfg{
		KeyUsages: x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		Subject: pkix.Name{
			CommonName:         caCommonName,
			OrganizationalUnit: []string{"hypershift-e2e-mirror-registry"},
		},
		Validity: certs.ValidityOneYear,
		IsCA:     true,
	})
	if err != nil {
		return KeyPair{}, fmt.Errorf("generate mirror registry CA: %w", err)
	}
	return KeyPair{
		CertPEM: certs.CertToPem(certificate),
		KeyPEM:  certs.PrivateKeyToPem(key),
	}, nil
}

func servingCert(ca KeyPair, cn string, dnsNames []string) (KeyPair, error) {
	if len(cn) == 0 {
		return KeyPair{}, fmt.Errorf("serving certificate common name must not be empty")
	}
	if len(cn) > 64 {
		return KeyPair{}, fmt.Errorf("serving certificate common name %q exceeds the 64-character RFC 5280 limit", cn)
	}
	caKey, caCertificate, err := parseCA(ca)
	if err != nil {
		return KeyPair{}, err
	}
	validity := servingCertificateTTL
	// Leave time for serving-key generation so the new certificate cannot outlive its CA.
	caValidityRemaining := time.Until(caCertificate.NotAfter) - time.Minute
	if caValidityRemaining < validity {
		validity = caValidityRemaining
	}
	if validity <= 0 {
		return KeyPair{}, fmt.Errorf("mirror registry CA certificate expires too soon to issue a serving certificate")
	}
	key, certificate, err := certs.GenerateSignedCertificate(caKey, caCertificate, &certs.CertCfg{
		DNSNames:     dnsNames,
		ExtKeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		KeyUsages:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		Subject: pkix.Name{
			CommonName:         cn,
			OrganizationalUnit: []string{"hypershift-e2e-mirror-registry"},
		},
		Validity: validity,
	})
	if err != nil {
		return KeyPair{}, fmt.Errorf("generate mirror registry serving certificate: %w", err)
	}
	return KeyPair{
		CertPEM: certs.CertToPem(certificate),
		KeyPEM:  certs.PrivateKeyToPem(key),
	}, nil
}

func tlsMaterial(opts Options, host string) (ca KeyPair, serving KeyPair, err error) {
	opts = opts.withDefaults()
	candidates := make([]string, 0, 1+len(serviceDNSNames(opts.Name, opts.Namespace))+len(opts.ExtraDNSNames))
	candidates = append(candidates, host)
	candidates = append(candidates, serviceDNSNames(opts.Name, opts.Namespace)...)
	candidates = append(candidates, opts.ExtraDNSNames...)

	names := make([]string, 0, len(candidates))
	seen := sets.New[string]()
	for _, name := range candidates {
		trimmedName := strings.TrimSpace(name)
		if trimmedName == "" {
			continue
		}
		if trimmedName != name {
			return KeyPair{}, KeyPair{}, fmt.Errorf("mirror registry DNS name %q has leading or trailing whitespace", name)
		}
		if seen.Has(name) {
			continue
		}
		seen.Insert(name)
		names = append(names, name)
	}

	if opts.CA == nil {
		return KeyPair{}, KeyPair{}, fmt.Errorf("mirror registry CA must be resolved before preparing TLS material")
	}
	ca = *opts.CA
	// servingCert validates the CA before signing; do not parse it twice here.
	serving, err = servingCert(ca, opts.Name, names)
	if err != nil {
		return KeyPair{}, KeyPair{}, err
	}
	return ca, serving, nil
}

func parseCA(ca KeyPair) (*rsa.PrivateKey, *x509.Certificate, error) {
	key, err := parsePrivateKey(ca.KeyPEM)
	if err != nil {
		return nil, nil, fmt.Errorf("parse mirror registry CA private key: %w", err)
	}
	certificate, err := certs.PemToCertificate(ca.CertPEM)
	if err != nil {
		return nil, nil, fmt.Errorf("parse mirror registry CA certificate: %w", err)
	}
	publicKey, ok := certificate.PublicKey.(*rsa.PublicKey)
	if !ok || publicKey.N.Cmp(key.N) != 0 || publicKey.E != key.E {
		return nil, nil, fmt.Errorf("mirror registry CA certificate does not match its RSA private key")
	}
	if !certificate.IsCA || !certificate.BasicConstraintsValid || certificate.KeyUsage&x509.KeyUsageCertSign == 0 {
		return nil, nil, fmt.Errorf("mirror registry CA certificate is not valid for certificate signing")
	}
	now := time.Now()
	if now.Before(certificate.NotBefore) || now.After(certificate.NotAfter) {
		return nil, nil, fmt.Errorf("mirror registry CA certificate is not currently valid")
	}
	return key, certificate, nil
}

func parsePrivateKey(keyPEM []byte) (*rsa.PrivateKey, error) {
	key, err := certs.PemToPrivateKey(keyPEM)
	if err == nil {
		return key, nil
	}
	block, _ := pem.Decode(keyPEM)
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, err
	}
	parsedKey, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse PKCS#8 private key: %w", err)
	}
	rsaKey, ok := parsedKey.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("PKCS#8 private key has type %T, expected RSA", parsedKey)
	}
	return rsaKey, nil
}
