package util

import (
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"net"

	"github.com/pkg/errors"
)

func GenerateCert(commonName, organization string, hostNames, addresses []string, ca *CA) (*Cert, error) {
	ipAddr := []net.IP{}
	for _, ip := range addresses {
		ipAddr = append(ipAddr, net.ParseIP(ip))
	}
	cfg := &CertCfg{
		Subject:      pkix.Name{CommonName: commonName, Organization: []string{organization}},
		KeyUsages:    x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		Validity:     ValidityOneYear,
		DNSNames:     hostNames,
		IPAddresses:  ipAddr,
	}
	key, crt, err := GenerateSignedCertificate(ca.Key, ca.Cert, cfg)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to generate signed certificate for cn=%s,o=%s", commonName, organization)
	}
	return &Cert{
		Parent: ca,
		Key:    key,
		Cert:   crt,
	}, nil
}

type Cert struct {
	Parent *CA
	Key    *rsa.PrivateKey
	Cert   *x509.Certificate
}

func (c *Cert) Serialize() ([]byte, []byte) {
	certBytes := CertToPem(c.Cert)
	keyBytes := PrivateKeyToPem(c.Key)
	return certBytes, keyBytes
}
