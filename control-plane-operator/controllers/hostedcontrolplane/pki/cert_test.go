package pki

import (
	"bytes"
	"crypto/x509"
	"crypto/x509/pkix"
	"net"
	"testing"
	"time"

	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/config"

	corev1 "k8s.io/api/core/v1"

	"github.com/google/go-cmp/cmp"
)

func TestReconcileSignedCertWithKeysAndAddresses(t *testing.T) {
	t.Parallel()
	caCfg := certs.CertCfg{IsCA: true, Subject: pkix.Name{CommonName: "root-ca", OrganizationalUnit: []string{"ou"}}}
	caKey, caCert, err := certs.GenerateSelfSignedCertificate(&caCfg)
	if err != nil {
		t.Fatalf("failed go generate CA: %v", err)
	}

	caSecret := &corev1.Secret{
		Data: map[string][]byte{
			certs.CASignerCertMapKey: certs.CertToPem(caCert),
			certs.CASignerKeyMapKey:  certs.PrivateKeyToPem(caKey),
		},
	}

	testCases := []struct {
		name         string
		secret       func() (*corev1.Secret, error)
		expectUpdate bool
	}{
		{
			name: "Valid secret, no change",
			secret: func() (*corev1.Secret, error) {
				cfg := &certs.CertCfg{
					Subject:      pkix.Name{CommonName: "foo", Organization: []string{"org"}},
					KeyUsages:    x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
					ExtKeyUsages: X509UsageServerAuth,
					Validity:     certs.ValidityOneYear,
					DNSNames:     []string{"foo.svc.local"},
					IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
				}
				key, cert, err := certs.GenerateSignedCertificate(caKey, caCert, cfg)
				if err != nil {
					return nil, err
				}
				return &corev1.Secret{
					Data: map[string][]byte{
						corev1.TLSPrivateKeyKey: certs.PrivateKeyToPem(key),
						corev1.TLSCertKey:       certs.CertToPem(cert),
					},
				}, nil
			},
			expectUpdate: false,
		},
		{
			name: "Expires in one day, cert is re-generated",
			secret: func() (*corev1.Secret, error) {
				cfg := &certs.CertCfg{
					Subject:      pkix.Name{CommonName: "foo", Organization: []string{"org"}},
					KeyUsages:    x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
					ExtKeyUsages: X509UsageServerAuth,
					Validity:     24 * time.Hour,
					DNSNames:     []string{"foo.svc.local"},
					IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
				}
				key, cert, err := certs.GenerateSignedCertificate(caKey, caCert, cfg)
				if err != nil {
					return nil, err
				}
				return &corev1.Secret{
					Data: map[string][]byte{
						corev1.TLSPrivateKeyKey: certs.PrivateKeyToPem(key),
						corev1.TLSCertKey:       certs.CertToPem(cert),
					},
				}, nil
			},
			expectUpdate: true,
		},
		{
			name: "Empty secret gets filled",
			secret: func() (*corev1.Secret, error) {
				return &corev1.Secret{}, nil
			},
			expectUpdate: true,
		},
		{
			name: "Garbage entries get replaced",
			secret: func() (*corev1.Secret, error) {
				return &corev1.Secret{
					Data: map[string][]byte{
						corev1.TLSCertKey:       []byte("not a cert"),
						corev1.TLSPrivateKeyKey: []byte("not a key"),
					},
				}, nil
			},
			expectUpdate: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			secret, err := tc.secret()
			if err != nil {
				t.Fatalf("failed to generate secret: %v", err)
			}
			initialKey, initalCert := secret.Data[corev1.TLSPrivateKeyKey], secret.Data[corev1.TLSCertKey]

			if err := reconcileSignedCertWithKeysAndAddresses(secret, caSecret, config.OwnerRef{}, "foo", []string{"org"}, X509UsageServerAuth, corev1.TLSCertKey, corev1.TLSPrivateKeyKey, certs.CASignerCertMapKey, []string{"foo.svc.local"}, []string{"127.0.0.1"}, ""); err != nil {
				t.Fatalf("reconcileSignedCertWithKeysAndAddresses failed: %v", err)
			}

			didUpdate := !bytes.Equal(initialKey, secret.Data[corev1.TLSPrivateKeyKey]) && !bytes.Equal(initalCert, secret.Data[corev1.TLSCertKey])
			if didUpdate != tc.expectUpdate {
				t.Errorf("expectUpdate: %t differs froma actual %t", tc.expectUpdate, didUpdate)
			}

			if !certs.HasCAHash(secret, caSecret, &certs.CAOpts{}) {
				t.Error("secret doesn't have ca hash")
			}

			if diff := cmp.Diff(string(secret.Data[certs.CASignerCertMapKey]), string(caSecret.Data[certs.CASignerCertMapKey])); diff != "" {
				t.Errorf("Cacert differs from expected: %s", diff)
			}
		})
	}
}
