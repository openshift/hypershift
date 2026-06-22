package util

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"

	"github.com/openshift/hypershift/support/certs"

	"golang.org/x/crypto/ssh"
)

func GenerateSSHKeys() ([]byte, []byte, error) {
	privateKey, err := rsa.GenerateKey(certs.Reader(), 4096)
	if err != nil {
		return nil, nil, err
	}
	privateDER := x509.MarshalPKCS1PrivateKey(privateKey)
	privatePEMBlock := pem.Block{
		Type:    "RSA PRIVATE KEY",
		Headers: nil,
		Bytes:   privateDER,
	}
	privatePEM := pem.EncodeToMemory(&privatePEMBlock)

	publicRSAKey, err := ssh.NewPublicKey(&privateKey.PublicKey)
	if err != nil {
		return nil, nil, err
	}
	publicBytes := ssh.MarshalAuthorizedKey(publicRSAKey)

	return publicBytes, privatePEM, nil
}
