package util

import (
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/support/certs"
)

func TestGenerateCASignedCertificate(t *testing.T) {
	g := NewWithT(t)

	const (
		dnsName  = "api.example.com"
		validity = time.Hour
	)

	caPEM, leafChainPEM, leafKeyPEM, err := GenerateCASignedCertificate([]string{dnsName}, validity)
	g.Expect(err).NotTo(HaveOccurred(), "generating a CA-signed certificate must succeed")
	g.Expect(caPEM).NotTo(BeEmpty())
	g.Expect(leafChainPEM).NotTo(BeEmpty())
	g.Expect(leafKeyPEM).NotTo(BeEmpty())

	caCerts := parseTestCertificates(t, caPEM)
	g.Expect(caCerts).To(HaveLen(1))
	g.Expect(caCerts[0].IsCA).To(BeTrue(), "generated CA must be marked as a CA")
	g.Expect(caCerts[0].KeyUsage&x509.KeyUsageCertSign).NotTo(BeZero(), "generated CA must be able to sign certificates")

	chain := parseTestCertificates(t, leafChainPEM)
	g.Expect(chain).To(HaveLen(2), "leaf chain must contain the leaf followed by the CA")
	leaf, ca := chain[0], chain[1]
	g.Expect(leaf.IsCA).To(BeFalse(), "leaf certificate must not be a CA")
	g.Expect(leaf.DNSNames).To(ConsistOf(dnsName))
	g.Expect(leaf.NotAfter.Sub(leaf.NotBefore)).To(Equal(validity))

	leafKey, err := certs.PemToPrivateKey(leafKeyPEM)
	g.Expect(err).NotTo(HaveOccurred(), "leaf private key must be parseable")
	g.Expect(leaf.PublicKey).To(Equal(leafKey.Public()), "leaf private key must match the leaf certificate")

	roots := x509.NewCertPool()
	roots.AddCert(ca)
	_, err = leaf.Verify(x509.VerifyOptions{
		DNSName:   dnsName,
		Roots:     roots,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	g.Expect(err).NotTo(HaveOccurred(), "leaf certificate must verify against the generated CA")
}

func parseTestCertificates(t *testing.T, data []byte) []*x509.Certificate {
	t.Helper()

	var parsed []*x509.Certificate
	for len(data) > 0 {
		var block *pem.Block
		block, data = pem.Decode(data)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			t.Fatalf("failed to parse certificate: %v", err)
		}
		parsed = append(parsed, cert)
	}
	return parsed
}
