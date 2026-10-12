package mirrorregistry

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/openshift/hypershift/support/certs"
)

func TestGenerateCA(t *testing.T) {
	tests := []struct {
		name string
	}{
		{name: "When generating a CA, it should return a one-year signing certificate"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ca, err := generateCA()
			if err != nil {
				t.Fatalf("generateCA() error = %v", err)
			}
			certificate, err := certs.PemToCertificate(ca.CertPEM)
			if err != nil {
				t.Fatalf("parse generated CA certificate: %v", err)
			}
			if certificate.Subject.CommonName != caCommonName {
				t.Errorf("CA common name = %q, want %q", certificate.Subject.CommonName, caCommonName)
			}
			if !certificate.IsCA || !certificate.BasicConstraintsValid {
				t.Errorf("generated certificate is not a valid CA")
			}
			if certificate.KeyUsage&x509.KeyUsageCertSign == 0 {
				t.Errorf("CA key usage %v does not permit certificate signing", certificate.KeyUsage)
			}
			if got := certificate.NotAfter.Sub(certificate.NotBefore); got != certs.ValidityOneYear {
				t.Errorf("CA validity = %v, want %v", got, certs.ValidityOneYear)
			}
		})
	}
}

func TestServingCert(t *testing.T) {
	ca, err := generateCA()
	if err != nil {
		t.Fatalf("generate CA for test: %v", err)
	}
	host := "mirror-registry-test-mirror-registry.apps.alesross-mc.alesross.hypershift.devcluster.openshift.com"
	if len(host) <= 64 {
		t.Fatalf("test host %q should exceed the RFC 5280 common-name limit", host)
	}

	malformedCA := KeyPair{CertPEM: []byte("not a certificate"), KeyPEM: []byte("not a private key")}
	shortLivedCA := newTestCA(t, 30*24*time.Hour)
	tests := []struct {
		name            string
		cn              string
		dnsNames        []string
		ca              KeyPair
		caValidity      time.Duration
		wantErrContains string
		wantMaxValidity time.Duration
	}{
		{
			name:            "When the CA expires within one minute, it should return an error",
			cn:              "mirror-registry",
			dnsNames:        []string{host},
			caValidity:      45 * time.Second,
			wantErrContains: "expires too soon",
		},
		{
			name:     "When the DNS host exceeds 64 characters, it should verify through the SAN with a short common name",
			cn:       "mirror-registry",
			dnsNames: []string{host},
		},
		{
			name:            "When the common name exceeds 64 characters, it should return an error",
			cn:              strings.Repeat("a", 65),
			wantErrContains: "exceeds the 64-character",
		},
		{
			name:            "When the common name is empty, it should return an error",
			cn:              "",
			wantErrContains: "must not be empty",
		},
		{
			name:            "When the CA key pair is malformed, it should return an error",
			cn:              "mirror-registry",
			ca:              malformedCA,
			wantErrContains: "parse mirror registry CA private key",
		},
		{
			name:            "When the CA expires within 30 days, it should shorten serving certificate validity",
			cn:              "mirror-registry",
			dnsNames:        []string{host},
			ca:              shortLivedCA,
			wantMaxValidity: 30 * 24 * time.Hour,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			testCA := ca
			if test.ca.CertPEM != nil || test.ca.KeyPEM != nil {
				testCA = test.ca
			}
			if test.caValidity > 0 {
				testCA = newTestCA(t, test.caValidity)
			}
			serving, err := servingCert(testCA, test.cn, test.dnsNames)
			if test.wantErrContains != "" {
				if err == nil {
					t.Fatal("servingCert() error = nil, want an error")
				}
				if !strings.Contains(err.Error(), test.wantErrContains) {
					t.Errorf("servingCert() error = %q, want it to contain %q", err, test.wantErrContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("servingCert() error = %v", err)
			}
			servingCertificate, err := certs.PemToCertificate(serving.CertPEM)
			if err != nil {
				t.Fatalf("parse serving certificate: %v", err)
			}
			if got := servingCertificate.Subject.CommonName; got != test.cn {
				t.Errorf("serving common name = %q, want %q", got, test.cn)
			}
			if len(servingCertificate.Subject.CommonName) > 64 {
				t.Errorf("serving common name has %d characters, want at most 64", len(servingCertificate.Subject.CommonName))
			}
			if len(servingCertificate.ExtKeyUsage) != 1 || servingCertificate.ExtKeyUsage[0] != x509.ExtKeyUsageServerAuth {
				t.Errorf("serving extended key usage = %v, want ServerAuth", servingCertificate.ExtKeyUsage)
			}
			gotValidity := servingCertificate.NotAfter.Sub(servingCertificate.NotBefore)
			if test.wantMaxValidity == 0 {
				if gotValidity != servingCertificateTTL {
					t.Errorf("serving certificate validity = %v, want %v", gotValidity, servingCertificateTTL)
				}
			} else if gotValidity <= 0 || gotValidity >= test.wantMaxValidity {
				t.Errorf("serving certificate validity = %v, want a positive duration shorter than %v", gotValidity, test.wantMaxValidity)
			}
			certificateAuthority, err := certs.PemToCertificate(testCA.CertPEM)
			if err != nil {
				t.Fatalf("parse test CA certificate: %v", err)
			}
			if !servingCertificate.NotAfter.Before(certificateAuthority.NotAfter) {
				t.Errorf("serving certificate expires at %v, not before CA expiry %v", servingCertificate.NotAfter, certificateAuthority.NotAfter)
			}
			verifyDNSNames(t, testCA, servingCertificate, test.dnsNames)
		})
	}
}

func TestTLSMaterial(t *testing.T) {
	suppliedCA, err := generateCA()
	if err != nil {
		t.Fatalf("generate supplied CA for test: %v", err)
	}
	longHost := "mirror-registry-test-mirror-registry.apps.alesross-mc.alesross.hypershift.devcluster.openshift.com"
	pkcs8CA := pkcs8EncodedCA(t, suppliedCA)

	tests := []struct {
		name            string
		opts            Options
		host            string
		wantCA          *KeyPair
		wantErrContains string
		wantDNS         []string
	}{
		{
			name: "When a CA is supplied, it should preserve that CA and sign a certificate for all deduplicated names",
			opts: Options{
				Name:      "mirror-registry",
				Namespace: "test-mirror-registry",
				CA:        &suppliedCA,
				ExtraDNSNames: []string{
					longHost,
					"*.apps.alesross-mc.alesross.hypershift.devcluster.openshift.com",
					"",
					" \t ",
				},
			},
			host:   longHost,
			wantCA: &suppliedCA,
			wantDNS: []string{
				longHost,
				"mirror-registry",
				"mirror-registry.test-mirror-registry",
				"mirror-registry.test-mirror-registry.svc",
				"mirror-registry.test-mirror-registry.svc.cluster.local",
				"*.apps.alesross-mc.alesross.hypershift.devcluster.openshift.com",
			},
		},
		{
			name: "When a PKCS#8 CA key is supplied, it should generate a verifiable serving certificate",
			opts: Options{
				Name:      "mirror-registry",
				Namespace: "test-mirror-registry",
				CA:        pkcs8CA,
			},
			host:   longHost,
			wantCA: pkcs8CA,
			wantDNS: []string{
				longHost,
				"mirror-registry",
				"mirror-registry.test-mirror-registry",
				"mirror-registry.test-mirror-registry.svc",
				"mirror-registry.test-mirror-registry.svc.cluster.local",
			},
		},
		{
			name: "When the name is empty, it should use the default name as the serving certificate CN",
			opts: Options{
				Namespace: "test-mirror-registry",
			},
			host: longHost,
			wantDNS: []string{
				longHost,
				"mirror-registry",
				"mirror-registry.test-mirror-registry",
				"mirror-registry.test-mirror-registry.svc",
				"mirror-registry.test-mirror-registry.svc.cluster.local",
			},
		},
		{
			name: "When no CA is supplied, it should generate a CA and a verifiable serving certificate",
			opts: Options{
				Name:      "mirror-registry",
				Namespace: "test-mirror-registry",
				ExtraDNSNames: []string{
					"mirror-registry.test-mirror-registry.svc",
				},
			},
			host: longHost,
			wantDNS: []string{
				longHost,
				"mirror-registry",
				"mirror-registry.test-mirror-registry",
				"mirror-registry.test-mirror-registry.svc",
				"mirror-registry.test-mirror-registry.svc.cluster.local",
			},
		},
		{
			name: "When the supplied CA is malformed, it should return an error",
			opts: Options{
				Name:      "mirror-registry",
				Namespace: "test-mirror-registry",
				CA: &KeyPair{
					CertPEM: []byte("not a certificate"),
					KeyPEM:  []byte("not a private key"),
				},
			},
			host:            longHost,
			wantErrContains: "parse mirror registry CA private key",
		},
		{
			name: "When an extra DNS name has surrounding whitespace, it should return an error",
			opts: Options{
				Name:      "mirror-registry",
				Namespace: "test-mirror-registry",
				ExtraDNSNames: []string{
					" mirror-registry.apps.alesross-mc.alesross.hypershift.devcluster.openshift.com ",
				},
			},
			host:            longHost,
			wantErrContains: "mirror registry DNS name \" mirror-registry.apps.alesross-mc.alesross.hypershift.devcluster.openshift.com \" has leading or trailing whitespace",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ca, serving, err := tlsMaterial(test.opts, test.host)
			if test.wantErrContains != "" {
				if err == nil {
					t.Fatal("tlsMaterial() error = nil, want an error")
				}
				if !strings.Contains(err.Error(), test.wantErrContains) {
					t.Errorf("tlsMaterial() error = %q, want it to contain %q", err, test.wantErrContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("tlsMaterial() error = %v", err)
			}
			if test.wantCA != nil && (!bytes.Equal(ca.CertPEM, test.wantCA.CertPEM) || !bytes.Equal(ca.KeyPEM, test.wantCA.KeyPEM)) {
				t.Errorf("tlsMaterial() did not preserve the supplied CA key pair")
			}
			servingCertificate, err := certs.PemToCertificate(serving.CertPEM)
			if err != nil {
				t.Fatalf("parse returned serving certificate: %v", err)
			}
			if got := servingCertificate.DNSNames; !slices.Equal(got, test.wantDNS) {
				t.Errorf("serving DNS names = %q, want %q", got, test.wantDNS)
			}
			wantCN := test.opts.Name
			if wantCN == "" {
				wantCN = DefaultName
			}
			if got := servingCertificate.Subject.CommonName; got != wantCN {
				t.Errorf("serving certificate common name = %q, want opts.Name %q", got, wantCN)
			}
			verifyDNSNames(t, ca, servingCertificate, test.wantDNS)
		})
	}
}

func TestParseCA(t *testing.T) {
	validCA := newTestCA(t, certs.ValidityOneYear)
	otherCA := newTestCA(t, certs.ValidityOneYear)
	nonCA := newTestCertificate(t, certs.ValidityOneYear, false)
	expiredCA := newTestCA(t, -time.Hour)
	pkcs8CA := pkcs8EncodedCA(t, validCA)
	tests := []struct {
		name            string
		ca              KeyPair
		wantErrContains string
	}{
		{
			name: "When the CA is valid, it should return its key and certificate",
			ca:   validCA,
		},
		{
			name: "When the CA private key uses PKCS#8, it should parse the RSA key",
			ca:   *pkcs8CA,
		},
		{
			name: "When the certificate PEM is malformed, it should return an error",
			ca: KeyPair{
				CertPEM: []byte("not a certificate"),
				KeyPEM:  validCA.KeyPEM,
			},
			wantErrContains: "parse mirror registry CA certificate",
		},
		{
			name: "When the private key PEM is malformed, it should return an error",
			ca: KeyPair{
				CertPEM: validCA.CertPEM,
				KeyPEM:  []byte("not a private key"),
			},
			wantErrContains: "parse mirror registry CA private key",
		},
		{
			name: "When the key does not match the certificate, it should return an error",
			ca: KeyPair{
				CertPEM: validCA.CertPEM,
				KeyPEM:  otherCA.KeyPEM,
			},
			wantErrContains: "does not match",
		},
		{
			name:            "When the certificate is not a CA, it should return an error",
			ca:              nonCA,
			wantErrContains: "not valid for certificate signing",
		},
		{
			name:            "When the CA is not currently valid, it should return an error",
			ca:              expiredCA,
			wantErrContains: "not currently valid",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			key, certificate, err := parseCA(test.ca)
			if test.wantErrContains != "" {
				if err == nil {
					t.Fatal("parseCA() error = nil, want an error")
				}
				if !strings.Contains(err.Error(), test.wantErrContains) {
					t.Errorf("parseCA() error = %q, want it to contain %q", err, test.wantErrContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseCA() error = %v", err)
			}
			if key == nil || certificate == nil {
				t.Fatalf("parseCA() returned key %v and certificate %v, want both non-nil", key, certificate)
			}
			if certificate.Subject.CommonName != "test-mirror-registry-ca" {
				t.Errorf("parseCA() certificate common name = %q, want %q", certificate.Subject.CommonName, "test-mirror-registry-ca")
			}
			publicKey, ok := certificate.PublicKey.(*rsa.PublicKey)
			if !ok {
				t.Fatalf("parseCA() certificate public key has type %T, want *rsa.PublicKey", certificate.PublicKey)
			}
			if key.N.Cmp(publicKey.N) != 0 {
				t.Errorf("parseCA() private key does not match the certificate public key")
			}
		})
	}
}

func TestParsePrivateKey(t *testing.T) {
	validCA := newTestCA(t, certs.ValidityOneYear)
	pkcs8CA := pkcs8EncodedCA(t, validCA)
	ecdsaKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate ECDSA test key: %v", err)
	}
	ecdsaDER, err := x509.MarshalPKCS8PrivateKey(ecdsaKey)
	if err != nil {
		t.Fatalf("marshal ECDSA test key: %v", err)
	}
	tests := []struct {
		name            string
		keyPEM          []byte
		wantErrContains string
	}{
		{
			name:   "When the key is PKCS#1 RSA, it should parse successfully",
			keyPEM: validCA.KeyPEM,
		},
		{
			name:   "When the key is PKCS#8 RSA, it should parse successfully",
			keyPEM: pkcs8CA.KeyPEM,
		},
		{
			name:            "When the key is PKCS#8 ECDSA, it should return an error",
			keyPEM:          pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: ecdsaDER}),
			wantErrContains: "expected RSA",
		},
		{
			name:            "When the PKCS#8 data is malformed, it should return an error",
			keyPEM:          pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("malformed PKCS#8")}),
			wantErrContains: "parse PKCS#8 private key",
		},
		{
			name:            "When the key is not PEM encoded, it should return an error",
			keyPEM:          []byte("not a PEM private key"),
			wantErrContains: "PEM block",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			key, err := parsePrivateKey(test.keyPEM)
			if test.wantErrContains != "" {
				if err == nil {
					t.Fatal("parsePrivateKey() error = nil, want an error")
				}
				if !strings.Contains(err.Error(), test.wantErrContains) {
					t.Errorf("parsePrivateKey() error = %q, want it to contain %q", err, test.wantErrContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("parsePrivateKey() error = %v", err)
			}
			if key == nil {
				t.Fatal("parsePrivateKey() key = nil, want an RSA key")
			}
		})
	}
}

func newTestCA(t *testing.T, validity time.Duration) KeyPair {
	t.Helper()
	return newTestCertificate(t, validity, true)
}

// newTestCertificate gives non-CA fixtures signing usage too, isolating parseCA's IsCA check.
func newTestCertificate(t *testing.T, validity time.Duration, isCA bool) KeyPair {
	t.Helper()
	keyUsages := x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature
	key, certificate, err := certs.GenerateSelfSignedCertificate(&certs.CertCfg{
		KeyUsages: keyUsages,
		Subject: pkix.Name{
			CommonName:         "test-mirror-registry-ca",
			OrganizationalUnit: []string{"test"},
		},
		Validity: validity,
		IsCA:     isCA,
	})
	if err != nil {
		t.Fatalf("generate test certificate: %v", err)
	}
	return KeyPair{CertPEM: certs.CertToPem(certificate), KeyPEM: certs.PrivateKeyToPem(key)}
}

func pkcs8EncodedCA(t *testing.T, ca KeyPair) *KeyPair {
	t.Helper()
	key, err := certs.PemToPrivateKey(ca.KeyPEM)
	if err != nil {
		t.Fatalf("parse test CA private key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal PKCS#8 test CA private key: %v", err)
	}
	return &KeyPair{
		CertPEM: ca.CertPEM,
		KeyPEM:  pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}),
	}
}

func verifyDNSNames(t *testing.T, ca KeyPair, certificate *x509.Certificate, dnsNames []string) {
	t.Helper()
	caCertificate, err := certs.PemToCertificate(ca.CertPEM)
	if err != nil {
		t.Fatalf("parse CA certificate for verification: %v", err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(caCertificate)
	for _, dnsName := range dnsNames {
		verificationName := dnsName
		if strings.HasPrefix(dnsName, "*.") {
			verificationName = "mirror-registry." + strings.TrimPrefix(dnsName, "*.")
		}
		if _, err := certificate.Verify(x509.VerifyOptions{
			Roots:       roots,
			DNSName:     verificationName,
			CurrentTime: time.Now(),
			KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		}); err != nil {
			t.Errorf("serving certificate does not verify for DNS name %q (checked as %q): %v", dnsName, verificationName, err)
		}
	}
}
