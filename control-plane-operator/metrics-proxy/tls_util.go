package metricsproxy

import (
	"crypto/x509"
	"os"
)

// loadCertPool reads a PEM-encoded CA certificate file and returns a cert pool.
// Returns nil if the file path is empty, the file cannot be read (e.g. volume
// not yet mounted), or the file contains no valid PEM certificates.
func loadCertPool(caFile string) *x509.CertPool {
	if caFile == "" {
		return nil
	}
	caPEM, err := os.ReadFile(caFile)
	if err != nil {
		return nil
	}
	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caPEM) {
		return nil
	}
	return caPool
}
