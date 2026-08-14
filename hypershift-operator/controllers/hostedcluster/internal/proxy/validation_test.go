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
	"strings"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// TestLoadCABundle tests the LoadCABundle function.
func TestLoadCABundle(t *testing.T) {
	now := time.Now().Truncate(time.Second)

	tests := []struct {
		name          string
		configMap     corev1.ConfigMap
		expectError   bool
		errorContains string
		expectCerts   int
	}{
		{
			name: "When ConfigMap has valid certificate it should succeed",
			configMap: corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ca-bundle",
					Namespace: "test-namespace",
				},
				Data: map[string]string{
					ProxyCAConfigMapKey: generateCertPEM(t, now.Add(24*time.Hour)),
				},
			},
			expectError: false,
			expectCerts: 1,
		},
		{
			name: "When ConfigMap has multiple certificates it should succeed",
			configMap: corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ca-bundle",
					Namespace: "test-namespace",
				},
				Data: map[string]string{
					ProxyCAConfigMapKey: generateCertPEM(t, now.Add(24*time.Hour)) + generateCertPEM(t, now.Add(48*time.Hour)),
				},
			},
			expectError: false,
			expectCerts: 2,
		},
		{
			name: "When ConfigMap is missing ca-bundle.crt key it should fail",
			configMap: corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ca-bundle",
					Namespace: "test-namespace",
				},
				Data: map[string]string{
					"wrong-key": "some-data",
				},
			},
			expectError:   true,
			errorContains: "is missing \"ca-bundle.crt\"",
		},
		{
			name: "When ConfigMap has empty ca-bundle.crt it should fail",
			configMap: corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ca-bundle",
					Namespace: "test-namespace",
				},
				Data: map[string]string{
					ProxyCAConfigMapKey: "",
				},
			},
			expectError:   true,
			errorContains: "is empty",
		},
		{
			name: "When ConfigMap has invalid certificate data it should fail",
			configMap: corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ca-bundle",
					Namespace: "test-namespace",
				},
				Data: map[string]string{
					ProxyCAConfigMapKey: "invalid-cert-data",
				},
			},
			expectError:   true,
			errorContains: "failed parsing certificate data",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			certs, err := LoadCABundle(tc.configMap)

			if tc.expectError && err == nil {
				t.Errorf("Expected error but got none")
			}
			if !tc.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}
			if tc.expectError && err != nil && tc.errorContains != "" {
				if !strings.Contains(err.Error(), tc.errorContains) {
					t.Errorf("Expected error to contain %q but got: %v", tc.errorContains, err)
				}
			}
			if !tc.expectError && len(certs) != tc.expectCerts {
				t.Errorf("Expected %d certificates but got %d", tc.expectCerts, len(certs))
			}
		})
	}
}

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
		{
			name: "When future-dated certificate it should fail",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedClusterSpec{
					Configuration: &hyperv1.ClusterConfiguration{
						Proxy: &configv1.ProxySpec{
							TrustedCA: configv1.ConfigMapNameReference{
								Name: "test-ca-bundle-future",
							},
						},
					},
				},
			},
			configMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ca-bundle-future",
					Namespace: "test-namespace",
				},
				Data: map[string]string{
					// Certificate with NotBefore 24 hours in the future
					ProxyCAConfigMapKey: generateCertPEMWithNotBefore(t, now.Add(24*time.Hour), now.Add(48*time.Hour)),
				},
			},
			expectError:   true,
			errorContains: "not yet valid",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clientBuilder := fake.NewClientBuilder().WithScheme(api.Scheme)
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
				if !strings.Contains(err.Error(), tc.errorContains) {
					t.Errorf("Expected error to contain %q but got: %v", tc.errorContains, err)
				}
			}
		})
	}
}

// TestExpiryTimeProxyCA tests the ExpiryTimeProxyCA function.
func TestExpiryTimeProxyCA(t *testing.T) {
	now := time.Now().Truncate(time.Second)

	tests := []struct {
		name           string
		hcluster       *hyperv1.HostedCluster
		configMap      *corev1.ConfigMap
		expectError    bool
		expectNil      bool
		expectedExpiry *time.Time
	}{
		{
			name: "When no proxy configured it should return nil",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedClusterSpec{},
			},
			expectNil: true,
		},
		{
			name: "When proxy configured without CA it should return nil",
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
			expectNil: true,
		},
		{
			name: "When single certificate it should return its expiry time",
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
			expectedExpiry: func() *time.Time { t := now.Add(24 * time.Hour); return &t }(),
		},
		{
			name: "When multiple certificates it should return earliest expiry time",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedClusterSpec{
					Configuration: &hyperv1.ClusterConfiguration{
						Proxy: &configv1.ProxySpec{
							TrustedCA: configv1.ConfigMapNameReference{
								Name: "test-ca-bundle-multi",
							},
						},
					},
				},
			},
			configMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ca-bundle-multi",
					Namespace: "test-namespace",
				},
				Data: map[string]string{
					// First cert expires in 48 hours, second in 24 hours (earlier)
					ProxyCAConfigMapKey: generateCertPEM(t, now.Add(48*time.Hour)) + generateCertPEM(t, now.Add(24*time.Hour)),
				},
			},
			expectedExpiry: func() *time.Time { t := now.Add(24 * time.Hour); return &t }(),
		},
		{
			name: "When ConfigMap not found it should return error",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedClusterSpec{
					Configuration: &hyperv1.ClusterConfiguration{
						Proxy: &configv1.ProxySpec{
							TrustedCA: configv1.ConfigMapNameReference{
								Name: "missing-ca-bundle",
							},
						},
					},
				},
			},
			expectError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clientBuilder := fake.NewClientBuilder().WithScheme(api.Scheme)
			if tc.configMap != nil {
				clientBuilder = clientBuilder.WithObjects(tc.configMap)
			}
			client := clientBuilder.Build()

			expiry, err := ExpiryTimeProxyCA(context.Background(), client, tc.hcluster)

			if tc.expectError && err == nil {
				t.Errorf("Expected error but got none")
			}
			if !tc.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}
			if tc.expectNil && expiry != nil {
				t.Errorf("Expected nil expiry but got: %v", expiry)
			}
			if !tc.expectNil && !tc.expectError && expiry == nil {
				t.Errorf("Expected non-nil expiry but got nil")
			}
			if tc.expectedExpiry != nil && expiry != nil {
				if !expiry.Equal(*tc.expectedExpiry) {
					t.Errorf("Expected expiry time %v but got %v", *tc.expectedExpiry, *expiry)
				}
			}
		})
	}
}

func generateCertPEM(t *testing.T, notAfter time.Time) string {
	return generateCertPEMWithNotBefore(t, time.Now().Add(-1*time.Hour), notAfter)
}

func generateCertPEMWithNotBefore(t *testing.T, notBefore, notAfter time.Time) string {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("Failed to generate private key: %v", err)
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "test-cert",
		},
		NotBefore:             notBefore,
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
