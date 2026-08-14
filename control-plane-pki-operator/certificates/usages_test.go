package certificates

import (
	"crypto/x509"
	"fmt"
	"testing"

	certificatesv1 "k8s.io/api/certificates/v1"

	"github.com/google/go-cmp/cmp"
)

func TestKeyUsagesFromStrings(t *testing.T) {
	testcases := []struct {
		usages              []certificatesv1.KeyUsage
		expectedKeyUsage    x509.KeyUsage
		expectedExtKeyUsage []x509.ExtKeyUsage
		expectErr           bool
	}{
		{
			usages:              []certificatesv1.KeyUsage{"signing"},
			expectedKeyUsage:    x509.KeyUsageDigitalSignature,
			expectedExtKeyUsage: []x509.ExtKeyUsage{},
			expectErr:           false,
		},
		{
			usages:              []certificatesv1.KeyUsage{"client auth"},
			expectedKeyUsage:    0,
			expectedExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
			expectErr:           false,
		},
		{
			usages:              []certificatesv1.KeyUsage{"client auth", "client auth"},
			expectedKeyUsage:    0,
			expectedExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
			expectErr:           false,
		},
		{
			usages:              []certificatesv1.KeyUsage{"cert sign", "encipher only"},
			expectedKeyUsage:    x509.KeyUsageCertSign | x509.KeyUsageEncipherOnly,
			expectedExtKeyUsage: []x509.ExtKeyUsage{},
			expectErr:           false,
		},
		{
			usages:              []certificatesv1.KeyUsage{"ocsp signing", "crl sign", "s/mime", "content commitment"},
			expectedKeyUsage:    x509.KeyUsageCRLSign | x509.KeyUsageContentCommitment,
			expectedExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageEmailProtection, x509.ExtKeyUsageOCSPSigning},
			expectErr:           false,
		},
		{
			usages:              []certificatesv1.KeyUsage{"unsupported string"},
			expectedKeyUsage:    0,
			expectedExtKeyUsage: nil,
			expectErr:           true,
		},
	}

	for _, tc := range testcases {
		t.Run(fmt.Sprint(tc.usages), func(t *testing.T) {
			ku, eku, err := KeyUsagesFromStrings(tc.usages)

			if tc.expectErr {
				if err == nil {
					t.Errorf("did not return an error, but expected one")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			if keyUsageDiff := cmp.Diff(ku, tc.expectedKeyUsage); keyUsageDiff != "" {
				t.Errorf("got incorrect key usage: %v", keyUsageDiff)
			}
			if extKeyUsageDiff := cmp.Diff(eku, tc.expectedExtKeyUsage); extKeyUsageDiff != "" {
				t.Errorf("got incorrect ext key usage: %v", extKeyUsageDiff)
			}
		})
	}
}
