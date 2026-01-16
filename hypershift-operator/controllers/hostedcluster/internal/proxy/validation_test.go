package proxy

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// TestValidateProxyCAValidity tests the proxy CA validation logic.
func TestValidateProxyCAValidity(t *testing.T) {
	now := time.Now().Truncate(time.Second)

	tests := []struct {
		name          string
		hcluster      *hyperv1.HostedCluster
		configMap     *corev1.ConfigMap
		expectError   bool
		errorContains string
	}{
		{
			name: "When no proxy configured it should succeed",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedClusterSpec{},
			},
			expectError: false,
		},
		{
			name: "When proxy configured without CA it should succeed",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedClusterSpec{
					Configuration: &hyperv1.ClusterConfiguration{
						Proxy: &configv1.ProxySpec{
							HTTPProxy: "http://proxy.example.com:8080",
						},
					},
				},
			},
			expectError: false,
		},
		{
			name: "When valid certificate it should succeed",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedClusterSpec{
					Configuration: &hyperv1.ClusterConfiguration{
						Proxy: &configv1.ProxySpec{
							TrustedCA: configv1.ConfigMapNameReference{
								Name: "test-ca-bundle",
							},
						},
					},
				},
			},
			configMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ca-bundle",
					Namespace: "test-namespace",
				},
				Data: map[string]string{
					ProxyCAConfigMapKey: generateCertPEM(t, now.Add(24*time.Hour)),
				},
			},
			expectError: false,
		},
		{
			name: "When expired certificate it should fail",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedClusterSpec{
					Configuration: &hyperv1.ClusterConfiguration{
						Proxy: &configv1.ProxySpec{
							TrustedCA: configv1.ConfigMapNameReference{
								Name: "test-ca-bundle-expired",
							},
						},
					},
				},
			},
			configMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ca-bundle-expired",
					Namespace: "test-namespace",
				},
				Data: map[string]string{
					ProxyCAConfigMapKey: generateCertPEM(t, now.Add(-24*time.Hour)),
				},
			},
			expectError:   true,
			errorContains: "no longer valid",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clientBuilder := fake.NewClientBuilder()
			if tc.configMap != nil {
				clientBuilder = clientBuilder.WithObjects(tc.configMap)
			}
			client := clientBuilder.Build()

			err := ValidateProxyCAValidity(context.Background(), client, tc.hcluster)

			if tc.expectError && err == nil {
				t.Errorf("Expected error but got none")
			}
			if !tc.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}
			if tc.expectError && err != nil && tc.errorContains != "" {
				if !containsString(err.Error(), tc.errorContains) {
					t.Errorf("Expected error to contain %q but got: %v", tc.errorContains, err)
				}
			}
		})
	}
}

func generateCertPEM(t *testing.T, notAfter time.Time) string {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("Failed to generate private key: %v", err)
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "test-cert",
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatalf("Failed to create certificate: %v", err)
	}

	var certPEM bytes.Buffer
	if err := pem.Encode(&certPEM, &pem.Block{Type: "CERTIFICATE", Bytes: certDER}); err != nil {
		t.Fatalf("Failed to encode certificate: %v", err)
	}

	return certPEM.String()
}

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || bytes.Contains([]byte(s), []byte(substr)))
}
