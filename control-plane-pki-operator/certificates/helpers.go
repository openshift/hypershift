package certificates

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
)

// The following code comes from core Kube, which can't be imported, unfortunately. These methods are copied from:
// https://github.com/kubernetes/kubernetes/blob/ec5096fa869b801d6eb1bf019819287ca61edc4d/pkg/apis/certificates/v1/helpers.go#L25-L37

// ParseCSR decodes a PEM encoded CSR
func ParseCSR(pemBytes []byte) (*x509.CertificateRequest, error) {
	// extract PEM from request object
	block, _ := pem.Decode(pemBytes)
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		return nil, errors.New("PEM block type must be CERTIFICATE REQUEST")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, err
	}
	return csr, nil
}
