package kcm

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/api"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/k8sutil"

	kcpv1 "github.com/openshift/api/kubecontrolplane/v1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestAdaptConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		existingObjects []client.Object
		configData      string
		validate        func(*testing.T, *corev1.ConfigMap, error)
	}{
		{
			name: "When service serving CA exists, it should set CertFile in config",
			existingObjects: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "service-serving-ca",
						Namespace: "test-namespace",
					},
					Data: map[string]string{
						"service-ca.crt": "test-ca-data",
					},
				},
			},
			configData: `{"kind":"KubeControllerManagerConfig","apiVersion":"kubecontrolplane.config.openshift.io/v1"}`,
			validate: func(t *testing.T, cm *corev1.ConfigMap, err error) {
				g := NewWithT(t)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cm.Data).To(HaveKey(KubeControllerManagerConfigKey))

				config := &kcpv1.KubeControllerManagerConfig{}
				decodeErr := k8sutil.DeserializeResource(cm.Data[KubeControllerManagerConfigKey], config, api.Scheme)
				g.Expect(decodeErr).ToNot(HaveOccurred())
				g.Expect(config.ServiceServingCert.CertFile).To(Equal("/etc/kubernetes/certs/service-ca/service-ca.crt"))
			},
		},
		{
			name:            "When service serving CA does not exist, it should not set CertFile",
			existingObjects: []client.Object{},
			configData:      `{"kind":"KubeControllerManagerConfig","apiVersion":"kubecontrolplane.config.openshift.io/v1"}`,
			validate: func(t *testing.T, cm *corev1.ConfigMap, err error) {
				g := NewWithT(t)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cm.Data).To(HaveKey(KubeControllerManagerConfigKey))

				config := &kcpv1.KubeControllerManagerConfig{}
				decodeErr := k8sutil.DeserializeResource(cm.Data[KubeControllerManagerConfigKey], config, api.Scheme)
				g.Expect(decodeErr).ToNot(HaveOccurred())
				g.Expect(config.ServiceServingCert.CertFile).To(BeEmpty())
			},
		},
		{
			name:            "When config data is invalid, it should return error",
			existingObjects: []client.Object{},
			configData:      `invalid json`,
			validate: func(t *testing.T, cm *corev1.ConfigMap, err error) {
				g := NewWithT(t)
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("unable to decode existing KubeControllerManager configuration"))
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			scheme := runtime.NewScheme()
			_ = corev1.AddToScheme(scheme)
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tc.existingObjects...).Build()

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
				},
			}

			cpContext := component.WorkloadContext{
				Context: t.Context(),
				Client:  fakeClient,
				HCP:     hcp,
			}

			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kcm-config",
					Namespace: "test-namespace",
				},
				Data: map[string]string{
					KubeControllerManagerConfigKey: tc.configData,
				},
			}

			err := adaptConfig(cpContext, cm)
			tc.validate(t, cm, err)
		})
	}
}

func TestAdaptRecyclerConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		inputTemplate  string
		toolsImage     string
		expectedOutput string
	}{
		{
			name:           "When template has tools image placeholder, it should replace it",
			inputTemplate:  "image: {{.tools_image}}",
			toolsImage:     "quay.io/openshift/tools:v1.0.0",
			expectedOutput: "image: quay.io/openshift/tools:v1.0.0",
		},
		{
			name: "When template has multiple lines with placeholder, it should replace first occurrence only",
			inputTemplate: `apiVersion: v1
kind: Pod
spec:
  containers:
  - image: {{.tools_image}}
    name: recycler
  - image: {{.tools_image}}
    name: other`,
			toolsImage: "registry.io/tools:latest",
			expectedOutput: `apiVersion: v1
kind: Pod
spec:
  containers:
  - image: registry.io/tools:latest
    name: recycler
  - image: {{.tools_image}}
    name: other`,
		},
		{
			name:           "When template has no placeholder, it should remain unchanged",
			inputTemplate:  "image: static-image:v1.0.0",
			toolsImage:     "quay.io/openshift/tools:v1.0.0",
			expectedOutput: "image: static-image:v1.0.0",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			mockImageProvider := imageprovider.NewFromImages(map[string]string{
				"tools": tc.toolsImage,
			})

			cpContext := component.WorkloadContext{
				Context:              t.Context(),
				ReleaseImageProvider: mockImageProvider,
			}

			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "recycler-config",
					Namespace: "test-namespace",
				},
				Data: map[string]string{
					RecyclerPodTemplateKey: tc.inputTemplate,
				},
			}

			err := adaptRecyclerConfig(cpContext, cm)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(cm.Data[RecyclerPodTemplateKey]).To(Equal(tc.expectedOutput))
		})
	}
}

func TestGetServiceServingCA(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		existingObjects []client.Object
		validate        func(*testing.T, *corev1.ConfigMap, error)
	}{
		{
			name: "When service serving CA exists, it should return the ConfigMap",
			existingObjects: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "service-serving-ca",
						Namespace: "test-namespace",
					},
					Data: map[string]string{
						"service-ca.crt": "test-ca-data",
					},
				},
			},
			validate: func(t *testing.T, cm *corev1.ConfigMap, err error) {
				g := NewWithT(t)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cm).ToNot(BeNil())
				g.Expect(cm.Name).To(Equal("service-serving-ca"))
				g.Expect(cm.Data).To(HaveKey("service-ca.crt"))
			},
		},
		{
			name:            "When service serving CA does not exist, it should return nil without error",
			existingObjects: []client.Object{},
			validate: func(t *testing.T, cm *corev1.ConfigMap, err error) {
				g := NewWithT(t)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cm).To(BeNil())
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			scheme := runtime.NewScheme()
			_ = corev1.AddToScheme(scheme)
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tc.existingObjects...).Build()

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
				},
			}

			cpContext := component.WorkloadContext{
				Context: t.Context(),
				Client:  fakeClient,
				HCP:     hcp,
			}

			cm, err := getServiceServingCA(cpContext)
			tc.validate(t, cm, err)
		})
	}
}

func TestGetServiceServingCAError(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	// Create a client that will return an error
	fakeClient := &erroringClient{
		Reader: fake.NewClientBuilder().WithScheme(scheme).Build(),
		err:    apierrors.NewServiceUnavailable("test error"),
	}

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hcp",
			Namespace: "test-namespace",
		},
	}

	cpContext := component.WorkloadContext{
		Context: t.Context(),
		Client:  fakeClient,
		HCP:     hcp,
	}

	_, err := getServiceServingCA(cpContext)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("failed to get service serving CA"))
}

// erroringClient wraps a client.Reader and returns a custom error on Get
type erroringClient struct {
	client.Reader
	err error
}

func (e *erroringClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if key.Name == manifests.ServiceServingCA(key.Namespace).Name {
		return e.err
	}
	return e.Reader.Get(ctx, key, obj, opts...)
}
