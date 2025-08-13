package validations

import (
	"context"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/test/util"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	customServingCertSecretName = "custom-serving-cert"
)

func TestValidateOCPAPIServerSANs(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = hyperv1.AddToScheme(scheme)
	_ = configv1.AddToScheme(scheme)

	kasServerCertSecret := sampleKASServerCertSecret()
	kasServerPrivateCertSecret := sampleKASServerPrivateCertSecret()

	// Get a sample HostedCluster
	hc := sampleHostedCluster()

	tests := []struct {
		name              string
		annotation        string
		customCertSecret  *corev1.Secret
		secrets           []client.Object
		namedCertificates []configv1.APIServerNamedServingCert
		expectedErrors    field.ErrorList
		dnsNames          []string
		ipAddresses       []string
	}{
		{
			name: "custom serving cert, hcp deployed, valid configuration with no conflicts",
			customCertSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      customServingCertSecretName,
					Namespace: "clusters",
				},
			},
			dnsNames:    []string{"test.example.com"},
			ipAddresses: []string{"192.168.1.1"},
			secrets: []client.Object{
				kasServerCertSecret,
				kasServerPrivateCertSecret,
			},
			namedCertificates: []configv1.APIServerNamedServingCert{
				{
					Names:              []string{"test.example.com"},
					ServingCertificate: configv1.SecretNameReference{Name: customServingCertSecretName},
				},
			},
			expectedErrors: nil,
		},
		{
			name: "custom serving cert, hcp not deployed, valid configuration with no conflicts",
			customCertSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      customServingCertSecretName,
					Namespace: "clusters",
				},
			},
			dnsNames: []string{"test.example.com"},
			secrets:  []client.Object{},
			namedCertificates: []configv1.APIServerNamedServingCert{
				{
					Names:              []string{"test.example.com"},
					ServingCertificate: configv1.SecretNameReference{Name: customServingCertSecretName},
				},
			},
			expectedErrors: nil,
		},
		{
			name:       "invalid certificate format, PKI reconciliation disabled",
			annotation: hyperv1.DisablePKIReconciliationAnnotation,
			customCertSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      customServingCertSecretName,
					Namespace: "clusters",
				},
				Data: map[string][]byte{
					"tls.crt": []byte("invalid certificate"),
				},
			},
			secrets: []client.Object{},
			namedCertificates: []configv1.APIServerNamedServingCert{
				{
					Names:              []string{"test.example.com"},
					ServingCertificate: configv1.SecretNameReference{Name: customServingCertSecretName},
				},
			},
			expectedErrors: nil,
		},
		{
			name: "invalid certificate format",
			customCertSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      customServingCertSecretName,
					Namespace: "clusters",
				},
				Data: map[string][]byte{
					"tls.crt": []byte("invalid certificate"),
				},
			},
			secrets: []client.Object{},
			namedCertificates: []configv1.APIServerNamedServingCert{
				{
					Names:              []string{"test.example.com"},
					ServingCertificate: configv1.SecretNameReference{Name: customServingCertSecretName},
				},
			},
			expectedErrors: field.ErrorList{
				field.Invalid(field.NewPath("KAS TLS private cert decrypt"), KASServerPrivateCertSecretName, "failed to decode PEM block from certificate"),
			},
		},
		{
			name: "missing secret",
			namedCertificates: []configv1.APIServerNamedServingCert{
				{
					Names:              []string{"test.example.com"},
					ServingCertificate: configv1.SecretNameReference{Name: customServingCertSecretName},
				},
			},
			expectedErrors: field.ErrorList{
				field.Invalid(field.NewPath("NamedCertificates get secret"), "custom-serving-cert", "secrets \"custom-serving-cert\" not found"),
			},
		},
		{
			name:           "no custom serving cert, hcp not deployed, valid configuration with no conflicts",
			secrets:        []client.Object{},
			expectedErrors: nil,
		},
		{
			name: "conflicting SANs with KAS",

			customCertSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      customServingCertSecretName,
					Namespace: "clusters",
				},
			},
			dnsNames: []string{"test-conflicting-kas-san.example.com"},
			secrets: []client.Object{
				kasServerCertSecret,
				kasServerPrivateCertSecret,
			},
			namedCertificates: []configv1.APIServerNamedServingCert{
				{
					Names:              []string{"test-conflicting-kas-san.example.com"},
					ServingCertificate: configv1.SecretNameReference{Name: customServingCertSecretName},
				},
			},
			expectedErrors: field.ErrorList{
				field.Invalid(field.NewPath("custom serving cert"), []string{"test-conflicting-kas-san.example.com"}, "conflicting DNS names found in KAS SANs. Configuration is invalid"),
			},
		},
		{
			name: "invalid certificate data",
			customCertSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      customServingCertSecretName,
					Namespace: "clusters",
				},
				Data: map[string][]byte{
					"tls.crt": []byte("invalid certificate data"),
				},
			},
			secrets: []client.Object{
				kasServerCertSecret,
				kasServerPrivateCertSecret,
			},
			namedCertificates: []configv1.APIServerNamedServingCert{
				{
					Names:              []string{"test.example.com"},
					ServingCertificate: configv1.SecretNameReference{Name: customServingCertSecretName},
				},
			},
			expectedErrors: field.ErrorList{
				field.Invalid(field.NewPath("KAS TLS private cert decrypt"), KASServerPrivateCertSecretName, "failed to decode PEM block from certificate"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var (
				pemCert []byte
				pemKey  []byte
				err     error
				g       = NewWithT(t)
			)

			if len(tt.namedCertificates) > 0 && (tt.customCertSecret != nil && tt.customCertSecret.Data == nil) {
				// Generate a test certificate
				pemCert, pemKey, err = util.GenerateTestCertificate(t.Context(), tt.dnsNames, tt.ipAddresses, 24*time.Hour)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(pemCert).NotTo(BeNil())
			}

			// Set the annotation if there is one
			hc.Annotations = make(map[string]string)
			if tt.annotation != "" {
				hc.Annotations[tt.annotation] = ""
			}

			// Initialize secrets with proper data
			objects := make([]client.Object, 0)
			if tt.customCertSecret != nil {
				if tt.customCertSecret.Data == nil {
					tt.customCertSecret.Data = make(map[string][]byte)
					tt.customCertSecret.Data["tls.crt"] = pemCert
					tt.customCertSecret.Data["tls.key"] = pemKey
				}
				objects = append(objects, tt.customCertSecret)
			}

			// Add KAS secrets if they are in the test case
			if len(tt.secrets) > 0 {
				objects = append(objects, tt.secrets...)
			}

			if len(tt.namedCertificates) > 0 {
				hc.Spec.Configuration.APIServer.ServingCerts.NamedCertificates = tt.namedCertificates
				objects = append(objects, hc)
			}

			// Create a new client with the scheme and objects
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objects...).
				Build()

			// Verify that the secrets were added correctly
			for _, object := range objects {
				switch object.(type) {
				case *corev1.Secret:
					var foundSecret corev1.Secret
					err := fakeClient.Get(t.Context(), types.NamespacedName{
						Name:      object.GetName(),
						Namespace: object.GetNamespace(),
					}, &foundSecret)
					g.Expect(err).ToNot(HaveOccurred(), "failed to get secret %s/%s", object.GetNamespace(), object.GetName())
					g.Expect(foundSecret.Data).ToNot(BeNil(), "secret data should not be nil")
					g.Expect(foundSecret.Data["tls.crt"]).ToNot(BeEmpty(), "certificate data should not be empty")
				}
			}

			if hc.Spec.Configuration == nil {
				hc.Spec.Configuration = &hyperv1.ClusterConfiguration{}
			}
			if hc.Spec.Configuration.APIServer == nil {
				hc.Spec.Configuration.APIServer = &configv1.APIServerSpec{}
			}
			if hc.Spec.Configuration.APIServer.ServingCerts.NamedCertificates == nil {
				hc.Spec.Configuration.APIServer.ServingCerts.NamedCertificates = []configv1.APIServerNamedServingCert{}
			}
			hc.Spec.Configuration.APIServer.ServingCerts.NamedCertificates = tt.namedCertificates
			errs := ValidateOCPAPIServerSANs(t.Context(), hc, fakeClient)
			g.Expect(errs).To(Equal(tt.expectedErrors))
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
			g := NewWithT(t)
			result := appendEntriesIfNotExists(tt.slice, tt.entries)
			g.Expect(result).To(Equal(tt.expected))
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
			g := NewWithT(t)
			err := checkConflictingSANs(tt.customEntries, tt.kasSANEntries, tt.entryType)
			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("conflicting"))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
}

func sampleKASServerCertSecret() *corev1.Secret {
	pemCert, pemKey, err := util.GenerateTestCertificate(
		context.Background(),
		[]string{
			"kube-apiserver",
			"kube-apiserver.clusters-jparrill-hosted.svc",
			"kube-apiserver.clusters-jparrill-hosted.svc.cluster.local",
			"test-conflicting-kas-san.example.com",
		},
		[]string{},
		24*time.Hour,
	)
	if err != nil {
		panic(fmt.Sprintf("failed to generate KAS server certificate: %v", err))
	}

	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      KASServerCertSecretName,
			Namespace: "clusters-test",
		},
		Data: map[string][]byte{
			"tls.crt": pemCert,
			"tls.key": pemKey,
		},
		Type: corev1.SecretTypeTLS,
	}
}

func sampleKASServerPrivateCertSecret() *corev1.Secret {
	pemCert, pemKey, err := util.GenerateTestCertificate(
		context.Background(),
		[]string{
			"localhost",
			"kubernetes",
			"kubernetes.default",
			"kubernetes.default.svc",
			"kubernetes.default.svc.cluster.local",
			"openshift",
			"openshift.default",
			"openshift.default.svc",
			"openshift.default.svc.cluster.local",
		},
		[]string{"127.0.0.1", "::1"},
		24*time.Hour,
	)
	if err != nil {
		panic(fmt.Sprintf("failed to generate KAS private certificate: %v", err))
	}

	// Create a PEM block for the certificate

	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      KASServerPrivateCertSecretName,
			Namespace: "clusters-test",
		},
		Data: map[string][]byte{
			"tls.crt": pemCert,
			"tls.key": pemKey,
		},
		Type: corev1.SecretTypeTLS,
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
					ServingCerts: configv1.APIServerServingCerts{
						NamedCertificates: []configv1.APIServerNamedServingCert{},
					},
				},
			},
		},
	}
}
