package kas

import (
	"context"
	"errors"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/featuregates"
	"github.com/openshift/hypershift/support/api"
	component "github.com/openshift/hypershift/support/controlplane-component"

	configv1 "github.com/openshift/api/config/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fgtesting "k8s.io/component-base/featuregate/testing"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/stretchr/testify/require"
)

func TestCheckExternalOIDCWebhookMigration(t *testing.T) {
	const namespace = "test-namespace"
	configuration := &hyperv1.ClusterConfiguration{Authentication: &configv1.AuthenticationSpec{Type: configv1.AuthenticationTypeOIDC}}
	legacyKAS := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: ComponentName, Namespace: namespace},
		Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
			Volumes: []corev1.Volume{{Name: authConfigVolumeName}},
		}}},
	}
	migratedKAS := legacyKAS.DeepCopy()
	migratedKAS.Spec.Template.Spec.Volumes = nil
	readyWebhook := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "external-oidc-webhook", Namespace: namespace, Generation: 2},
		Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](1)},
		Status: appsv1.DeploymentStatus{
			ObservedGeneration: 2,
			Replicas:           1,
			UpdatedReplicas:    1,
			ReadyReplicas:      1,
			AvailableReplicas:  1,
		},
	}
	unreadyWebhook := readyWebhook.DeepCopy()
	unreadyWebhook.Status.ReadyReplicas = 0
	staleWebhook := readyWebhook.DeepCopy()
	staleWebhook.Status.ObservedGeneration = 1
	scaledDownWebhook := readyWebhook.DeepCopy()
	scaledDownWebhook.Spec.Replicas = ptr.To[int32](0)
	scaledDownWebhook.Status = appsv1.DeploymentStatus{ObservedGeneration: 2}
	readyEndpoints := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name: "webhook-endpoints", Namespace: namespace,
			Labels: map[string]string{discoveryv1.LabelServiceName: "external-oidc-webhook"},
		},
		AddressType: discoveryv1.AddressTypeIPv4,
		Endpoints: []discoveryv1.Endpoint{{
			Addresses:  []string{"10.0.0.1"},
			Conditions: discoveryv1.EndpointConditions{Ready: ptr.To(true)},
		}},
	}
	unreadyEndpoints := readyEndpoints.DeepCopy()
	unreadyEndpoints.Endpoints[0].Conditions.Ready = ptr.To(false)
	terminatingEndpoints := readyEndpoints.DeepCopy()
	terminatingEndpoints.Endpoints[0].Conditions.Terminating = ptr.To(true)
	unknownReadinessEndpoints := readyEndpoints.DeepCopy()
	unknownReadinessEndpoints.Endpoints[0].Conditions.Ready = nil
	unrelatedEndpoints := readyEndpoints.DeepCopy()
	unrelatedEndpoints.Labels[discoveryv1.LabelServiceName] = "other-service"
	otherNamespaceEndpoints := readyEndpoints.DeepCopy()
	otherNamespaceEndpoints.Namespace = "other-namespace"
	lookupErr := errors.New("API unavailable")

	testCases := []struct {
		name          string
		configuration *hyperv1.ClusterConfiguration
		disableGate   bool
		objects       []client.Object
		getErrorFor   string
		listError     bool
		expectedError string
	}{
		{
			name:        "When configuration is absent, it should allow reconciliation",
			getErrorFor: ComponentName,
		},
		{
			name:          "When authentication is integrated OAuth, it should allow reconciliation",
			configuration: &hyperv1.ClusterConfiguration{Authentication: &configv1.AuthenticationSpec{Type: configv1.AuthenticationTypeIntegratedOAuth}},
			getErrorFor:   ComponentName,
		},
		{
			name:          "When the webhook gate is disabled, it should allow reconciliation",
			configuration: configuration, disableGate: true, getErrorFor: ComponentName,
		},
		{
			name:          "When KAS does not exist, it should allow bootstrap without a webhook",
			configuration: configuration,
		},
		{
			name:          "When KAS has already migrated, it should allow reconciliation without a webhook",
			configuration: configuration, objects: []client.Object{migratedKAS},
		},
		{
			name:          "When the webhook deployment is missing, it should wait before migrating",
			configuration: configuration, objects: []client.Object{legacyKAS},
			expectedError: "failed to get external OIDC webhook deployment",
		},
		{
			name:          "When the webhook deployment is not ready, it should wait before migrating",
			configuration: configuration, objects: []client.Object{legacyKAS, unreadyWebhook, readyEndpoints},
			expectedError: "deployment is not ready",
		},
		{
			name:          "When the webhook status is stale, it should wait before migrating",
			configuration: configuration, objects: []client.Object{legacyKAS, staleWebhook, readyEndpoints},
			expectedError: "deployment is not ready",
		},
		{
			name:          "When the webhook is scaled to zero, it should wait before migrating",
			configuration: configuration, objects: []client.Object{legacyKAS, scaledDownWebhook, readyEndpoints},
			expectedError: "deployment is not ready",
		},
		{
			name:          "When endpoint slices are missing, it should wait before migrating",
			configuration: configuration, objects: []client.Object{legacyKAS, readyWebhook},
			expectedError: "service has no ready endpoints",
		},
		{
			name:          "When endpoints are not ready, it should wait before migrating",
			configuration: configuration, objects: []client.Object{legacyKAS, readyWebhook, unreadyEndpoints},
			expectedError: "service has no ready endpoints",
		},
		{
			name:          "When endpoints are terminating, it should wait before migrating",
			configuration: configuration, objects: []client.Object{legacyKAS, readyWebhook, terminatingEndpoints},
			expectedError: "service has no ready endpoints",
		},
		{
			name:          "When ready endpoints belong to another service, it should wait before migrating",
			configuration: configuration, objects: []client.Object{legacyKAS, readyWebhook, unrelatedEndpoints},
			expectedError: "service has no ready endpoints",
		},
		{
			name:          "When ready endpoints belong to another namespace, it should wait before migrating",
			configuration: configuration, objects: []client.Object{legacyKAS, readyWebhook, otherNamespaceEndpoints},
			expectedError: "service has no ready endpoints",
		},
		{
			name:          "When the webhook deployment and endpoints are ready, it should allow migration",
			configuration: configuration, objects: []client.Object{legacyKAS, readyWebhook, readyEndpoints},
		},
		{
			name:          "When endpoint readiness is unspecified, it should treat the endpoint as ready",
			configuration: configuration, objects: []client.Object{legacyKAS, readyWebhook, unknownReadinessEndpoints},
		},
		{
			name:          "When reading KAS fails, it should return the lookup error",
			configuration: configuration, getErrorFor: ComponentName,
			expectedError: "failed to get KAS deployment",
		},
		{
			name:          "When reading the webhook fails, it should return the lookup error",
			configuration: configuration, objects: []client.Object{legacyKAS}, getErrorFor: "external-oidc-webhook",
			expectedError: "failed to get external OIDC webhook deployment",
		},
		{
			name:          "When listing endpoints fails, it should return the lookup error",
			configuration: configuration, objects: []client.Object{legacyKAS, readyWebhook}, listError: true,
			expectedError: "failed to list webhook endpoints",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fgtesting.SetFeatureGateDuringTest(t, featuregates.Gate(), featuregates.ExternalOIDCAsWebhook, !tc.disableGate)
			fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(tc.objects...).
				WithInterceptorFuncs(interceptor.Funcs{
					Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
						if key.Name == tc.getErrorFor {
							return lookupErr
						}
						return c.Get(ctx, key, obj, opts...)
					},
					List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
						if tc.listError {
							return lookupErr
						}
						return c.List(ctx, list, opts...)
					},
				}).Build()
			cpContext := component.WorkloadContext{
				Context: t.Context(), Client: fakeClient,
				HCP: &hyperv1.HostedControlPlane{
					ObjectMeta: metav1.ObjectMeta{Namespace: namespace},
					Spec:       hyperv1.HostedControlPlaneSpec{Configuration: tc.configuration},
				},
			}
			enabled, err := checkExternalOIDCWebhookMigration(cpContext)
			require.True(t, enabled, "the migration guard must never request deletion of KAS")
			if tc.expectedError == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.expectedError)
				if tc.getErrorFor != "" || tc.listError {
					require.ErrorIs(t, err, lookupErr)
				}
			}
		})
	}
}

func TestCheckExternalOIDCWebhookMigrationPreservesResources(t *testing.T) {
	fgtesting.SetFeatureGateDuringTest(t, featuregates.Gate(), featuregates.ExternalOIDCAsWebhook, true)
	const namespace = "test-namespace"
	objects := []client.Object{
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: ComponentName, Namespace: namespace},
			Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
				Volumes: []corev1.Volume{{Name: authConfigVolumeName}},
			}}},
		},
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "auth-config", Namespace: namespace},
			Data:       map[string]string{AuthenticationConfigKey: "existing authentication configuration"},
		},
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "kas-config", Namespace: namespace},
			Data:       map[string]string{KubeAPIServerConfigKey: "existing KAS configuration"},
		},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(objects...).Build()
	for _, obj := range objects {
		require.NoError(t, fakeClient.Get(t.Context(), client.ObjectKeyFromObject(obj), obj))
	}
	err := NewComponent().Reconcile(component.ControlPlaneContext{
		Context: t.Context(), Client: fakeClient,
		HCP: &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{Namespace: namespace},
			Spec: hyperv1.HostedControlPlaneSpec{Configuration: &hyperv1.ClusterConfiguration{
				Authentication: &configv1.AuthenticationSpec{Type: configv1.AuthenticationTypeOIDC},
			}},
		},
	})
	require.ErrorContains(t, err, "waiting to migrate KAS authentication")
	for _, before := range objects {
		after := before.DeepCopyObject().(client.Object)
		require.NoError(t, fakeClient.Get(t.Context(), client.ObjectKeyFromObject(before), after))
		require.Equal(t, before, after)
	}
}

func TestEnableDirectOIDCAuthenticationConfig(t *testing.T) {
	oidcAuthentication := &configv1.AuthenticationSpec{
		Type:          configv1.AuthenticationTypeOIDC,
		OIDCProviders: []configv1.OIDCProvider{{Name: "test-provider"}},
	}
	testCases := []struct {
		name                  string
		configuration         *hyperv1.ClusterConfiguration
		enableExternalWebhook bool
		expected              bool
	}{
		{
			name: "When cluster configuration is absent, it should return false",
		},
		{
			name:          "When integrated OAuth is configured, it should return false",
			configuration: &hyperv1.ClusterConfiguration{Authentication: &configv1.AuthenticationSpec{Type: configv1.AuthenticationTypeIntegratedOAuth}},
		},
		{
			name:          "When direct OIDC authentication is configured, it should return true",
			configuration: &hyperv1.ClusterConfiguration{Authentication: oidcAuthentication},
			expected:      true,
		},
		{
			name:                  "When external OIDC webhook authentication is configured, it should return false",
			configuration:         &hyperv1.ClusterConfiguration{Authentication: oidcAuthentication},
			enableExternalWebhook: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fgtesting.SetFeatureGateDuringTest(t, featuregates.Gate(), featuregates.ExternalOIDCAsWebhook, tc.enableExternalWebhook)
			cpContext := component.WorkloadContext{HCP: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{Configuration: tc.configuration},
			}}

			require.Equal(t, tc.expected, enableDirectOIDCAuthenticationConfig(cpContext))
		})
	}
}

func TestEnableTokenWebhookAuthenticator(t *testing.T) {
	oidcAuthentication := &configv1.AuthenticationSpec{
		Type:          configv1.AuthenticationTypeOIDC,
		OIDCProviders: []configv1.OIDCProvider{{Name: "test-provider"}},
	}
	testCases := []struct {
		name                  string
		configuration         *hyperv1.ClusterConfiguration
		enableExternalWebhook bool
		expected              bool
	}{
		{
			name:     "When cluster configuration is absent, it should return true",
			expected: true,
		},
		{
			name:          "When authentication configuration is absent, it should return true",
			configuration: &hyperv1.ClusterConfiguration{},
			expected:      true,
		},
		{
			name:          "When integrated OAuth is configured, it should return true",
			configuration: &hyperv1.ClusterConfiguration{Authentication: &configv1.AuthenticationSpec{Type: configv1.AuthenticationTypeIntegratedOAuth}},
			expected:      true,
		},
		{
			name:          "When direct OIDC authentication is configured, it should return false",
			configuration: &hyperv1.ClusterConfiguration{Authentication: oidcAuthentication},
		},
		{
			name:                  "When external OIDC webhook authentication is configured, it should return true",
			configuration:         &hyperv1.ClusterConfiguration{Authentication: oidcAuthentication},
			enableExternalWebhook: true,
			expected:              true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fgtesting.SetFeatureGateDuringTest(t, featuregates.Gate(), featuregates.ExternalOIDCAsWebhook, tc.enableExternalWebhook)
			cpContext := component.WorkloadContext{HCP: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{Configuration: tc.configuration},
			}}

			require.Equal(t, tc.expected, enableTokenWebhookAuthenticator(cpContext))
		})
	}
}
