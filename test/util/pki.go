package util

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"time"
)

// GenerateTestCertificate creates a test certificate with the given DNS names and IP addresses
func GenerateTestCertificate(ctx context.Context, dnsNames []string, ipAddresses []string, duration time.Duration) ([]byte, []byte, error) {
	var cn string

	// Generate a private key
	if len(dnsNames) == 0 && len(ipAddresses) == 0 {
		return nil, nil, fmt.Errorf("no DNS names or IP addresses provided")
	}

	// set the common name to the first DNS name or ip address
	if len(dnsNames) > 0 {
		cn = dnsNames[0]
	} else {
		cn = ipAddresses[0]
	}

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}

	// Convert IP addresses to net.IP
	ips := make([]net.IP, len(ipAddresses))
	for i, ip := range ipAddresses {
		ips[i] = net.ParseIP(ip)
	}

	// Create certificate template
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"HostedControlPlanes TestOrg"},
			CommonName:   cn,
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(duration),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              dnsNames,
		IPAddresses:           ips,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
	}

	// Create the certificate
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create certificate: %w", err)
	}

	// Validate the certificate by parsing it
	_, err = x509.ParseCertificate(certDER)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse generated certificate: %w", err)
	}

	// Convert private key to PEM format
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	})

	// Convert certificate to PEM format
	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certDER,
	})

	// Add newline to both PEM blocks
	privateKeyPEM = append(privateKeyPEM, '\n')
	certPEM = append(certPEM, '\n')

	return certPEM, privateKeyPEM, nil
}
