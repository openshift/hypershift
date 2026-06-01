package kcm

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestAdaptKubeconfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		platformType    hyperv1.PlatformType
		existingObjects []client.Object
		validate        func(*testing.T, *corev1.Secret, error)
	}{
		{
			name:         "When kubeconfig is generated successfully, it should populate secret data",
			platformType: hyperv1.AWSPlatform,
			existingObjects: []client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kube-controller-manager",
						Namespace: "test-namespace",
					},
					Data: map[string][]byte{
						"tls.crt": []byte("test-cert"),
						"tls.key": []byte("test-key"),
					},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "root-ca",
						Namespace: "test-namespace",
					},
					Data: map[string][]byte{
						"ca.crt": []byte("test-ca"),
					},
				},
			},
			validate: func(t *testing.T, secret *corev1.Secret, err error) {
				g := NewWithT(t)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(secret.Data).To(HaveKey(podspec.KubeconfigKey))
				g.Expect(secret.Data[podspec.KubeconfigKey]).ToNot(BeEmpty())
			},
		},
		{
			name:         "When secret data is nil, it should initialize the data map",
			platformType: hyperv1.AzurePlatform,
			existingObjects: []client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kube-controller-manager",
						Namespace: "test-namespace",
					},
					Data: map[string][]byte{
						"tls.crt": []byte("test-cert"),
						"tls.key": []byte("test-key"),
					},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "root-ca",
						Namespace: "test-namespace",
					},
					Data: map[string][]byte{
						"ca.crt": []byte("test-ca"),
					},
				},
			},
			validate: func(t *testing.T, secret *corev1.Secret, err error) {
				g := NewWithT(t)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(secret.Data).ToNot(BeNil())
				g.Expect(secret.Data).To(HaveKey(podspec.KubeconfigKey))
			},
		},
		{
			name:            "When client cert secret is missing, it should return error",
			platformType:    hyperv1.AWSPlatform,
			existingObjects: []client.Object{},
			validate: func(t *testing.T, secret *corev1.Secret, err error) {
				g := NewWithT(t)
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("failed to generate kubeconfig"))
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			g := NewWithT(t)
			scheme := runtime.NewScheme()
			g.Expect(corev1.AddToScheme(scheme)).To(Succeed())
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tc.existingObjects...).Build()

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: tc.platformType,
					},
				},
			}

			mockImageProvider := imageprovider.NewFromImages(map[string]string{})

			cpContext := component.WorkloadContext{
				Context:              t.Context(),
				Client:               fakeClient,
				HCP:                  hcp,
				ReleaseImageProvider: mockImageProvider,
			}

			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kubeconfig",
					Namespace: "test-namespace",
				},
			}

			err := adaptKubeconfig(cpContext, secret)
			tc.validate(t, secret, err)
		})
	}
}
