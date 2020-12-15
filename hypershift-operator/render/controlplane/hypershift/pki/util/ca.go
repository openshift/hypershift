package util

import (
	"bytes"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"

	"github.com/pkg/errors"
)

type CA struct {
	Key  *rsa.PrivateKey
	Cert *x509.Certificate
}

type CAList []*CA

// GenerateCA generates a CA key pair with the given filename
func GenerateCA(commonName, organizationalUnit string) (*CA, error) {
	cfg := &CertCfg{
		Subject:      pkix.Name{CommonName: commonName, OrganizationalUnit: []string{organizationalUnit}},
		KeyUsages:    x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		Validity:     ValidityTenYears,
		IsCA:         true,
	}

	key, crt, err := GenerateSelfSignedCertificate(cfg)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to generate CA with cn=%s,ou=%s", commonName, organizationalUnit)
	}
	return &CA{Key: key, Cert: crt}, nil
}

func (c *CA) Serialize() ([]byte, []byte) {
	return CertToPem(c.Cert), PrivateKeyToPem(c.Key)
}

func (l CAList) Serialize() []byte {
	var allBytes [][]byte
	for _, ca := range l {
		allBytes = append(allBytes, CertToPem(ca.Cert))
	}
	return bytes.Join(allBytes, []byte(""))
}
