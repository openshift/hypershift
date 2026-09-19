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
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	"github.com/go-logr/logr"
)

func TestTLSConfigForProbeRejectsUntrustedCertificates(t *testing.T) {
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

	trustedResp, err := client.Get(trustedSrv.URL)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(trustedResp.StatusCode).To(Equal(http.StatusOK))
	g.Expect(trustedResp.Body.Close()).To(Succeed())

	_, err = client.Get(untrustedSrv.URL)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("certificate"))
}

func TestTLSConfigForProbeRequiresCA(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)
	_, err := tlsConfigForProbe("", nil)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("certificate authority"))
}

func TestTLSConfigForProbeAcceptsKubeconfigCA(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)
	caPEM, _, _ := mustGenerateCA(t)
	tlsCfg, err := tlsConfigForProbe("", caPEM)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(tlsCfg.RootCAs).ToNot(BeNil())
}

func TestCheckSucceedsAgainstTrustedHTTPS(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)
	caPEM, caCert, caKey := mustGenerateCA(t)
	srv := tlsServerWithCert(t, caCert, caKey)

	caFile := filepath.Join(t.TempDir(), "ca.crt")
	g.Expect(os.WriteFile(caFile, caPEM, 0o600)).To(Succeed())
	tlsCfg, err := tlsConfigForProbe(caFile, nil)
	g.Expect(err).ToNot(HaveOccurred())

	target, err := url.Parse(srv.URL)
	g.Expect(err).ToNot(HaveOccurred())

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	check(ctx, logr.Discard(), target, time.Second, time.Millisecond, nil, false, "", "", nil, nil, tlsCfg)
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
