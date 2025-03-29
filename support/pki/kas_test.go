package pki

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGetSANsFromCertificate(t *testing.T) {
	// Create a test certificate with known SANs
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Test Org"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(time.Hour * 24),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames: []string{
			"test.example.com",
			"*.test.example.com",
		},
		IPAddresses: []net.IP{
			net.ParseIP("192.168.1.1"),
			net.ParseIP("2001:db8::1"),
		},
	}

	// Generate a private key
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	// Create the certificate
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	require.NoError(t, err)

	// Test cases
	testCases := []struct {
		name          string
		cert          []byte
		expectedDNS   []string
		expectedIPs   []string
		expectedError bool
	}{
		{
			name:        "valid certificate",
			cert:        certDER,
			expectedDNS: []string{"test.example.com", "*.test.example.com"},
			expectedIPs: []string{"192.168.1.1", "2001:db8::1"},
		},
		{
			name:          "invalid certificate",
			cert:          []byte("invalid certificate"),
			expectedError: true,
		},
		{
			name:          "empty certificate",
			cert:          []byte{},
			expectedError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			dnsNames, ipAddresses, err := GetSANsFromCertificate(tc.cert)

			if tc.expectedError {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.ElementsMatch(t, tc.expectedDNS, dnsNames, "DNS names don't match")
			require.ElementsMatch(t, tc.expectedIPs, ipAddresses, "IP addresses don't match")
		})
	}
}
