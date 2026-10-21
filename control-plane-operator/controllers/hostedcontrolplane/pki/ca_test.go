package pki

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/config"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// generateTestCert creates a PEM-encoded certificate signed by the given parent.
// If parent and parentKey are nil, the certificate is self-signed.
func generateTestCert(t *testing.T, cn string, isCA bool, parent *x509.Certificate, parentKey *ecdsa.PrivateKey) (certPEM []byte, key *ecdsa.PrivateKey, cert *x509.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-1 * time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		IsCA:         isCA,
	}
	if isCA {
		template.KeyUsage = x509.KeyUsageCertSign
		template.BasicConstraintsValid = true
	}
	signer := key
	signerCert := template
	if parent != nil {
		signerCert = parent
		signer = parentKey
	}
	certDER, err := x509.CreateCertificate(rand.Reader, template, signerCert, &key.PublicKey, signer)
	if err != nil {
		t.Fatalf("failed to create certificate: %v", err)
	}
	cert, err = x509.ParseCertificate(certDER)
	if err != nil {
		t.Fatalf("failed to parse certificate: %v", err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	return certPEM, key, cert
}

// buildChain concatenates PEM blocks into a single chain.
func buildChain(parts ...[]byte) []byte {
	var chain []byte
	for _, p := range parts {
		chain = append(chain, p...)
	}
	return chain
}

func TestFilterCACertificates(t *testing.T) {
	rootCAPEM, rootKey, rootCert := generateTestCert(t, "root-ca", true, nil, nil)
	intermediatePEM, _, _ := generateTestCert(t, "intermediate-ca", true, rootCert, rootKey)
	leafPEM, _, _ := generateTestCert(t, "api.example.com", false, nil, nil)

	tests := []struct {
		name           string
		certPEM        []byte
		expectNonEmpty bool
		expectContains []string
	}{
		{
			name:           "When input is nil, it should return nil",
			certPEM:        nil,
			expectNonEmpty: false,
		},
		{
			name:           "When input contains a single CA, it should return it",
			certPEM:        rootCAPEM,
			expectNonEmpty: true,
			expectContains: []string{"root-ca"},
		},
		{
			name:           "When input contains multiple CAs, it should return all of them",
			certPEM:        buildChain(rootCAPEM, intermediatePEM),
			expectNonEmpty: true,
			expectContains: []string{"root-ca", "intermediate-ca"},
		},
		{
			name:           "When input contains only a non-CA certificate, it should return nil",
			certPEM:        leafPEM,
			expectNonEmpty: false,
		},
		{
			name:           "When input mixes CA and non-CA certificates, it should return only CAs",
			certPEM:        buildChain(leafPEM, rootCAPEM),
			expectNonEmpty: true,
			expectContains: []string{"root-ca"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := FilterCACertificates(tc.certPEM)
			if tc.expectNonEmpty && len(result) == 0 {
				t.Fatal("expected non-empty CA bundle, got empty")
			}
			if !tc.expectNonEmpty && len(result) != 0 {
				t.Fatalf("expected empty CA bundle, got %d bytes", len(result))
			}
			for _, cn := range tc.expectContains {
				rest := result
				found := false
				for {
					var block *pem.Block
					block, rest = pem.Decode(rest)
					if block == nil {
						break
					}
					cert, err := x509.ParseCertificate(block.Bytes)
					if err != nil {
						t.Fatalf("failed to parse certificate: %v", err)
					}
					if cert.Subject.CommonName == cn {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected CA with CN=%q in result, not found", cn)
				}
			}
		})
	}
}

func TestExtractCACertificatesFromChain(t *testing.T) {
	rootCAPEM, rootKey, rootCert := generateTestCert(t, "root-ca", true, nil, nil)
	intermediatePEM, intermediateKey, intermediateCert := generateTestCert(t, "intermediate-ca", true, rootCert, rootKey)
	leafPEM, _, _ := generateTestCert(t, "api.example.com", false, intermediateCert, intermediateKey)
	nonCAPEM, _, _ := generateTestCert(t, "not-a-ca", false, rootCert, rootKey)

	tests := []struct {
		name           string
		certPEM        []byte
		expectNonEmpty bool
		expectContains []string
	}{
		{
			name:           "When input is nil, it should return nil",
			certPEM:        nil,
			expectNonEmpty: false,
		},
		{
			name:           "When input is empty, it should return nil",
			certPEM:        []byte{},
			expectNonEmpty: false,
		},
		{
			name:           "When chain has only a single self-signed cert, it should return nil",
			certPEM:        leafPEM,
			expectNonEmpty: false,
		},
		{
			name:           "When chain has a leaf and intermediate CA, it should return the intermediate CA",
			certPEM:        buildChain(leafPEM, intermediatePEM),
			expectNonEmpty: true,
			expectContains: []string{"intermediate-ca"},
		},
		{
			name:           "When chain has leaf, intermediate, and root CA, it should return both CAs",
			certPEM:        buildChain(leafPEM, intermediatePEM, rootCAPEM),
			expectNonEmpty: true,
			expectContains: []string{"intermediate-ca", "root-ca"},
		},
		{
			name:           "When chain contains non-CERTIFICATE PEM blocks, it should skip them",
			certPEM:        buildChain(leafPEM, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: []byte("fake")}), intermediatePEM),
			expectNonEmpty: true,
			expectContains: []string{"intermediate-ca"},
		},
		{
			name:           "When chain contains a non-CA certificate after the leaf, it should skip it",
			certPEM:        buildChain(leafPEM, nonCAPEM, intermediatePEM),
			expectNonEmpty: true,
			expectContains: []string{"intermediate-ca"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := ExtractCACertificatesFromChain(tc.certPEM)
			if tc.expectNonEmpty && len(result) == 0 {
				t.Fatal("expected non-empty CA bundle, got empty")
			}
			if !tc.expectNonEmpty && len(result) != 0 {
				t.Fatalf("expected empty CA bundle, got %d bytes", len(result))
			}
			for _, cn := range tc.expectContains {
				rest := result
				found := false
				for {
					var block *pem.Block
					block, rest = pem.Decode(rest)
					if block == nil {
						break
					}
					cert, err := x509.ParseCertificate(block.Bytes)
					if err != nil {
						t.Fatalf("failed to parse certificate: %v", err)
					}
					if cert.Subject.CommonName == cn {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected CA with CN=%q in result, not found", cn)
				}
			}
		})
	}
}

func TestReconcileRootCAConfigMap(t *testing.T) {
	tests := []struct {
		name        string
		rootCA      *corev1.Secret
		ingressCert *corev1.ConfigMap
		namedCAs    []*corev1.Secret
		expected    string
	}{
		{
			name: "When no named certs are configured, it should contain only root CA and ingress cert",
			rootCA: &corev1.Secret{
				Data: map[string][]byte{
					certs.CASignerCertMapKey: []byte("root-ca-cert-pem"),
				},
			},
			ingressCert: &corev1.ConfigMap{
				Data: map[string]string{
					certs.CASignerCertMapKey: "ingress-cert-pem",
				},
			},
			namedCAs: nil,
			expected: "root-ca-cert-pemingress-cert-pem",
		},
		{
			name: "When ingress cert is nil, it should contain only root CA",
			rootCA: &corev1.Secret{
				Data: map[string][]byte{
					certs.CASignerCertMapKey: []byte("root-ca-cert-pem"),
				},
			},
			ingressCert: nil,
			namedCAs:    nil,
			expected:    "root-ca-cert-pem",
		},
		{
			name: "When named cert CAs are provided, it should include them after root CA and ingress cert",
			rootCA: &corev1.Secret{
				Data: map[string][]byte{
					certs.CASignerCertMapKey: []byte("root-ca-cert-pem"),
				},
			},
			ingressCert: &corev1.ConfigMap{
				Data: map[string]string{
					certs.CASignerCertMapKey: "ingress-cert-pem",
				},
			},
			namedCAs: []*corev1.Secret{
				{Data: map[string][]byte{certs.CASignerCertMapKey: []byte("external-ca-1-pem")}},
				{Data: map[string][]byte{certs.CASignerCertMapKey: []byte("external-ca-2-pem")}},
			},
			expected: "root-ca-cert-pemingress-cert-pemexternal-ca-1-pemexternal-ca-2-pem",
		},
		{
			name: "When ingress cert is nil and named cert CAs are provided, it should include root CA and named CAs",
			rootCA: &corev1.Secret{
				Data: map[string][]byte{
					certs.CASignerCertMapKey: []byte("root-ca-cert-pem"),
				},
			},
			ingressCert: nil,
			namedCAs: []*corev1.Secret{
				{Data: map[string][]byte{certs.CASignerCertMapKey: []byte("external-ca-pem")}},
			},
			expected: "root-ca-cert-pemexternal-ca-pem",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "root-ca", Namespace: "test"}}
			ownerRef := config.OwnerRef{}

			if err := ReconcileRootCAConfigMap(cm, ownerRef, tc.rootCA, tc.ingressCert, tc.namedCAs...); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			got := cm.Data[certs.CASignerCertMapKey]
			if got != tc.expected {
				t.Errorf("unexpected configmap data:\ngot:  %q\nwant: %q", got, tc.expected)
			}
		})
	}
}
