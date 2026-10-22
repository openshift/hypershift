//go:build e2ev2

package tests

import (
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	v2util "github.com/openshift/hypershift/test/e2e/v2/util"

	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	"github.com/google/go-cmp/cmp"
)

func TestBreakGlassRequest(t *testing.T) {
	for _, commonName := range []string{"system:admin:e2e-test", "invalid-name", ""} {
		t.Run("When common name is "+commonName+", it should generate a signed client authentication request", func(t *testing.T) {
			request, privateKey, err := breakGlassRequest(commonName)
			if err != nil {
				t.Fatal(err)
			}
			requestBlock, _ := pem.Decode(request)
			keyBlock, _ := pem.Decode(privateKey)
			if requestBlock == nil || keyBlock == nil {
				t.Fatal("expected PEM request and private key")
			}
			parsed, err := x509.ParseCertificateRequest(requestBlock.Bytes)
			if err != nil {
				t.Fatal(err)
			}
			if err := parsed.CheckSignature(); err != nil {
				t.Fatal(err)
			}
			if parsed.Subject.CommonName != commonName || !cmp.Equal(parsed.Subject.Organization, []string{"system:masters"}) {
				t.Fatalf("unexpected CSR subject: %#v", parsed.Subject)
			}
			key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
			if err != nil {
				t.Fatal(err)
			}
			publicKey, ok := parsed.PublicKey.(*ecdsa.PublicKey)
			if !ok || !key.PublicKey.Equal(publicKey) {
				t.Fatal("CSR and private key must form a matching pair")
			}
		})
	}
}

func TestBreakGlassClient(t *testing.T) {
	t.Run("When source config has existing credentials, it should authenticate only with the supplied certificate", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			if request.Header.Get("Authorization") != "" {
				t.Error("source authorization credentials must not be forwarded")
			}
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write([]byte(`{"kind":"SelfSubjectReview","apiVersion":"authentication.k8s.io/v1","status":{"userInfo":{"username":"test-user","groups":["system:masters"]}}}`))
		}))
		defer server.Close()
		certificate, key, err := v2util.GenerateCustomCertificate([]string{"break-glass.example.test"}, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		config := &rest.Config{Host: server.URL, BearerToken: "synthetic-test-token", Username: "original-user", Password: "synthetic-test-password", TLSClientConfig: rest.TLSClientConfig{CAData: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})}}
		client, err := breakGlassClient(config, certificate, key)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.AuthenticationV1().SelfSubjectReviews().Create(t.Context(), &authenticationv1.SelfSubjectReview{}, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
		if config.BearerToken != "synthetic-test-token" || config.Username != "original-user" {
			t.Fatal("source credentials must remain unchanged")
		}
	})
	t.Run("When certificate credentials are missing, it should fail before constructing a client", func(t *testing.T) {
		for _, testCase := range []struct {
			config      *rest.Config
			certificate []byte
			key         []byte
		}{
			{config: nil, certificate: []byte("certificate"), key: []byte("key")},
			{config: &rest.Config{}, key: []byte("key")},
			{config: &rest.Config{}, certificate: []byte("certificate")},
		} {
			if _, err := breakGlassClient(testCase.config, testCase.certificate, testCase.key); err == nil {
				t.Fatal("expected missing credentials to fail")
			}
		}
	})
	t.Run("When certificate credentials are invalid, it should fail without changing the source config", func(t *testing.T) {
		config := &rest.Config{Host: "https://api.example.test", BearerToken: "synthetic-test-token", Username: "original-user", Password: "synthetic-test-password"}
		original := rest.CopyConfig(config)
		if _, err := breakGlassClient(config, []byte("invalid certificate"), []byte("invalid key")); err == nil {
			t.Fatal("expected malformed certificate credentials to fail")
		}
		if diff := cmp.Diff(original, config); diff != "" {
			t.Fatalf("source config changed (-want +got): %s", diff)
		}
	})
}
