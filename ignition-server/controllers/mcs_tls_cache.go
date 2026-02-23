package controllers

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"sync"
	"time"

	"github.com/openshift/hypershift/support/certs"
)

const (
	// mcsCertSafetyMargin is subtracted from the certificate's NotAfter time
	// to ensure we regenerate the certificate before it actually expires.
	mcsCertSafetyMargin = 1 * time.Hour
)

// mcsTLSCache caches the MCS TLS certificate and key PEM bytes
// to avoid regenerating them on every ignition payload request.
// The cached certificate is reused as long as it has not expired
// (with a safety margin applied). When the certificate is expired
// or missing, a new one is generated and cached.
type mcsTLSCache struct {
	certPEM []byte
	keyPEM  []byte
	expiry  time.Time
	mu      sync.Mutex

	// nowFn returns the current time. It defaults to time.Now
	// and can be overridden in tests.
	nowFn func() time.Time
}

// NewMCSTLSCache creates a new mcsTLSCache with an empty cache.
func NewMCSTLSCache() *mcsTLSCache {
	return &mcsTLSCache{
		nowFn: time.Now,
	}
}

// getOrGenerate returns cached MCS TLS certificate and key PEM bytes if
// the cached certificate is still valid. If the cache is empty or the
// cached certificate has expired (or is within the safety margin of
// expiring), a new self-signed certificate is generated, cached, and returned.
func (c *mcsTLSCache) getOrGenerate() (certPEM []byte, keyPEM []byte, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.nowFn()
	if c.certPEM != nil && now.Before(c.expiry.Add(-mcsCertSafetyMargin)) {
		return c.certPEM, c.keyPEM, nil
	}

	cfg := &certs.CertCfg{
		Subject:   pkix.Name{CommonName: "machine-config-server", OrganizationalUnit: []string{"openshift"}},
		KeyUsages: x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		Validity:  certs.ValidityOneDay,
		IsCA:      true,
	}
	key, crt, err := certs.GenerateSelfSignedCertificate(cfg)
	if err != nil {
		return nil, nil, err
	}

	c.certPEM = certs.CertToPem(crt)
	c.keyPEM = certs.PrivateKeyToPem(key)
	c.expiry = crt.NotAfter

	return c.certPEM, c.keyPEM, nil
}
