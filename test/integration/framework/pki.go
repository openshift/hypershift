package framework

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"embed"
	"encoding/pem"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/openshift/hypershift/control-plane-pki-operator/certificates"

	librarygocrypto "github.com/openshift/library-go/pkg/crypto"
)

// generating lots of PKI in environments where compute and/or entropy is limited (like in test containers)
// can be very slow - instead, we use precomputed PKI and allow for re-generating it if necessary
//
//go:embed testdata
var testdata embed.FS

func CertKeyRequest(t *testing.T, signer certificates.SignerClass) ([]byte, []byte, []byte, []byte) {
	if os.Getenv("REGENERATE_PKI") != "" {
		t.Logf("$REGENERATE_PKI set, generating a new cert/key pair for signer %s", signer)
		cfg, err := librarygocrypto.MakeSelfSignedCAConfigForDuration("test-signer", time.Hour*24*365*100)
		if err != nil {
			t.Fatalf("could not generate self-signed CA: %v", err)
		}
		certb, keyb, err := cfg.GetPEMBytes()
		if err != nil {
			t.Fatalf("failed to marshal CA cert and key: %v", err)
		}

		if err := os.WriteFile(filepath.Join("testdata", string(signer)+"-tls.key"), keyb, 0666); err != nil {
			t.Fatalf("failed to write re-generated private key: %v", err)
		}

		if err := os.WriteFile(filepath.Join("testdata", string(signer)+"-tls.crt"), certb, 0666); err != nil {
			t.Fatalf("failed to write re-generated certificate: %v", err)
		}

		csr, err := x509.CreateCertificateRequest(rand.New(rand.NewSource(0)), &x509.CertificateRequest{
			Subject: pkix.Name{
				CommonName:   CommonNameFor(signer),
				Organization: []string{"system:masters"},
			},
		}, cfg.Key)
		if err != nil {
			t.Fatalf("failed to create certificate request")
		}
		csrb := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csr})
		if err := os.WriteFile(filepath.Join("testdata", string(signer)+"-csr.pem"), csrb, 0666); err != nil {
			t.Fatalf("failed to write re-generated certificate request: %v", err)
		}

		wrongCsr, err := x509.CreateCertificateRequest(rand.New(rand.NewSource(0)), &x509.CertificateRequest{
			Subject: pkix.Name{
				CommonName:   "invalid-name",
				Organization: []string{"system:masters"},
			},
		}, cfg.Key)
		if err != nil {
			t.Fatalf("failed to create certificate request")
		}
		wrongCsrb := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: wrongCsr})
		if err := os.WriteFile(filepath.Join("testdata", string(signer)+"-invalid-csr.pem"), wrongCsrb, 0666); err != nil {
			t.Fatalf("failed to write re-generated certificate request: %v", err)
		}

		return certb, keyb, csrb, wrongCsrb
	}

	t.Logf("loading certificate/key pair from disk for signer %s, use $REGENERATE_PKI to generate new ones", signer)
	keyb, err := testdata.ReadFile(filepath.Join("testdata", string(signer)+"-tls.key"))
	if err != nil {
		t.Fatalf("failed to read private key: %v", err)
	}

	crtb, err := testdata.ReadFile(filepath.Join("testdata", string(signer)+"-tls.crt"))
	if err != nil {
		t.Fatalf("failed to read certificate: %v", err)
	}

	csrb, err := testdata.ReadFile(filepath.Join("testdata", string(signer)+"-csr.pem"))
	if err != nil {
		t.Fatalf("failed to read certificate request: %v", err)
	}

	wrongCsrb, err := testdata.ReadFile(filepath.Join("testdata", string(signer)+"-invalid-csr.pem"))
	if err != nil {
		t.Fatalf("failed to read certificate request: %v", err)
	}
	return crtb, keyb, csrb, wrongCsrb
}

func CommonNameFor(signer certificates.SignerClass) string {
	return certificates.CommonNamePrefix(signer) + "e2e-test"
}
