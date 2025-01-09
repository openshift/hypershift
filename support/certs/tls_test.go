package certs_test

import (
	"bytes"
	"crypto/x509"
	"crypto/x509/pkix"
	"net"
	"reflect"
	"strconv"
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
