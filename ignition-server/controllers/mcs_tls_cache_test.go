package controllers

import (
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"

	. "github.com/onsi/gomega"
)

func TestMCSTLSCache(t *testing.T) {
	testCases := []struct {
		name        string
		setupCache  func(c *mcsTLSCache)
		nowFn       func() time.Time
		expectReuse bool
	}{
		{
			name:        "When cache is empty, it should generate a new certificate",
			setupCache:  func(c *mcsTLSCache) {},
			expectReuse: false,
		},
		{
			name: "When cache has a valid cert, it should reuse it",
			setupCache: func(c *mcsTLSCache) {
				// Pre-populate cache
				_, _, _ = c.getOrGenerate()
			},
			expectReuse: true,
		},
		{
			name: "When cached cert is expired, it should generate a new certificate",
			setupCache: func(c *mcsTLSCache) {
				// Pre-populate cache
				_, _, _ = c.getOrGenerate()
				// Move time forward past expiry
				c.nowFn = func() time.Time {
					return time.Now().Add(25 * time.Hour)
				}
			},
			expectReuse: false,
		},
		{
			name: "When cached cert is within safety margin of expiry, it should generate a new certificate",
			setupCache: func(c *mcsTLSCache) {
				// Pre-populate cache
				_, _, _ = c.getOrGenerate()
				// Move time forward to within 30 minutes of expiry (inside 1 hour safety margin)
				c.nowFn = func() time.Time {
					return c.expiry.Add(-30 * time.Minute)
				}
			},
			expectReuse: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			cache := NewMCSTLSCache()
			tc.setupCache(cache)

			// Capture existing cert before the call if we expect reuse
			existingCertPEM := cache.certPEM

			certPEM, keyPEM, err := cache.getOrGenerate()
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(certPEM).NotTo(BeEmpty())
			g.Expect(keyPEM).NotTo(BeEmpty())

			// Verify the PEM data is valid
			certBlock, _ := pem.Decode(certPEM)
			g.Expect(certBlock).NotTo(BeNil(), "cert PEM should decode")
			g.Expect(certBlock.Type).To(Equal("CERTIFICATE"))

			keyBlock, _ := pem.Decode(keyPEM)
			g.Expect(keyBlock).NotTo(BeNil(), "key PEM should decode")
			g.Expect(keyBlock.Type).To(Equal("RSA PRIVATE KEY"))

			// Parse and verify the certificate properties
			cert, err := x509.ParseCertificate(certBlock.Bytes)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(cert.Subject.CommonName).To(Equal("machine-config-server"))
			g.Expect(cert.Subject.OrganizationalUnit).To(Equal([]string{"openshift"}))
			g.Expect(cert.IsCA).To(BeTrue())

			if tc.expectReuse {
				g.Expect(certPEM).To(Equal(existingCertPEM), "expected cached cert to be reused")
			} else if existingCertPEM != nil {
				g.Expect(certPEM).NotTo(Equal(existingCertPEM), "expected a new cert to be generated")
			}
		})
	}
}

func TestMCSTLSCacheGetOrGenerate_WhenCalledMultipleTimes_ItShouldReturnSameCert(t *testing.T) {
	g := NewWithT(t)

	cache := NewMCSTLSCache()

	cert1, key1, err := cache.getOrGenerate()
	g.Expect(err).NotTo(HaveOccurred())

	cert2, key2, err := cache.getOrGenerate()
	g.Expect(err).NotTo(HaveOccurred())

	g.Expect(cert1).To(Equal(cert2), "consecutive calls should return the same cached cert")
	g.Expect(key1).To(Equal(key2), "consecutive calls should return the same cached key")
}

func TestMCSTLSCacheGetOrGenerate_WhenCertExpiryIsAfterSafetyMargin_ItShouldReuseCert(t *testing.T) {
	g := NewWithT(t)

	cache := NewMCSTLSCache()

	cert1, _, err := cache.getOrGenerate()
	g.Expect(err).NotTo(HaveOccurred())

	// Move time forward but still outside safety margin (e.g., 20 hours into a 24h cert with 1h safety)
	cache.nowFn = func() time.Time {
		return time.Now().Add(20 * time.Hour)
	}

	cert2, _, err := cache.getOrGenerate()
	g.Expect(err).NotTo(HaveOccurred())

	g.Expect(cert1).To(Equal(cert2), "cert should be reused when still valid outside safety margin")
}
