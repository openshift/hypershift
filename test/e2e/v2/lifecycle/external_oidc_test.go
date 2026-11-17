//go:build e2ev2

package lifecycle

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

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
		Data:       map[string]string{"config.json": `{"apiServerArguments":{"authentication-config":["/etc/kubernetes/auth/auth.json"]}}`},
	}
	readyWebhookAuthConfig := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: controlPlaneNamespace, Name: "external-oidc-webhook-auth-config"},
		Data: map[string]string{
			"auth-config.json": readyAuthConfig.Data["auth.json"],
		},
	}
	readyWebhookKASConfig := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: controlPlaneNamespace, Name: "kas-config"},
		Data: map[string]string{
			"config.json": `{"apiServerArguments":{"authentication-token-webhook-config-file":["/etc/kubernetes/auth-token-webhook/kubeconfig"]}}`,
		},
	}
	readyWebhookDeployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: controlPlaneNamespace, Name: "external-oidc-webhook", Generation: 1},
		Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](1)},
		Status: appsv1.DeploymentStatus{
			ObservedGeneration: 1,
			Replicas:           1,
			UpdatedReplicas:    1,
			ReadyReplicas:      1,
			AvailableReplicas:  1,
		},
	}
	readyWebhookSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: controlPlaneNamespace, Name: "kas-authentication-token-webhook-config"},
		Data: map[string][]byte{
			"kubeconfig": []byte(`{"apiVersion":"v1","kind":"Config","current-context":"webhook","contexts":[{"name":"webhook","context":{"cluster":"webhook"}}],"clusters":[{"name":"webhook","cluster":{"server":"https://external-oidc-webhook.clusters-example.svc:443/apis/oauth.openshift.io/v1/tokenreviews"}}]}`),
		},
	}

	type testCase struct {
		name      string
		objects   []crclient.Object
		wantReady bool
		wantError bool
	}
	tests := []testCase{
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
				readyKASConfig.DeepCopy(),
			},
			wantError: true,
		},
		{
			name: "When KAS configuration is missing, it should report the transition incomplete",
		},
		{
			name: "When KAS configuration is malformed, it should return an error",
			objects: []crclient.Object{&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Namespace: controlPlaneNamespace, Name: "kas-config"},
				Data:       map[string]string{"config.json": "{"},
			}},
			wantError: true,
		},
		{
			name: "When KAS has neither OIDC authentication mode configured, it should report the transition incomplete",
			objects: []crclient.Object{readyAuthConfig.DeepCopy(), &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Namespace: controlPlaneNamespace, Name: "kas-config"},
				Data:       map[string]string{"config.json": `{"apiServerArguments":{}}`},
			}},
		},
		{
			name:    "When the direct OIDC authentication configuration is missing, it should report the transition incomplete",
			objects: []crclient.Object{readyKASConfig.DeepCopy()},
		},
	}

	webhookTests := []struct {
		name      string
		mutate    func(*appsv1.Deployment, *corev1.ConfigMap, *corev1.ConfigMap, *corev1.Secret)
		omit      string
		wantReady bool
		wantError bool
	}{
		{
			name:      "When the webhook is ready with the expected issuer and KAS endpoint, it should report the transition complete",
			wantReady: true,
		},
		{
			name: "When the webhook deployment is missing, it should report the transition incomplete",
			omit: readyWebhookDeployment.Name,
		},
		{
			name: "When the webhook deployment is unavailable, it should report the transition incomplete",
			mutate: func(d *appsv1.Deployment, _, _ *corev1.ConfigMap, _ *corev1.Secret) {
				d.Status.AvailableReplicas = 0
			},
		},
		{
			name: "When the webhook deployment has not observed its latest generation, it should report the transition incomplete",
			mutate: func(d *appsv1.Deployment, _, _ *corev1.ConfigMap, _ *corev1.Secret) {
				d.Generation++
			},
		},
		{
			name: "When the webhook is scaled to zero, it should report the transition incomplete",
			mutate: func(d *appsv1.Deployment, _, _ *corev1.ConfigMap, _ *corev1.Secret) {
				d.Spec.Replicas = ptr.To[int32](0)
				d.Status = appsv1.DeploymentStatus{ObservedGeneration: d.Generation}
			},
		},
		{
			name: "When the webhook authentication configuration is missing, it should report the transition incomplete",
			omit: readyWebhookAuthConfig.Name,
		},
		{
			name: "When the webhook has no JWT authenticators, it should report the transition incomplete",
			mutate: func(_ *appsv1.Deployment, auth, _ *corev1.ConfigMap, _ *corev1.Secret) {
				auth.Data["auth-config.json"] = `{"jwt":[]}`
			},
		},
		{
			name: "When the webhook has a different issuer, it should report the transition incomplete",
			mutate: func(_ *appsv1.Deployment, auth, _ *corev1.ConfigMap, _ *corev1.Secret) {
				auth.Data["auth-config.json"] = `{"jwt":[{"issuer":{"url":"https://previous.example.com"}}]}`
			},
		},
		{
			name: "When the webhook authentication configuration is malformed, it should return an error",
			mutate: func(_ *appsv1.Deployment, auth, _ *corev1.ConfigMap, _ *corev1.Secret) {
				auth.Data["auth-config.json"] = "{"
			},
			wantError: true,
		},
		{
			name: "When KAS still has direct OIDC authentication enabled alongside the webhook, it should report the transition incomplete",
			mutate: func(_ *appsv1.Deployment, _, kas *corev1.ConfigMap, _ *corev1.Secret) {
				kas.Data["config.json"] = `{"apiServerArguments":{"authentication-config":["/etc/kubernetes/auth/auth.json"],"authentication-token-webhook-config-file":["/etc/kubernetes/auth-token-webhook/kubeconfig"]}}`
			},
		},
		{
			name: "When the token webhook secret is missing, it should report the transition incomplete",
			omit: readyWebhookSecret.Name,
		},
		{
			name: "When the token webhook kubeconfig is malformed, it should return an error",
			mutate: func(_ *appsv1.Deployment, _, _ *corev1.ConfigMap, secret *corev1.Secret) {
				secret.Data["kubeconfig"] = []byte("{")
			},
			wantError: true,
		},
		{
			name: "When the token webhook kubeconfig has no current context, it should report the transition incomplete",
			mutate: func(_ *appsv1.Deployment, _, _ *corev1.ConfigMap, secret *corev1.Secret) {
				secret.Data["kubeconfig"] = nil
			},
		},
		{
			name: "When the token webhook kubeconfig references a missing cluster, it should report the transition incomplete",
			mutate: func(_ *appsv1.Deployment, _, _ *corev1.ConfigMap, secret *corev1.Secret) {
				secret.Data["kubeconfig"] = []byte(`{"apiVersion":"v1","kind":"Config","current-context":"webhook","contexts":[{"name":"webhook","context":{"cluster":"missing"}}]}`)
			},
		},
		{
			name: "When KAS still points to the integrated OAuth endpoint, it should report the transition incomplete",
			mutate: func(_ *appsv1.Deployment, _, _ *corev1.ConfigMap, secret *corev1.Secret) {
				secret.Data["kubeconfig"] = []byte(`{"apiVersion":"v1","kind":"Config","current-context":"webhook","contexts":[{"name":"webhook","context":{"cluster":"webhook"}}],"clusters":[{"name":"webhook","cluster":{"server":"https://openshift-oauth-apiserver.clusters-example.svc:443/apis/oauth.openshift.io/v1/tokenreviews"}}]}`)
			},
		},
	}
	for _, tt := range webhookTests {
		deployment := readyWebhookDeployment.DeepCopy()
		auth := readyWebhookAuthConfig.DeepCopy()
		kas := readyWebhookKASConfig.DeepCopy()
		secret := readyWebhookSecret.DeepCopy()
		if tt.mutate != nil {
			tt.mutate(deployment, auth, kas, secret)
		}
		var objects []crclient.Object
		for _, object := range []crclient.Object{deployment, auth, kas, secret} {
			if object.GetName() != tt.omit {
				objects = append(objects, object)
			}
		}
		tests = append(tests, testCase{name: tt.name, objects: objects, wantReady: tt.wantReady, wantError: tt.wantError})
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
			gotReady, err := externalOIDCTransitionComplete(t.Context(), cl, controlPlaneNamespace, issuerURL)
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
