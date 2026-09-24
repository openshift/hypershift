package util

import (
	"crypto/x509"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/support/certs"
)

// TestGenerateCustomCertificate verifies that our certificate generation works correctly
func TestGenerateCustomCertificate(t *testing.T) {
	testsCases := []struct {
		name       string
		dnsNames   []string
		duration   time.Duration
		wantErr    bool
		expectedCN string
	}{
		{
			name:       "When generating a certificate with DNS names it should succeed",
			dnsNames:   []string{"example.com", "test.example.com"},
			duration:   24 * time.Hour,
			wantErr:    false,
			expectedCN: "example.com",
		},
		{
			name:     "When generating a certificate with no DNS names it should fail",
			dnsNames: []string{},
			duration: 24 * time.Hour,
			wantErr:  true,
		},
		{
			name:       "When generating a certificate with zero duration it should succeed",
			dnsNames:   []string{"example.com"},
			duration:   0,
			wantErr:    false,
			expectedCN: "example.com",
		},
	}

	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			certPEM, keyPEM, err := GenerateCustomCertificate(tc.dnsNames, tc.duration)

			if tc.wantErr {
				g.Expect(err).To(HaveOccurred())
				return
			}

			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(certPEM).NotTo(BeNil())
			g.Expect(keyPEM).NotTo(BeNil())

			// Parse the certificate to verify its contents
			cert, err := certs.PemToCertificate(certPEM)
			g.Expect(err).NotTo(HaveOccurred())

			// Verify CommonName
			g.Expect(cert.Subject.CommonName).To(Equal(tc.expectedCN))

			// Verify DNS names
			if len(tc.dnsNames) == 0 {
				g.Expect(cert.DNSNames).To(BeEmpty())
			} else {
				g.Expect(cert.DNSNames).To(Equal(tc.dnsNames))
			}

			// Verify validity period
			if tc.duration > 0 {
				g.Expect(cert.NotAfter.Sub(cert.NotBefore)).To(Equal(tc.duration))
			}

			// Verify key usage
			g.Expect(cert.KeyUsage & x509.KeyUsageKeyEncipherment).NotTo(BeZero())
			g.Expect(cert.KeyUsage & x509.KeyUsageDigitalSignature).NotTo(BeZero())

			// Verify extended key usage
			g.Expect(cert.ExtKeyUsage).To(ContainElement(x509.ExtKeyUsageServerAuth))

			// Verify the private key can be parsed
			_, err = certs.PemToPrivateKey(keyPEM)
			g.Expect(err).NotTo(HaveOccurred())
		})
	}
}
