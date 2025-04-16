package util

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/go-logr/logr/testr"
)

func TestGenerateTestCertificate(t *testing.T) {
	ctx := log.IntoContext(context.Background(), testr.New(t))

	testsCases := []struct {
		name        string
		dnsNames    []string
		ipAddresses []string
		duration    time.Duration
		wantErr     bool
		expectedCN  string
	}{
		{
			name:        "When generating a certificate with DNS names it should succeed",
			dnsNames:    []string{"example.com", "test.example.com"},
			ipAddresses: []string{"192.168.1.1"},
			duration:    24 * time.Hour,
			wantErr:     false,
			expectedCN:  "example.com",
		},
		{
			name:        "When generating a certificate with IP addresses only it should succeed",
			dnsNames:    []string{},
			ipAddresses: []string{"192.168.1.1", "10.0.0.1"},
			duration:    24 * time.Hour,
			wantErr:     false,
			expectedCN:  "192.168.1.1",
		},
		{
			name:        "When generating a certificate with no DNS names or IP addresses it should fail",
			dnsNames:    []string{},
			ipAddresses: []string{},
			duration:    24 * time.Hour,
			wantErr:     true,
		},
		{
			name:        "When generating a certificate with invalid IP address it should fail",
			dnsNames:    []string{},
			ipAddresses: []string{"invalid.ip.address"},
			duration:    24 * time.Hour,
			wantErr:     true,
		},
		{
			name:        "When generating a certificate with zero duration it should succeed",
			dnsNames:    []string{"example.com"},
			ipAddresses: []string{"192.168.1.1"},
			duration:    0,
			wantErr:     false,
			expectedCN:  "example.com",
		},
	}

	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			certPEM, keyPEM, err := GenerateTestCertificate(ctx, tc.dnsNames, tc.ipAddresses, tc.duration)

			if tc.wantErr {
				g.Expect(err).To(HaveOccurred())
				return
			}

			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(certPEM).NotTo(BeNil())
			g.Expect(keyPEM).NotTo(BeNil())

			// Verify PEM blocks have newlines at the end
			g.Expect(certPEM[len(certPEM)-1]).To(Equal(byte('\n')))
			g.Expect(keyPEM[len(keyPEM)-1]).To(Equal(byte('\n')))

			// Parse the certificate to verify its contents
			block, _ := pem.Decode(certPEM)
			g.Expect(block).NotTo(BeNil())
			g.Expect(block.Type).To(Equal("CERTIFICATE"))

			cert, err := x509.ParseCertificate(block.Bytes)
			g.Expect(err).NotTo(HaveOccurred())

			// Verify CommonName
			g.Expect(cert.Subject.CommonName).To(Equal(tc.expectedCN))

			// Verify DNS names
			if len(tc.dnsNames) == 0 {
				g.Expect(cert.DNSNames).To(BeEmpty())
			} else {
				g.Expect(cert.DNSNames).To(Equal(tc.dnsNames))
			}

			// Verify IP addresses
			if len(tc.ipAddresses) > 0 {
				g.Expect(cert.IPAddresses).To(HaveLen(len(tc.ipAddresses)))
			}

			// Verify certificate is self-signed
			g.Expect(cert.Subject.CommonName).To(Equal(cert.Issuer.CommonName))
			g.Expect(cert.Subject.Organization).To(Equal(cert.Issuer.Organization))

			// Verify certificate is a CA
			g.Expect(cert.IsCA).To(BeTrue())
			g.Expect(cert.MaxPathLen).To(Equal(0))
			g.Expect(cert.MaxPathLenZero).To(BeTrue())

			// Verify key usage
			expectedKeyUsage := x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature
			g.Expect(cert.KeyUsage).To(Equal(expectedKeyUsage))

			// Verify extended key usage
			g.Expect(cert.ExtKeyUsage).To(Equal([]x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}))

			// Verify validity period
			if tc.duration > 0 {
				g.Expect(time.Now()).To(BeTemporally("<", cert.NotAfter))
				g.Expect(time.Now()).To(BeTemporally(">", cert.NotBefore))
			}

			// Verify private key PEM block
			keyBlock, _ := pem.Decode(keyPEM)
			g.Expect(keyBlock).NotTo(BeNil())
			g.Expect(keyBlock.Type).To(Equal("RSA PRIVATE KEY"))
		})
	}
}
