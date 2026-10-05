package certs_test

import (
	"testing"

	"github.com/openshift/hypershift/support/certs"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/stretchr/testify/require"
)

func TestConfigMapCABundleResolver(t *testing.T) {
	const (
		namespace = "test-namespace"
		name      = "issuer-ca"
	)

	testCases := []struct {
		name      string
		objects   []client.Object
		expected  string
		errorText string
	}{
		{
			name:      "When the ConfigMap does not exist, it should return an error",
			errorText: `failed to get CA configmap "issuer-ca"`,
		},
		{
			name: "When the ConfigMap does not contain the CA bundle key, it should return an error",
			objects: []client.Object{&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
			}},
			errorText: `CA configmap "issuer-ca" key "ca-bundle.crt" is missing`,
		},
		{
			name: "When the CA bundle is empty, it should return an error",
			objects: []client.Object{&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
				Data:       map[string]string{certs.UserCABundleMapKey: ""},
			}},
			errorText: `CA configmap "issuer-ca" key "ca-bundle.crt" is empty`,
		},
		{
			name: "When the ConfigMap contains a CA bundle, it should return the bundle",
			objects: []client.Object{&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
				Data:       map[string]string{certs.UserCABundleMapKey: "ca-bundle"},
			}},
			expected: "ca-bundle",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			require.NoError(t, corev1.AddToScheme(scheme))
			reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tc.objects...).Build()

			resolver := certs.ConfigMapCABundleResolver(t.Context(), reader, namespace)
			bundle, err := resolver(name)
			if tc.errorText != "" {
				require.ErrorContains(t, err, tc.errorText)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.expected, bundle)
		})
	}
}
