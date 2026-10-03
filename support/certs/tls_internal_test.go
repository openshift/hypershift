package certs

import (
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"testing"
	"time"
)

import corev1 "k8s.io/api/core/v1"

func TestValidateSignedCert(t *testing.T) {
	newCA := func(t *testing.T, commonName string) *corev1.Secret {
		t.Helper()

		ca := &corev1.Secret{}
		if err := ReconcileSelfSignedCA(ca, commonName, "test"); err != nil {
			t.Fatalf("reconciling CA: %v", err)
		}
		return ca
	}

	newLeaf := func(t *testing.T, ca *corev1.Secret) ([]byte, []byte) {
		t.Helper()

		caCert, caKey, err := decodeCA(ca, (&CAOpts{}).withDefaults())
		if err != nil {
			t.Fatalf("decoding CA: %v", err)
		}
		key, leaf, err := GenerateSignedCertificate(caKey, caCert, &CertCfg{
			Subject:      pkix.Name{CommonName: "test.example.com"},
			KeyUsages:    x509.KeyUsageDigitalSignature,
			ExtKeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			Validity:     ValidityOneYear,
			DNSNames:     []string{"test.example.com"},
		})
		if err != nil {
			t.Fatalf("generating leaf certificate: %v", err)
		}
		return PrivateKeyToPem(key), CertToPem(leaf)
	}

	newCAWithSameKeyDifferentSubject := func(t *testing.T, ca *corev1.Secret) *corev1.Secret {
		t.Helper()

		_, caKey, err := decodeCA(ca, (&CAOpts{}).withDefaults())
		if err != nil {
			t.Fatalf("decoding CA: %v", err)
		}
		certTemplate := &x509.Certificate{
			SerialNumber:          big.NewInt(2),
			Subject:               pkix.Name{CommonName: "replacement-ca"},
			NotBefore:             time.Now(),
			NotAfter:              time.Now().Add(ValidityTenYears),
			KeyUsage:              x509.KeyUsageCertSign,
			IsCA:                  true,
			BasicConstraintsValid: true,
		}
		certBytes, err := x509.CreateCertificate(rand.Reader, certTemplate, certTemplate, &caKey.PublicKey, caKey)
		if err != nil {
			t.Fatalf("generating replacement CA certificate: %v", err)
		}
		replacementCA, err := x509.ParseCertificate(certBytes)
		if err != nil {
			t.Fatalf("parsing replacement CA certificate: %v", err)
		}
		return &corev1.Secret{Data: map[string][]byte{
			CASignerCertMapKey: CertToPem(replacementCA),
		}}
	}

	currentCA := newCA(t, "current-ca")
	otherCA := newCA(t, "current-ca")
	validKey, validLeaf := newLeaf(t, currentCA)
	otherKey, otherLeaf := newLeaf(t, otherCA)
	replacementCA := newCAWithSameKeyDifferentSubject(t, currentCA)
	cfg := &CertCfg{
		Subject:      pkix.Name{CommonName: "test.example.com"},
		KeyUsages:    x509.KeyUsageDigitalSignature,
		ExtKeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		Validity:     ValidityOneYear,
		DNSNames:     []string{"test.example.com"},
	}

	testCases := []struct {
		name    string
		keyPEM  []byte
		leafPEM []byte
		ca      *corev1.Secret
		valid   bool
	}{
		{
			name:  "When the leaf certificate is missing, it should return an error",
			ca:    currentCA,
			valid: false,
		},
		{
			name:    "When the leaf certificate is malformed, it should return an error",
			keyPEM:  validKey,
			leafPEM: []byte("not a certificate"),
			ca:      currentCA,
			valid:   false,
		},
		{
			name:    "When the current CA certificate is malformed, it should return an error",
			keyPEM:  validKey,
			leafPEM: validLeaf,
			ca: &corev1.Secret{Data: map[string][]byte{
				CASignerCertMapKey: []byte("not a certificate"),
			}},
			valid: false,
		},
		{
			name:    "When the leaf certificate was signed by another CA with the same subject, it should return an error",
			keyPEM:  otherKey,
			leafPEM: otherLeaf,
			ca:      currentCA,
			valid:   false,
		},
		{
			name:    "When the current CA reuses a key with a different subject, it should return an error",
			keyPEM:  validKey,
			leafPEM: validLeaf,
			ca:      replacementCA,
			valid:   false,
		},
		{
			name:    "When the leaf certificate was signed by the current CA, it should not return an error",
			keyPEM:  validKey,
			leafPEM: validLeaf,
			ca:      currentCA,
			valid:   true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateSignedCert(tc.keyPEM, tc.leafPEM, cfg, 0, tc.ca, (&CAOpts{}).withDefaults())
			if (err == nil) != tc.valid {
				t.Errorf("expected valid=%t, got error: %v", tc.valid, err)
			}
		})
	}
}
