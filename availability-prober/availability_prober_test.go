package availabilityprober

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/gomega"
)

func TestTlsConfigForProbe(t *testing.T) {
	t.Parallel()
	t.Run("When given a CA file, it should accept trusted certificates and reject untrusted certificates", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		caPEM, caCert, caKey := mustGenerateCA(t)
		trustedSrv := tlsServerWithCert(t, caCert, caKey)
		untrustedSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(untrustedSrv.Close)

		caFile := filepath.Join(t.TempDir(), "ca.crt")
		g.Expect(os.WriteFile(caFile, caPEM, 0o600)).To(Succeed())

		tlsCfg, err := tlsConfigForProbe(caFile, nil)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(tlsCfg.InsecureSkipVerify).To(BeFalse())

		client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{TLSClientConfig: tlsCfg}}

		trustedReq, err := http.NewRequestWithContext(context.Background(), http.MethodGet, trustedSrv.URL, nil)
		g.Expect(err).ToNot(HaveOccurred())
		trustedResp, err := client.Do(trustedReq)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(trustedResp.StatusCode).To(Equal(http.StatusOK))
		g.Expect(trustedResp.Body.Close()).To(Succeed())

		untrustedReq, err := http.NewRequestWithContext(context.Background(), http.MethodGet, untrustedSrv.URL, nil)
		g.Expect(err).ToNot(HaveOccurred())
		_, err = client.Do(untrustedReq)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("certificate"))
	})

	t.Run("When no CA is configured, it should fail", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		_, err := tlsConfigForProbe("", nil)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("certificate authority"))
	})

	t.Run("When given a kubeconfig CA, it should use it", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		caPEM, _, _ := mustGenerateCA(t)
		tlsCfg, err := tlsConfigForProbe("", caPEM)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(tlsCfg.RootCAs).ToNot(BeNil())
	})
}

func TestNewStartCommand(t *testing.T) {
	if target := os.Getenv("HYPERSHIFT_PROBER_TEST_TARGET"); target != "" {
		cmd := NewStartCommand()
		cmd.SetArgs([]string{"--target", target, "--ca-file", os.Getenv("HYPERSHIFT_PROBER_TEST_CA_FILE")})
		if err := cmd.ExecuteContext(t.Context()); err != nil {
			t.Fatal(err)
		}
		return
	}

	t.Parallel()
	t.Run("When given a trusted HTTPS CA file, it should complete the probe", func(t *testing.T) {
		g := NewWithT(t)
		caPEM, caCert, caKey := mustGenerateCA(t)
		srv := tlsServerWithCert(t, caCert, caKey)

		caFile := filepath.Join(t.TempDir(), "ca.crt")
		g.Expect(os.WriteFile(caFile, caPEM, 0o600)).To(Succeed())

		testBinary, err := os.Executable()
		g.Expect(err).ToNot(HaveOccurred())

		// Run in a subprocess so a retrying probe is terminated when the deadline expires.
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, testBinary, "-test.run=^TestNewStartCommand$")
		cmd.Env = append(os.Environ(),
			"HYPERSHIFT_PROBER_TEST_TARGET="+srv.URL,
			"HYPERSHIFT_PROBER_TEST_CA_FILE="+caFile,
		)
		output, err := cmd.CombinedOutput()
		g.Expect(err).ToNot(HaveOccurred(), "prober output:\n%s", output)
		g.Expect(string(output)).To(ContainSubstring("all checks successful"))
	})
}

func tlsServerWithCert(t *testing.T, caCert *x509.Certificate, caKey *ecdsa.PrivateKey) *httptest.Server {
	t.Helper()
	leafCert, leafKey := mustIssueLeaf(t, caCert, caKey)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv.TLS = &tls.Config{
		MinVersion: tls.VersionTLS12,
		Certificates: []tls.Certificate{{
			Certificate: [][]byte{leafCert.Raw},
			PrivateKey:  leafKey,
		}},
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

func mustGenerateCA(t *testing.T) ([]byte, *x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "h12-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return pemBytes, cert, key
}

func mustIssueLeaf(t *testing.T, caCert *x509.Certificate, caKey *ecdsa.PrivateKey) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"127.0.0.1", "localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert, key
}
