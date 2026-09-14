package certs_test

import (
	"bytes"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"net"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/pki"
	"github.com/openshift/hypershift/support/certs"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/google/go-cmp/cmp"
	fuzz "github.com/google/gofuzz"
)

// TestValidateKeyPairConsidersAllFields does what the name suggests.
// It works by:
// * Fuzzing an existing config
// * Iterating over all fields in the config through `reflect`
// * Generating a cert
// * Re-fuzzing the field
// * Ensuring `ValidateKeyPair` returns an error
func TestValidateKeyPairConsidersAllFields(t *testing.T) {
	t.Parallel()

	fuzzer := fuzzer()
	caCfg := certs.CertCfg{IsCA: true, Subject: pkix.Name{CommonName: "root-ca", OrganizationalUnit: []string{"ou"}}}
	caKey, caCert, err := certs.GenerateSelfSignedCertificate(&caCfg)
	if err != nil {
		t.Fatalf("failed go generate CA: %v", err)
	}

	cfgReflectType := reflect.TypeOf(certs.CertCfg{})
	for i := 0; i < cfgReflectType.NumField(); i++ {

		// The Validity field is not checked by comparing config and cert so it doesn't fit this test.
		// It has its own test below.
		if cfgReflectType.Field(i).Name == "Validity" {
			continue
		}

		t.Run(cfgReflectType.Field(i).Name, func(t *testing.T) {
			cfg := &certs.CertCfg{}
			fuzzer.Fuzz(&cfg)
			key, cert, err := certs.GenerateSignedCertificate(caKey, caCert, cfg)
			if err != nil {
				t.Fatalf("GenerateSelfSignedCertificate failed: %v", err)
			}

			val := reflect.ValueOf(cfg).Elem()
			// Some fields have a very limited set of inputs so there is a chance we get the same value again
			// and cause testflakes. Hence, repeat the fuzzing until we get a new value.
			for current := val.Field(i).Interface(); reflect.DeepEqual(current, val.Field(i).Interface()); fuzzer.Fuzz(val.Field(i).Addr().Interface()) {
			}

			err = certs.ValidateKeyPair(certs.PrivateKeyToPem(key), certs.CertToPem(cert), cfg, 0)
			if err == nil {
				t.Error("ValidateKeyPair returned a nil error, should have detected the change")
			}
		})
	}
}

func TestValidateKeyPairConsidersExpiration(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name        string
		validity    time.Duration
		expectValid bool
	}{
		{
			name:        "Still valid",
			validity:    time.Hour,
			expectValid: true,
		},
		{
			name:        "Expired",
			validity:    0,
			expectValid: false,
		},
	}

	caCfg := certs.CertCfg{IsCA: true, Subject: pkix.Name{CommonName: "root-ca", OrganizationalUnit: []string{"ou"}}}
	caKey, caCert, err := certs.GenerateSelfSignedCertificate(&caCfg)
	if err != nil {
		t.Fatalf("failed go generate CA: %v", err)
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &certs.CertCfg{Validity: tc.validity}

			key, cert, err := certs.GenerateSignedCertificate(caKey, caCert, cfg)
			if err != nil {
				t.Fatalf("GenerateSelfSignedCertificate failed: %v", err)
			}

			err = certs.ValidateKeyPair(certs.PrivateKeyToPem(key), certs.CertToPem(cert), cfg, time.Minute)
			isValid := err == nil
			if isValid != tc.expectValid {
				t.Errorf("expected valid: %t, actual valid: %t, error from ValidateKeyPair: %v", tc.expectValid, isValid, err)
			}

		})
	}

}

func fuzzer() *fuzz.Fuzzer {
	return fuzz.New().NilChance(0).
		Funcs(
			// pkix.AttributeTypeAndValue has a nested interface field which the fuzzer can't fill.
			// just leave them empty, it is sufficient to test that changes in the parent struct cause
			// a diff.
			func(_ *[]pkix.AttributeTypeAndValue, _ fuzz.Continue) {},
			func(s *string, c fuzz.Continue) { c.FuzzNoCustom(s); *s = certs.Base64([]byte(*s)) },
			func(ip *net.IP, c fuzz.Continue) {
				var segments []byte
				for segment := 0; segment < 4; segment++ {
					var b byte
					c.Fuzz(&b)
					segments = append(segments, b)
				}
				*ip = net.IPv4(segments[0], segments[1], segments[2], segments[3])
			},
			// x509.ExtKeyUsage, needs to be a random positive integer < 13
			func(e *x509.ExtKeyUsage, c fuzz.Continue) {
				c.FuzzNoCustom(e)
				*e = x509.ExtKeyUsage(abs(int(*e)) % 13)
			},
			// x509.KeyUsage, needs to be a random positive integer < 8
			func(e *x509.KeyUsage, c fuzz.Continue) {
				c.FuzzNoCustom(e)
				*e = x509.KeyUsage(abs(int(*e)) % 8)
			},
			// Make sure durations are positive
			func(d *time.Duration, c fuzz.Continue) { c.FuzzNoCustom(d); *d = time.Duration(abs(int(*d))) },
		)
}

func TestValidateKeyPairRejectsMalformedIP(t *testing.T) {
	t.Parallel()

	t.Run("When config has a malformed IP address, it should fail validation", func(t *testing.T) {
		caCfg := certs.CertCfg{IsCA: true, Subject: pkix.Name{CommonName: "root-ca", OrganizationalUnit: []string{"ou"}}}
		caKey, caCert, err := certs.GenerateSelfSignedCertificate(&caCfg)
		if err != nil {
			t.Fatalf("failed to generate CA: %v", err)
		}

		cfg := &certs.CertCfg{
			Subject:     pkix.Name{CommonName: "test", OrganizationalUnit: []string{"ou"}},
			IPAddresses: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("10.0.0.1")},
		}
		key, cert, err := certs.GenerateSignedCertificate(caKey, caCert, cfg)
		if err != nil {
			t.Fatalf("GenerateSignedCertificate failed: %v", err)
		}

		cfg.IPAddresses = append(cfg.IPAddresses, net.IP{1, 2, 3})

		err = certs.ValidateKeyPair(certs.PrivateKeyToPem(key), certs.CertToPem(cert), cfg, 0)
		if err == nil {
			t.Fatal("ValidateKeyPair returned a nil error, should have detected the malformed IP")
		}
		if !strings.Contains(err.Error(), "ip addresses") {
			t.Fatalf("expected error about ip addresses, got: %v", err)
		}
	})
}

func TestValidateKeyPairItempotency(t *testing.T) {
	t.Parallel()
	caCfg := certs.CertCfg{IsCA: true, Subject: pkix.Name{CommonName: "root-ca", OrganizationalUnit: []string{"ou"}}}
	caKey, caCert, err := certs.GenerateSelfSignedCertificate(&caCfg)
	if err != nil {
		t.Fatalf("failed go generate CA: %v", err)
	}

	fuzzer := fuzzer()
	for i := 0; i < 1; i++ {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			cfg := &certs.CertCfg{}
			fuzzer.Fuzz(cfg)

			key, cert, err := certs.GenerateSignedCertificate(caKey, caCert, cfg)
			if err != nil {
				t.Fatalf("GenerateSelfSignedCertificate failed: %v", err)
			}

			if err := certs.ValidateKeyPair(certs.PrivateKeyToPem(key), certs.CertToPem(cert), cfg, 0); err != nil {
				t.Errorf("validation failed when config was unchanged: %v", err)
			}
		})
	}
}

// TestReconcileSignedCertIdempotentWithDualStackIPs asserts that repeated
// ReconcileSignedCert calls with the same ipv4-only or dual-stack IPs leave
// tls.crt unchanged.
func TestReconcileSignedCertIdempotentWithDualStackIPs(t *testing.T) {
	t.Parallel()

	ca := &corev1.Secret{Type: corev1.SecretTypeTLS}
	if err := certs.ReconcileSelfSignedCA(ca, "root-ca", "ou"); err != nil {
		t.Fatalf("failed to reconcile CA: %v", err)
	}

	dnsNames := []string{
		"localhost",
		"kubernetes",
		"kubernetes.default",
		"kubernetes.default.svc",
		"kubernetes.default.svc.cluster.local",
	}

	testCases := []struct {
		name string
		ips  []string
	}{
		{
			name: "When IPs are IPv4-only, it should not regenerate the certificate",
			ips:  []string{"127.0.0.1", "172.31.0.1", "172.20.0.1"},
		},
		{
			// Mirrors dual-stack serviceNetwork SANs that trigger KAS cert churn.
			name: "When IPs are dual-stack, it should not regenerate the certificate",
			ips:  []string{"127.0.0.1", "0:0:0:0:0:0:0:1", "172.31.0.1", "2001:780:1c6:601::1", "172.20.0.1"},
		},
	}

	const reconcileIterations = 2

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			secret := &corev1.Secret{Type: corev1.SecretTypeTLS}
			var firstCert, firstKey []byte

			for i := 0; i < reconcileIterations; i++ {
				if err := certs.ReconcileSignedCert(
					secret,
					ca,
					"kubernetes",
					[]string{"kubernetes"},
					pki.X509UsageServerAuth,
					corev1.TLSCertKey,
					corev1.TLSPrivateKeyKey,
					"",
					dnsNames,
					tc.ips,
				); err != nil {
					t.Fatalf("ReconcileSignedCert iteration %d failed: %v", i, err)
				}

				cert := secret.Data[corev1.TLSCertKey]
				key := secret.Data[corev1.TLSPrivateKeyKey]
				if len(cert) == 0 || len(key) == 0 {
					t.Fatalf("iteration %d did not populate tls.crt/tls.key", i)
				}

				if i == 0 {
					firstCert = append([]byte(nil), cert...)
					firstKey = append([]byte(nil), key...)
					continue
				}

				// Later iterations revalidate; they must be a no-op.
				if !bytes.Equal(firstCert, cert) {
					t.Fatalf("iteration %d regenerated tls.crt for %s (revalidation should have been a no-op)", i, tc.name)
				}
				if !bytes.Equal(firstKey, key) {
					t.Fatalf("iteration %d regenerated tls.key for %s (revalidation should have been a no-op)", i, tc.name)
				}
			}
		})
	}
}

func abs(i int) int {
	if i < 0 {
		return -i
	}
	return i
}

func TestReconcileSignedCertWithCustomCAKeys(t *testing.T) {
	t.Parallel()
	ca := &corev1.Secret{Type: corev1.SecretTypeTLS}
	if err := certs.ReconcileSelfSignedCA(ca, "some-cn", "some-ou", func(o *certs.CAOpts) {
		o.CASignerCertMapKey = "ca-signer-cert"
		o.CASignerKeyMapKey = "ca-signer-key"
	}); err != nil {
		t.Fatalf("failed to reconcile CA: %v", err)
	}

	if ca.Type != corev1.SecretTypeTLS {
		t.Error("ReconcileSelfSignedCA changed ca secret type")
	}

	certSecret := &corev1.Secret{Type: corev1.SecretTypeTLS}
	if err := certs.ReconcileSignedCert(
		certSecret,
		ca,
		"some-cn",
		[]string{"some-ou"},
		nil,
		corev1.TLSCertKey,
		corev1.TLSPrivateKeyKey,
		"",
		nil,
		nil,
		func(o *certs.CAOpts) {
			o.CASignerCertMapKey = "ca-signer-cert"
			o.CASignerKeyMapKey = "ca-signer-key"
		},
	); err != nil {
		t.Fatalf("failed to reconcile cert: %v", err)
	}

	if certSecret.Type != corev1.SecretTypeTLS {
		t.Error("ReconcileSignedCert changed cert secret type")

	}

	// Needed because validation of tls certs only allows these two keys
	expectedKeys, actualKeys := sets.NewString(corev1.TLSCertKey, corev1.TLSPrivateKeyKey), sets.StringKeySet(certSecret.Data)
	if diff := cmp.Diff(expectedKeys, actualKeys); diff != "" {
		t.Errorf("unexpected keys in cert secret: %s", diff)

	}
}

// TestReconcileSignedCertReSignsOnCAMismatch reproduces ROSAENG-65488: a leaf certificate
// that is still time-valid and has correct SANs must be re-signed when the CA secret it
// was issued from is no longer the CA secret currently in use (e.g. after CA regeneration),
// otherwise the leaf is orphaned against a CA nothing trusts anymore.
func TestReconcileSignedCertReSignsOnCAMismatch(t *testing.T) {
	t.Parallel()

	newCA := func(t *testing.T, cn string) *corev1.Secret {
		t.Helper()
		ca := &corev1.Secret{Type: corev1.SecretTypeOpaque}
		if err := certs.ReconcileSelfSignedCA(ca, cn, "some-ou"); err != nil {
			t.Fatalf("failed to reconcile CA %s: %v", cn, err)
		}
		return ca
	}

	reconcileLeaf := func(t *testing.T, secret, ca *corev1.Secret, o ...func(*certs.CAOpts)) {
		t.Helper()
		if err := certs.ReconcileSignedCert(
			secret,
			ca,
			"some-cn",
			[]string{"some-ou"},
			[]x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			corev1.TLSCertKey,
			corev1.TLSPrivateKeyKey,
			"",
			[]string{"some.svc"},
			nil,
			o...,
		); err != nil {
			t.Fatalf("failed to reconcile cert: %v", err)
		}
	}

	verifiesAgainst := func(t *testing.T, leafSecret, ca *corev1.Secret, caCertKey string) bool {
		t.Helper()
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(ca.Data[caCertKey]) {
			t.Fatalf("failed to add CA cert to pool")
		}
		block, _ := pem.Decode(leafSecret.Data[corev1.TLSCertKey])
		if block == nil {
			t.Fatalf("failed to decode leaf cert PEM")
		}
		leaf, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			t.Fatalf("failed to parse leaf cert: %v", err)
		}
		_, err = leaf.Verify(x509.VerifyOptions{Roots: pool})
		return err == nil
	}

	t.Run("When a leaf is signed by a stale CA but is still time-valid with correct SANs, it should be re-signed against the current CA", func(t *testing.T) {
		caA := newCA(t, "ca-a")
		caB := newCA(t, "ca-b")

		leaf := &corev1.Secret{Type: corev1.SecretTypeTLS}
		reconcileLeaf(t, leaf, caA)
		if !verifiesAgainst(t, leaf, caA, certs.CASignerCertMapKey) {
			t.Fatalf("leaf should verify against CA-A right after issuance")
		}
		originalCert := append([]byte(nil), leaf.Data[corev1.TLSCertKey]...)

		// Simulate CA regeneration: reconcile the same leaf secret against a new CA.
		// The leaf is still well within its validity window and SANs are unchanged,
		// so the legacy behavior (skip re-sign) would leave it orphaned.
		reconcileLeaf(t, leaf, caB)

		if bytes.Equal(originalCert, leaf.Data[corev1.TLSCertKey]) {
			t.Errorf("expected leaf to be re-signed after CA rotation, but cert bytes are unchanged")
		}
		if verifiesAgainst(t, leaf, caA, certs.CASignerCertMapKey) {
			t.Errorf("leaf should no longer verify against the old CA-A")
		}
		if !verifiesAgainst(t, leaf, caB, certs.CASignerCertMapKey) {
			t.Errorf("leaf should verify against the current CA-B after re-sign")
		}
		if !certs.HasCAHash(leaf, caB, &certs.CAOpts{}) {
			t.Errorf("leaf's CA-hash annotation should reflect the current CA-B")
		}
	})

	t.Run("When a leaf is signed by the current CA with validity remaining, it should remain unchanged", func(t *testing.T) {
		ca := newCA(t, "ca-a")

		leaf := &corev1.Secret{Type: corev1.SecretTypeTLS}
		reconcileLeaf(t, leaf, ca)
		originalCert := append([]byte(nil), leaf.Data[corev1.TLSCertKey]...)
		originalKey := append([]byte(nil), leaf.Data[corev1.TLSPrivateKeyKey]...)

		// Reconciling again against the same, unchanged CA should be a no-op.
		reconcileLeaf(t, leaf, ca)

		if !bytes.Equal(originalCert, leaf.Data[corev1.TLSCertKey]) {
			t.Errorf("expected leaf cert to be unchanged when CA has not changed")
		}
		if !bytes.Equal(originalKey, leaf.Data[corev1.TLSPrivateKeyKey]) {
			t.Errorf("expected leaf key to be unchanged when CA has not changed")
		}
	})

	t.Run("When the CA cert is stored under the tls.crt key and the CA rotates, it should re-sign then no-op", func(t *testing.T) {
		// endpoint_resolver, ignitionserver, and metrics_proxy override
		// CASignerCertMapKey to tls.crt; exercise that path explicitly so a
		// refactor that assumes the default ca.crt key can't silently break
		// re-signing for those components.
		withTLSKey := func(o *certs.CAOpts) { o.CASignerCertMapKey = corev1.TLSCertKey }

		caA := newCA(t, "ca-a")
		caB := newCA(t, "ca-b")

		leaf := &corev1.Secret{Type: corev1.SecretTypeTLS}
		reconcileLeaf(t, leaf, caA, withTLSKey)
		if !verifiesAgainst(t, leaf, caA, corev1.TLSCertKey) {
			t.Fatalf("leaf should verify against CA-A right after issuance")
		}
		originalCert := append([]byte(nil), leaf.Data[corev1.TLSCertKey]...)

		reconcileLeaf(t, leaf, caB, withTLSKey)
		if bytes.Equal(originalCert, leaf.Data[corev1.TLSCertKey]) {
			t.Errorf("expected leaf to be re-signed after CA rotation, but cert bytes are unchanged")
		}
		if !verifiesAgainst(t, leaf, caB, corev1.TLSCertKey) {
			t.Errorf("leaf should verify against the current CA-B after re-sign")
		}

		// Reconciling again against the unchanged CA-B should be a no-op.
		stableCert := append([]byte(nil), leaf.Data[corev1.TLSCertKey]...)
		reconcileLeaf(t, leaf, caB, withTLSKey)
		if !bytes.Equal(stableCert, leaf.Data[corev1.TLSCertKey]) {
			t.Errorf("expected leaf cert to be unchanged when CA has not changed")
		}
	})
}

func TestCertChainingAttributesPresent(t *testing.T) {
	caKey, caCert, err := certs.GenerateSelfSignedCertificate(&certs.CertCfg{
		Subject:   pkix.Name{CommonName: "testca", OrganizationalUnit: []string{"randomorg"}},
		KeyUsages: x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		Validity:  certs.ValidityTenYears,
		IsCA:      true,
	})

	if err != nil {
		t.Fatalf("failed to generate CA cert: %v", err)
	}

	// The root CA only needs SKID
	caSKID := caCert.SubjectKeyId
	if len(caSKID) == 0 {
		t.Errorf("CA is missing SKID")
	}

	key, cert, err := certs.GenerateSignedCertificate(caKey, caCert, &certs.CertCfg{
		Subject:      pkix.Name{CommonName: "server cert", OrganizationalUnit: []string{"testorg"}},
		KeyUsages:    x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsages: pki.X509UsageServerAuth,
		Validity:     certs.ValidityOneYear,
		DNSNames:     []string{"distant.lands.far"},
	})

	if err != nil {
		t.Fatalf("failed to generate server cert: %v", err)
	}

	if caKey.Equal(key) {
		t.Errorf("DANGEROUS!!! The CA private key matches the private key of the certificate it signed!")
	}

	if !bytes.Equal(cert.AuthorityKeyId, caSKID) {
		t.Errorf("the server cert AKID does not match the CA SKID")
	}

	if bytes.Equal(cert.SubjectKeyId, cert.AuthorityKeyId) {
		t.Errorf("The leaf certificate SKID and AKID matches. This should never happen!")
	}

	caSubKey, caSubCert, err := certs.GenerateSignedCertificate(caKey, caCert, &certs.CertCfg{
		Subject:   pkix.Name{CommonName: "testca-sub", OrganizationalUnit: []string{"randomorg"}},
		KeyUsages: x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		Validity:  certs.ValidityTenYears,
		IsCA:      true,
	})

	if err != nil {
		t.Fatalf("failed to generate sub CA cert: %v", err)
	}

	if caSubKey.Equal(caKey) {
		t.Errorf("DANGEROUS!!! The CA private key matches the private key of the sub-CA certificate it signed!")
	}

	subSKID := caSubCert.SubjectKeyId
	subAKID := caSubCert.AuthorityKeyId
	if len(subSKID) == 0 {
		t.Errorf("subCA cert is missing SKID")
	}

	if !bytes.Equal(subAKID, caSKID) {
		t.Errorf("the subCA AKID does not equal root CA SKID!")
	}

	if bytes.Equal(subSKID, subAKID) {
		t.Errorf("The sub-CA certificate SKID and AKID matches. This should never happen!")
	}
}
