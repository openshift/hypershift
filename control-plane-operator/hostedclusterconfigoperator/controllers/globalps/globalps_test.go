package globalps

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"

	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var (
	validAuth = base64.StdEncoding.EncodeToString([]byte("user:pass"))
	oldAuth   = base64.StdEncoding.EncodeToString([]byte("olduser:oldpass"))
)

func TestValidateAdditionalPullSecret(t *testing.T) {
	tests := []struct {
		name    string
		secret  *corev1.Secret
		wantErr bool
	}{
		{
			name: "valid pull secret",
			secret: &corev1.Secret{
				Data: map[string][]byte{
					corev1.DockerConfigJsonKey: composePullSecretBytes(map[string]string{"quay.io": validAuth}),
				},
			},
			wantErr: false,
		},
		{
			name: "missing docker config key",
			secret: &corev1.Secret{
				Data: map[string][]byte{
					"wrong-key": composePullSecretBytes(map[string]string{"quay.io": validAuth}),
				},
			},
			wantErr: true,
		},
		{
			name: "invalid json",
			secret: &corev1.Secret{
				Data: map[string][]byte{
					corev1.DockerConfigJsonKey: []byte(`invalid json`),
				},
			},
			wantErr: true,
		},
		{
			name: "empty auths",
			secret: &corev1.Secret{
				Data: map[string][]byte{
					corev1.DockerConfigJsonKey: []byte(`{"auths":{}}`),
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			_, err := validateAdditionalPullSecret(tt.secret)
			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}
		})
	}
}

func TestMergePullSecrets(t *testing.T) {
	tests := []struct {
		name             string
		originalSecret   []byte
		additionalSecret []byte
		managedServices  bool
		expectedResult   []byte
		wantErr          bool
	}{
		{
			name:             "successful merge with 1 entries - non-managed services",
			originalSecret:   composePullSecretBytes(map[string]string{"registry1": validAuth}),
			additionalSecret: composePullSecretBytes(map[string]string{"registry2": validAuth}),
			managedServices:  false,
			expectedResult:   composePullSecretBytes(map[string]string{"registry1": validAuth, "registry2": validAuth}),
			wantErr:          false,
		},
		{
			name:             "successful merge with 2 entries in additional secret - non-managed services",
			originalSecret:   composePullSecretBytes(map[string]string{"registry1": validAuth}),
			additionalSecret: composePullSecretBytes(map[string]string{"registry2": validAuth, "registry3": validAuth}),
			managedServices:  false,
			expectedResult:   composePullSecretBytes(map[string]string{"registry1": validAuth, "registry2": validAuth, "registry3": validAuth}),
			wantErr:          false,
		},
		{
			name:             "successful merge with 2 entries in original secret - non-managed services",
			originalSecret:   composePullSecretBytes(map[string]string{"registry1": validAuth, "registry2": validAuth}),
			additionalSecret: composePullSecretBytes(map[string]string{"registry3": validAuth}),
			managedServices:  false,
			expectedResult:   composePullSecretBytes(map[string]string{"registry1": validAuth, "registry2": validAuth, "registry3": validAuth}),
			wantErr:          false,
		},
		{
			name:             "overwrite existing registry - non-managed services (userProvided wins)",
			originalSecret:   composePullSecretBytes(map[string]string{"registry1": oldAuth}),
			additionalSecret: composePullSecretBytes(map[string]string{"registry1": validAuth}),
			managedServices:  false,
			expectedResult:   composePullSecretBytes(map[string]string{"registry1": validAuth}),
			wantErr:          false,
		},
		{
			name:             "overwrite existing registry - managed services (original wins)",
			originalSecret:   composePullSecretBytes(map[string]string{"registry1": oldAuth}),
			additionalSecret: composePullSecretBytes(map[string]string{"registry1": validAuth}),
			managedServices:  true,
			expectedResult:   composePullSecretBytes(map[string]string{"registry1": oldAuth}),
			wantErr:          false,
		},
		{
			name:             "precedence test - non-managed services (userProvided has precedence)",
			originalSecret:   composePullSecretBytes(map[string]string{"registry1": oldAuth, "registry2": oldAuth}),
			additionalSecret: composePullSecretBytes(map[string]string{"registry1": validAuth, "registry3": validAuth}),
			managedServices:  false,
			expectedResult:   composePullSecretBytes(map[string]string{"registry1": validAuth, "registry2": oldAuth, "registry3": validAuth}),
			wantErr:          false,
		},
		{
			name:             "precedence test - managed services (original has precedence)",
			originalSecret:   composePullSecretBytes(map[string]string{"registry1": oldAuth, "registry2": oldAuth}),
			additionalSecret: composePullSecretBytes(map[string]string{"registry1": validAuth, "registry3": validAuth}),
			managedServices:  true,
			expectedResult:   composePullSecretBytes(map[string]string{"registry1": oldAuth, "registry2": oldAuth, "registry3": validAuth}),
			wantErr:          false,
		},
		{
			name:             "invalid original secret",
			originalSecret:   []byte(`invalid json`),
			additionalSecret: composePullSecretBytes(map[string]string{"registry1": validAuth}),
			managedServices:  false,
			wantErr:          true,
		},
		{
			name:             "invalid additional secret",
			originalSecret:   composePullSecretBytes(map[string]string{"registry1": validAuth}),
			additionalSecret: []byte(`invalid json`),
			managedServices:  false,
			wantErr:          true,
		},
		{
			name:             "empty additional secret, invalid JSON",
			originalSecret:   composePullSecretBytes(map[string]string{"registry1": validAuth}),
			additionalSecret: []byte{},
			managedServices:  false,
			expectedResult:   composePullSecretBytes(map[string]string{"registry1": validAuth}),
			wantErr:          true,
		},
		{
			name:             "empty additional secret with valid JSON - non-managed services",
			originalSecret:   composePullSecretBytes(map[string]string{"registry1": validAuth, "registry2": validAuth}),
			additionalSecret: []byte(`{"auths":{}}`),
			managedServices:  false,
			expectedResult:   composePullSecretBytes(map[string]string{"registry1": validAuth, "registry2": validAuth}),
			wantErr:          false,
		},
		{
			name:             "empty additional secret with valid JSON - managed services",
			originalSecret:   composePullSecretBytes(map[string]string{"registry1": validAuth, "registry2": validAuth}),
			additionalSecret: []byte(`{"auths":{}}`),
			managedServices:  true,
			expectedResult:   composePullSecretBytes(map[string]string{"registry1": validAuth, "registry2": validAuth}),
			wantErr:          false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			result, err := mergePullSecrets(context.Background(), tt.originalSecret, tt.additionalSecret, tt.managedServices)
			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(result).To(Equal(tt.expectedResult))
			}
		})
	}
}

func composePullSecretBytes(auths map[string]string) []byte {
	authsJSON := make(map[string]any)
	authsEntries := make(map[string]any)
	for registry, authEntry := range auths {
		authsEntries[registry] = map[string]any{
			"auth": authEntry,
		}
	}
	authsJSON["auths"] = authsEntries
	authsBytes, err := json.Marshal(authsJSON)
	if err != nil {
		panic(err)
	}
	return authsBytes
}

func TestAdditionalPullSecretExists(t *testing.T) {
	pullSecret := composePullSecretBytes(map[string]string{"quay.io": validAuth})
	tests := []struct {
		name           string
		secretExists   bool
		expectedExists bool
		expectedSecret *corev1.Secret
		objects        []client.Object
	}{
		{
			name:           "secret exists",
			secretExists:   true,
			expectedExists: true,
			expectedSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "additional-pull-secret",
					Namespace: "kube-system",
				},
				Data: map[string][]byte{
					corev1.DockerConfigJsonKey: pullSecret,
				},
			},
			objects: []client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "additional-pull-secret",
						Namespace: "kube-system",
					},
					Data: map[string][]byte{
						corev1.DockerConfigJsonKey: pullSecret,
					},
				},
			},
		},
		{
			name:           "secret exists but has no content",
			secretExists:   true,
			expectedExists: true,
			expectedSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "additional-pull-secret",
					Namespace: "kube-system",
				},
				Data: nil,
			},
			objects: []client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "additional-pull-secret",
						Namespace: "kube-system",
					},
					Data: nil,
				},
			},
		},
		{
			name:           "secret exists but has incorrect content",
			secretExists:   true,
			expectedExists: true,
			expectedSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "additional-pull-secret",
					Namespace: "kube-system",
				},
				Data: map[string][]byte{
					corev1.DockerConfigJsonKey: []byte(`invalid json content`),
				},
			},
			objects: []client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "additional-pull-secret",
						Namespace: "kube-system",
					},
					Data: map[string][]byte{
						corev1.DockerConfigJsonKey: []byte(`invalid json content`),
					},
				},
			},
		},
		{
			name:           "secret does not exist",
			secretExists:   false,
			expectedExists: false,
			expectedSecret: nil,
			objects:        []client.Object{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			fakeClient := fake.NewClientBuilder().WithObjects(tt.objects...).Build()
			exists, secret, err := additionalPullSecretExists(context.Background(), fakeClient)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(exists).To(Equal(tt.expectedExists))

			if tt.expectedSecret != nil {
				g.Expect(secret).NotTo(BeNil())
				g.Expect(secret.Name).To(Equal(tt.expectedSecret.Name))
				g.Expect(secret.Namespace).To(Equal(tt.expectedSecret.Namespace))
				g.Expect(secret.Data).To(Equal(tt.expectedSecret.Data))
			} else {
				g.Expect(secret).To(BeNil())
			}
		})
	}
}
