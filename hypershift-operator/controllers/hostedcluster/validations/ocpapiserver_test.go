package validations

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/stretchr/testify/assert"
)

const (
	customServingCertSecretName = "custom-serving-cert"
)

func TestValidateOCPAPIServerSANs(t *testing.T) {
	hc := sampleHostedCluster()
	kasServerCertSecret := sampleKASServerCertSecret(t)
	kasServerPrivateCertSecret := sampleKASServerPrivateCertSecret(t)

	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = hyperv1.AddToScheme(scheme)
	_ = configv1.AddToScheme(scheme)

	tests := []struct {
		name              string
		namedCertificates []configv1.APIServerNamedServingCert
		secrets           []client.Object
		expectedErrors    field.ErrorList
	}{
		{
			name: "custom serving cert, hcp deployed, valid configuration with no conflicts",
			namedCertificates: []configv1.APIServerNamedServingCert{
				{
					Names: []string{"custom.example.com"},
					ServingCertificate: configv1.SecretNameReference{
						Name: customServingCertSecretName,
					},
				},
			},
			secrets: []client.Object{
				kasServerCertSecret,
				kasServerPrivateCertSecret,
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      customServingCertSecretName,
						Namespace: "clusters",
					},
					Data: map[string][]byte{
						"tls.crt": generateTestCertificate(t, []string{"custom.example.com"}, []string{"192.168.1.105"}),
					},
				},
			},
			expectedErrors: field.ErrorList(nil),
		},
		{
			name: "custom serving cert, hcp not deployed, valid configuration with no conflicts",
			namedCertificates: []configv1.APIServerNamedServingCert{
				{
					Names: []string{"custom.example.com"},
					ServingCertificate: configv1.SecretNameReference{
						Name: customServingCertSecretName,
					},
				},
			},
			secrets: []client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      customServingCertSecretName,
						Namespace: "clusters",
					},
					Data: map[string][]byte{
						"tls.crt": generateTestCertificate(t, []string{"custom.example.com"}, []string{"192.168.1.105"}),
					},
				},
			},
			expectedErrors: field.ErrorList(nil),
		},
		{
			name:              "no custom serving cert, hcp not deployed, valid configuration with no conflicts",
			namedCertificates: []configv1.APIServerNamedServingCert{},
			secrets:           []client.Object{},
			expectedErrors:    field.ErrorList(nil),
		},
		{
			name: "custom serving cert, hcp deployed, invalid configuration with conflicts",
			namedCertificates: []configv1.APIServerNamedServingCert{
				{
					Names: []string{"api.test.hypershift.local"},
					ServingCertificate: configv1.SecretNameReference{
						Name: customServingCertSecretName,
					},
				},
			},
			secrets: []client.Object{
				kasServerCertSecret,
				kasServerPrivateCertSecret,
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      customServingCertSecretName,
						Namespace: "clusters",
					},
					Data: map[string][]byte{
						"tls.crt": generateTestCertificate(t, []string{"api.test.hypershift.local"}, []string{"192.168.1.105"}),
					},
				},
			},
			expectedErrors: field.ErrorList{
				field.Invalid(field.NewPath("conflicting entries with KAS SANs"), []string{"api.test.hypershift.local"}, "conflicting DNS names found in KAS SANs. Configuration is invalid"),
			},
		},
		{
			name: "custom serving cert, hcp not deployed, invalid configuration with conflicts",
			namedCertificates: []configv1.APIServerNamedServingCert{
				{
					Names: []string{"api.test.hypershift.local"},
					ServingCertificate: configv1.SecretNameReference{
						Name: customServingCertSecretName,
					},
				},
			},
			secrets: []client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      customServingCertSecretName,
						Namespace: "clusters",
					},
					Data: map[string][]byte{
						"tls.crt": generateTestCertificate(t, []string{"api.test.hypershift.local"}, []string{"192.168.1.105"}),
					},
				},
			},
			expectedErrors: field.ErrorList{
				field.Invalid(field.NewPath("conflicting entries with KAS SANs"), []string{"api.test.hypershift.local"}, "conflicting DNS names found in KAS SANs. Configuration is invalid"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.secrets...).
				Build()

			hc.Spec.Configuration.APIServer.ServingCerts.NamedCertificates = tt.namedCertificates
			errs := ValidateOCPAPIServerSANs(context.Background(), hc, fakeClient)
			assert.Equal(t, tt.expectedErrors, errs)
		})
	}
}

func TestAppendEntriesIfNotExists(t *testing.T) {
	tests := []struct {
		name     string
		slice    []string
		entries  []string
		expected []string
	}{
		{
			name:     "empty slice and entries",
			slice:    []string{},
			entries:  []string{},
			expected: []string{},
		},
		{
			name:     "add new entries",
			slice:    []string{"a", "b"},
			entries:  []string{"c", "d"},
			expected: []string{"a", "b", "c", "d"},
		},
		{
			name:     "add existing and new entries",
			slice:    []string{"a", "b"},
			entries:  []string{"b", "c"},
			expected: []string{"a", "b", "c"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := appendEntriesIfNotExists(tt.slice, tt.entries)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCheckConflictingSANs(t *testing.T) {
	tests := []struct {
		name          string
		customEntries []string
		kasSANEntries []string
		entryType     string
		expectError   bool
	}{
		{
			name:          "no conflicts",
			customEntries: []string{"a", "b"},
			kasSANEntries: []string{"c", "d"},
			entryType:     "DNS names",
			expectError:   false,
		},
		{
			name:          "has conflicts",
			customEntries: []string{"a", "b"},
			kasSANEntries: []string{"b", "c"},
			entryType:     "DNS names",
			expectError:   true,
		},
		{
			name:          "empty entries",
			customEntries: []string{},
			kasSANEntries: []string{},
			entryType:     "DNS names",
			expectError:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkConflictingSANs(tt.customEntries, tt.kasSANEntries, tt.entryType)
			if tt.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "conflicting")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func sampleKASServerCertSecret(t *testing.T) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      KASServerCertSecretName,
			Namespace: "clusters-test",
		},
		Data: map[string][]byte{
			"tls.crt": generateTestCertificate(t, []string{
				"api.test.hypershift.local",
				"localhost",
				"kubernetes",
				"kubernetes.default",
				"kubernetes.default.svc",
				"kubernetes.default.svc.cluster.local",
				"openshift",
				"openshift.default",
				"openshift.default.svc",
				"openshift.default.svc.cluster.local",
				"xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx-xxxxxxxxxxxxxxxx.elb.us-east-2.amazonaws.com",
			}, []string{"10.0.0.1"}),
		},
	}
}

func sampleKASServerPrivateCertSecret(t *testing.T) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      KASServerPrivateCertSecretName,
			Namespace: "clusters-test",
		},
		Data: map[string][]byte{
			"tls.crt": generateTestCertificate(t, []string{"kube-apiserver", "kube-apiserver.clusters-test.svc", "kube-apiserver.clusters-test.svc.cluster.local"}, []string{}),
		},
	}
}

func sampleHostedCluster() *hyperv1.HostedCluster {
	return &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "clusters",
		},
		Spec: hyperv1.HostedClusterSpec{
			Configuration: &hyperv1.ClusterConfiguration{
				APIServer: &configv1.APIServerSpec{
					ServingCerts: configv1.APIServerServingCerts{},
				},
			},
		},
	}
}

// generateTestCertificate creates a test certificate with the given DNS names and IP addresses
func generateTestCertificate(t *testing.T, dnsNames []string, ipAddresses []string) []byte {
	// Generate a private key
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate private key: %v", err)
	}

	// Convert IP addresses to net.IP
	ips := make([]net.IP, len(ipAddresses))
	for i, ip := range ipAddresses {
		ips[i] = net.ParseIP(ip)
	}

	// Create certificate template
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Test Org"},
			CommonName:   dnsNames[0], // Use first DNS name as CN
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(time.Hour * 24),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              dnsNames,
		IPAddresses:           ips,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
	}

	// Create the certificate
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatalf("failed to create certificate: %v", err)
	}

	// Validate the certificate by parsing it
	_, err = x509.ParseCertificate(certDER)
	if err != nil {
		t.Fatalf("failed to parse generated certificate: %v", err)
	}

	return certDER
}
