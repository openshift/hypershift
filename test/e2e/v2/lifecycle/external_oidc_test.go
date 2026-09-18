//go:build e2ev2

package lifecycle

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestExternalOIDCTransitionComplete(t *testing.T) {
	t.Parallel()

	const (
		controlPlaneNamespace = "clusters-example"
		issuerURL             = "https://keycloak.apps.example.com/realms/master"
	)

	readyAuthConfig := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: controlPlaneNamespace, Name: "auth-config"},
		Data: map[string]string{
			"auth.json": `{"jwt":[{"issuer":{"url":"https://keycloak.apps.example.com/realms/master"}}]}`,
		},
	}
	readyKASConfig := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: controlPlaneNamespace, Name: "kas-config"},
		Data:       map[string]string{"config.json": `{"apiServerArguments":{}}`},
	}

	tests := []struct {
		name      string
		objects   []crclient.Object
		wantReady bool
		wantError bool
	}{
		{
			name:      "When OAuth deployments are absent and KAS uses the expected issuer, it should report the transition complete",
			objects:   []crclient.Object{readyAuthConfig.DeepCopy(), readyKASConfig.DeepCopy()},
			wantReady: true,
		},
		{
			name: "When an OAuth deployment still exists, it should report the transition incomplete",
			objects: []crclient.Object{
				readyAuthConfig.DeepCopy(),
				readyKASConfig.DeepCopy(),
				&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: controlPlaneNamespace, Name: "oauth-openshift"}},
			},
		},
		{
			name: "When KAS still uses the previous authentication configuration, it should report the transition incomplete",
			objects: []crclient.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Namespace: controlPlaneNamespace, Name: "auth-config"},
					Data:       map[string]string{"auth.json": `{"jwt":[]}`},
				},
				readyKASConfig.DeepCopy(),
			},
		},
		{
			name: "When KAS still uses OAuth webhook authentication, it should report the transition incomplete",
			objects: []crclient.Object{
				readyAuthConfig.DeepCopy(),
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Namespace: controlPlaneNamespace, Name: "kas-config"},
					Data: map[string]string{
						"config.json": `{"apiServerArguments":{"authentication-token-webhook-config-file":["/etc/kubernetes/auth-token-webhook-config.yaml"]}}`,
					},
				},
			},
		},
		{
			name: "When the authentication configuration is malformed, it should return an error",
			objects: []crclient.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Namespace: controlPlaneNamespace, Name: "auth-config"},
					Data:       map[string]string{"auth.json": "{"},
				},
			},
			wantError: true,
		},
	}

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("adding core API to scheme: %v", err)
	}
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("adding apps API to scheme: %v", err)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tt.objects...).Build()
			gotReady, err := externalOIDCTransitionComplete(context.Background(), cl, controlPlaneNamespace, issuerURL)
			if tt.wantError {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("externalOIDCTransitionComplete returned an unexpected error: %v", err)
			}
			if gotReady != tt.wantReady {
				t.Errorf("externalOIDCTransitionComplete returned %t, want %t", gotReady, tt.wantReady)
			}
		})
	}
}
