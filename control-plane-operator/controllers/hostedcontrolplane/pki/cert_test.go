package pki

import (
	"bytes"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/config"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func rsaRootCA() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "rsa",
		},
		Data: map[string][]byte{
			CASignerCertMapKey: []byte(`-----BEGIN CERTIFICATE-----
MIIDizCCAnOgAwIBAgIUU8MSwNvrwDfka3vMf67IGWxK3zYwDQYJKoZIhvcNAQEL
BQAwVDELMAkGA1UEBhMCWFgxFTATBgNVBAcMDERlZmF1bHQgQ2l0eTEcMBoGA1UE
CgwTRGVmYXVsdCBDb21wYW55IEx0ZDEQMA4GA1UEAwwHcm9vdC1jYTAgFw0yMjAz
MTgwMDM5MzZaGA8yMTIyMDIyMjAwMzkzNlowVDELMAkGA1UEBhMCWFgxFTATBgNV
BAcMDERlZmF1bHQgQ2l0eTEcMBoGA1UECgwTRGVmYXVsdCBDb21wYW55IEx0ZDEQ
MA4GA1UEAwwHcm9vdC1jYTCCASIwDQYJKoZIhvcNAQEBBQADggEPADCCAQoCggEB
ALNltzcqG+vJ0wK9FNuNgavGayfiO9nMh1Bl49ybO356XCVnf0Wt4noKaqbfsUuT
1xgHj4dX6319NNgWjkQbI6HIgJWxXmNbhuC2Lr9ZQ0RcR9tKu9P50jlDAaP3AFrH
sUHghHovqh9rYOb9vY1Rvit4tt5akl5c3sYn693/aqeA/vM0cJm4++CQCblHTyYh
zF/+s/KZ2kD670IVBJYX4/tq5pVjV2g7PYJQblR4RS0CrFf37pj5EdHm8dvitO21
9aLF6izXTvT1hoHdQvBGraIujIG6oT+P0EAWxfneOYPX1BOYGNH38rk90j3OzWYg
w40OeZI3fP5lpS/KljyXNgcCAwEAAaNTMFEwHQYDVR0OBBYEFEI1WTEJDpk939vr
YsdLDATHICIZMB8GA1UdIwQYMBaAFEI1WTEJDpk939vrYsdLDATHICIZMA8GA1Ud
EwEB/wQFMAMBAf8wDQYJKoZIhvcNAQELBQADggEBAIZT6bIp4RhsZLjC0p7LSH4v
aN+2JZKEMmCf/xgbJPizaZOvfyMykHIumGFXQmV1dser/pg3o5uAd9V6OhLXib6u
lhyx0tWhcEi2WkMME/TMdwWKKt7RpnMPp1Aq8ZDCMArVWsqhYbjRFR7+Mz/+U46Y
3Tl3wkWi+598ruiSKOZodwk1Nl0Sc3zR9/GphnmwKmXFukgdTv4HtGTjA8c3s+hB
50WT43ndKOQLWaBkWhvev2vBz3K3ZAi1ENoDjFYwtLym1AlisLlKOOUeC6ChpLAr
NakE7TNr5654KIzEGFHLA3h4UxNl33/m5zFgL0ByZTtCA1cvAnlLGOn7/MgN0Y4=
-----END CERTIFICATE-----
`),
			CASignerKeyMapKey: []byte(`-----BEGIN RSA PRIVATE KEY-----
MIIEpAIBAAKCAQEAs2W3Nyob68nTAr0U242Bq8ZrJ+I72cyHUGXj3Js7fnpcJWd/
Ra3iegpqpt+xS5PXGAePh1frfX002BaORBsjociAlbFeY1uG4LYuv1lDRFxH20q7
0/nSOUMBo/cAWsexQeCEei+qH2tg5v29jVG+K3i23lqSXlzexifr3f9qp4D+8zRw
mbj74JAJuUdPJiHMX/6z8pnaQPrvQhUElhfj+2rmlWNXaDs9glBuVHhFLQKsV/fu
mPkR0ebx2+K07bX1osXqLNdO9PWGgd1C8Eatoi6MgbqhP4/QQBbF+d45g9fUE5gY
0ffyuT3SPc7NZiDDjQ55kjd8/mWlL8qWPJc2BwIDAQABAoIBAFdNV6UD3ASZ+hMq
Gv1hVspWTA1jvkaWjv8kJohUDtbVCwS04i3xmfZUHWTKFUi3UISEIWf29EXkaZQD
HgaswmFX5qNyZoGpp/CxF/zMnrykv99K9i8JMzHklubJLCYBahSqAy5HBd42bjjb
IKSmNAqJu0xn/TToswzxnooxYyDSB0oKyfEDIXz97MA61G7SeL3FLORT8+WjP0kc
1FoIRHo5awG0z61ShA1rC0jByHjjaj96VehT7ZspurINiFQM9bR5tWhxYgtQumvv
QvQQ2FYMTZY8/S7cvBqMDi3APz5E7bMUMey5nArKQfQIQ2cVuBt4vxR/eUc4zV3y
JIVI3mECgYEA6u0bMeQUh301+foSR71B5oW47MvVbM/ThEM8GaCWWNKp1OVlS0cy
Q1ToB5yxDDh150lMNJauN1NnjYczguk9788osKwOxlzjNbE05jQ/9NH964ghpNLT
ltx522JiMMHTp4mjJmkdYtKWRGs2Mehhij3UFQrqsThJODK3Gb4Hh1ECgYEAw31u
2fEzcuLWunn+vPSbSbZs5TtjtdbggF46to2JlReshL5B7WKa0mm2ctcKzn94p0VD
woczvqgEjOBJCvI2p3WoHCXPDjQE0RAGKi/UwnhrWylFvH3+XZiOhLd1w+phxGYL
q73vGL1GE/85PrrxT088v4bev/PpWrB86kQZQdcCgYBmkwaHvyVzjyktL5IhvrHy
fDqlMc7LRub83fp02hgrSjgbG9ohh0GcAouZH0JyqohYZzmd0Jja0VDqi7jjFQIV
HiePFGETHWWbgPcu+GtgcvvihjriY6c9PKD8ODXVQhwvD7qrv8Oz7WztDL7KBcPo
/1wFoBGfNYtKvWITHFTfMQKBgQCyo7zYjAFnysJORYzzPtNo2LtJ/qtvT5x3saQV
jeFbzPZplzLHqoOwI8oFx1yotvOaZ0E0UjiG0SLXWV1mE1C+VlX44tQDNqXwJaR8
iJjz3Pa9p0mCpd/7x5z0ynFjRptwzY98sWP8R3nybBfzqwE4aEArBSQoZMupg/2i
Vfh+oQKBgQDUho++wrV1Djf7f1Tsf+Dz6MjaKi7HOG0i4Y/5BNLBOecMx2vnj0xT
3tO8SI3Xp9lgqsTPsjKb/BKV1YnaM+OOCuLWGOC6A9/5CLdkVd1SPyv0/gWFa/xG
cbB8e1/poskGpVSBbY2tUDSRqteUR7irvQlwpTCWATjjuBW7hd9knQ==
-----END RSA PRIVATE KEY-----
`),
		},
	}
}

func TestReconcileSignedCertWithKeysAndAddresses(t *testing.T) {
	t.Parallel()
	caCfg := certs.CertCfg{IsCA: true, Subject: pkix.Name{CommonName: "root-ca", OrganizationalUnit: []string{"ou"}}}
	caKey, caCert, err := certs.GenerateSelfSignedCertificate(&caCfg)
	if err != nil {
		t.Fatalf("failed go generate CA: %v", err)
	}

	caKeyPem, err := certs.PrivateKeyToPem(caKey)
	if err != nil {
		t.Fatalf("failed to serialize CA key to pem: %v", err)
	}
	ed25519RootCA := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "ed25519",
		},
		Data: map[string][]byte{
			CASignerCertMapKey: certs.CertToPem(caCert),
			CASignerKeyMapKey:  caKeyPem,
		},
	}

	// Run all tests with a RSA and ED25519 CA to ensure compatibility
	for _, caSecret := range []*corev1.Secret{rsaRootCA(), ed25519RootCA} {
		t.Run(caSecret.Name+" ca", func(t *testing.T) {
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
						privKeyPem, err := certs.PrivateKeyToPem(key)
						if err != nil {
							return nil, fmt.Errorf("failed to serialize private key to pem: %v", err)
						}
						return &corev1.Secret{
							Data: map[string][]byte{
								corev1.TLSPrivateKeyKey: privKeyPem,
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
						privKeyPem, err := certs.PrivateKeyToPem(key)
						if err != nil {
							return nil, fmt.Errorf("failed to serialize private key to pem: %v", err)
						}
						return &corev1.Secret{
							Data: map[string][]byte{
								corev1.TLSPrivateKeyKey: privKeyPem,
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

					if err := reconcileSignedCertWithKeysAndAddresses(
						secret,
						caSecret,
						config.OwnerRef{},
						"foo",
						[]string{"org"},
						X509UsageServerAuth,
						corev1.TLSCertKey,
						corev1.TLSPrivateKeyKey,
						CASignerCertMapKey,
						[]string{"foo.svc.local"},
						[]string{"127.0.0.1"},
					); err != nil {
						t.Fatalf("reconcileSignedCertWithKeysAndAddresses failed: %v", err)
					}

					didUpdate := !bytes.Equal(initialKey, secret.Data[corev1.TLSPrivateKeyKey]) && !bytes.Equal(initalCert, secret.Data[corev1.TLSCertKey])
					if didUpdate != tc.expectUpdate {
						t.Errorf("expectUpdate: %t differs froma actual %t", tc.expectUpdate, didUpdate)
					}

					if !hasCAHash(secret, caSecret) {
						t.Error("secret doesn't have ca hash")
					}

					if diff := cmp.Diff(string(secret.Data[CASignerCertMapKey]), string(caSecret.Data[CASignerCertMapKey])); diff != "" {
						t.Errorf("Cacert differs from expected: %s", diff)
					}
				})
			}

		})
	}

}
