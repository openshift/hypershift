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
				field.Invalid(field.NewPath("custom serving cert"), []string{"test-conflicting-kas-san.example.com"}, "conflicting DNS names found in KAS SANs. kasEntries: [localhost kubernetes kubernetes.default kubernetes.default.svc kubernetes.default.svc.cluster.local kube-apiserver openshift openshift.default openshift.default.svc openshift.default.svc.cluster.local kube-apiserver.clusters.svc kube-apiserver.clusters.svc.cluster.local  api.test.hypershift.local kube-apiserver.clusters-jparrill-hosted.svc kube-apiserver.clusters-jparrill-hosted.svc.cluster.local test-conflicting-kas-san.example.com], customEntry: test-conflicting-kas-san.example.com. The configuration is invalid because the custom DNS names is conflicting with the KAS SANs"),
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
				pemCert, pemKey, err = util.GenerateTestCertificate(context.Background(), tt.dnsNames, tt.ipAddresses, 24*time.Hour)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(pemCert).NotTo(BeNil())
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
					err := fakeClient.Get(context.Background(), types.NamespacedName{
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
			errs := ValidateOCPAPIServerSANs(context.Background(), hc, fakeClient)
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

func TestIsDNSNameMatch(t *testing.T) {
	tests := []struct {
		name     string
		dnsName  string
		pattern  string
		expected bool
	}{
		// Exact matches
		{
			name:     "exact match - simple domain",
			dnsName:  "example.com",
			pattern:  "example.com",
			expected: true,
		},
		{
			name:     "exact match - subdomain",
			dnsName:  "sub.example.com",
			pattern:  "sub.example.com",
			expected: true,
		},
		{
			name:     "exact match - multiple subdomains",
			dnsName:  "a.b.c.example.com",
			pattern:  "a.b.c.example.com",
			expected: true,
		},
		{
			name:     "no match - different domains",
			dnsName:  "example.com",
			pattern:  "other.com",
			expected: false,
		},
		{
			name:     "no match - different subdomains",
			dnsName:  "sub.example.com",
			pattern:  "other.example.com",
			expected: false,
		},
		// Wildcard matches
		{
			name:     "wildcard match - single level",
			dnsName:  "sub.example.com",
			pattern:  "*.example.com",
			expected: true,
		},
		{
			name:     "wildcard match - multiple levels",
			dnsName:  "baz.foo.bar.com",
			pattern:  "*.foo.bar.com",
			expected: true,
		},
		{
			name:     "wildcard no match - too many levels",
			dnsName:  "sub.sub.example.com",
			pattern:  "*.example.com",
			expected: false,
		},
		{
			name:     "wildcard no match - too few levels",
			dnsName:  "example.com",
			pattern:  "*.example.com",
			expected: false,
		},
		{
			name:     "wildcard no match - different domain",
			dnsName:  "sub.example.com",
			pattern:  "*.other.com",
			expected: false,
		},
		{
			name:     "wildcard no match - partial domain match",
			dnsName:  "sub.example.com",
			pattern:  "*.example.org",
			expected: false,
		},
		// Edge cases
		{
			name:     "wildcard pattern not at start",
			dnsName:  "example.com",
			pattern:  "example.*.com",
			expected: false,
		},
		{
			name:     "wildcard pattern at end",
			dnsName:  "example.com",
			pattern:  "*.com",
			expected: true,
		},
		{
			name:     "empty strings",
			dnsName:  "",
			pattern:  "",
			expected: true,
		},
		{
			name:     "empty dnsName",
			dnsName:  "",
			pattern:  "*.example.com",
			expected: false,
		},
		{
			name:     "empty pattern",
			dnsName:  "example.com",
			pattern:  "",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			result := isDNSNameMatch(tt.dnsName, tt.pattern)
			g.Expect(result).To(Equal(tt.expected),
				"DNS name '%s' should %s match pattern '%s'",
				tt.dnsName,
				map[bool]string{true: "", false: "not"}[tt.expected],
				tt.pattern)
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
		{
			name:          "wildcard conflicts - custom entry matches KAS wildcard",
			customEntries: []string{"sub.example.com"},
			kasSANEntries: []string{"*.example.com"},
			entryType:     "DNS names",
			expectError:   true,
		},
		{
			name:          "wildcard conflicts - custom wildcard matches KAS entry",
			customEntries: []string{"*.example.com"},
			kasSANEntries: []string{"sub.example.com"},
			entryType:     "DNS names",
			expectError:   true,
		},
		{
			name:          "wildcard conflicts - both wildcards with same domain",
			customEntries: []string{"*.example.com"},
			kasSANEntries: []string{"*.example.com"},
			entryType:     "DNS names",
			expectError:   true,
		},
		{
			name:          "wildcard conflicts - custom entry matches KAS wildcard with subdomain",
			customEntries: []string{"baz.foo.bar.com"},
			kasSANEntries: []string{"*.foo.bar.com"},
			entryType:     "DNS names",
			expectError:   true,
		},
		{
			name:          "no wildcard conflicts - custom entry doesn't match KAS wildcard",
			customEntries: []string{"sub.sub.example.com"},
			kasSANEntries: []string{"*.example.com"},
			entryType:     "DNS names",
			expectError:   false,
		},
		{
			name:          "no wildcard conflicts - different domains",
			customEntries: []string{"sub.example.com"},
			kasSANEntries: []string{"*.other.com"},
			entryType:     "DNS names",
			expectError:   false,
		},
		{
			name:          "no wildcard conflicts - custom wildcard doesn't match KAS entry",
			customEntries: []string{"*.example.com"},
			kasSANEntries: []string{"other.com"},
			entryType:     "DNS names",
			expectError:   false,
		},
		{
			name:          "mixed conflicts - exact match and wildcard match",
			customEntries: []string{"exact.example.com", "sub.example.com"},
			kasSANEntries: []string{"exact.example.com", "*.example.com"},
			entryType:     "DNS names",
			expectError:   true,
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
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.AWSPlatform,
				AWS: &hyperv1.AWSPlatformSpec{
					EndpointAccess: hyperv1.Public,
				},
			},
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
