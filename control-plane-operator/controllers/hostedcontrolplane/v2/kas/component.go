package kas

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	etcdv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/etcd"
	component "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"
)

const (
	ComponentName = "kube-apiserver"
)

var _ component.ComponentOptions = &KubeAPIServer{}

type KubeAPIServer struct {
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (k *KubeAPIServer) IsRequestServing() bool {
	return true
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (k *KubeAPIServer) MultiZoneSpread() bool {
	return true
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (k *KubeAPIServer) NeedsManagementKASAccess() bool {
	return false
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &KubeAPIServer{}).
		WithAdaptFunction(adaptDeployment).
		WithDependencies(etcdv2.ComponentName).
		WithManifestAdapter(
			"service-network-admin-kubeconfig.yaml",
			component.WithAdaptFunction(adaptServiceKubeconfigSecret),
		).
		WithManifestAdapter(
			"capi-kubeconfig.yaml",
			component.WithAdaptFunction(adaptCAPIKubeconfigSecret),
		).
		WithManifestAdapter(
			"hcco-kubeconfig.yaml",
			component.WithAdaptFunction(adaptHCCOKubeconfigSecret),
		).
		WithManifestAdapter(
			"local-kubeconfig.yaml",
			component.WithAdaptFunction(adaptLocalhostKubeconfigSecret),
		).
		WithManifestAdapter(
			"external-admin-kubeconfig.yaml",
			component.WithAdaptFunction(adapExternalAdminKubeconfigSecret),
		).
		WithManifestAdapter(
			"bootstrap-kubeconfig.yaml",
			component.WithAdaptFunction(adaptBootstrapKubeconfigSecret),
		).
		WithManifestAdapter(
			"kas-config.yaml",
			component.WithAdaptFunction(adaptKubeAPIServerConfig),
		).
		WithManifestAdapter(
			"audit-config.yaml",
			component.WithAdaptFunction(AdaptAuditConfig),
		).
		WithManifestAdapter(
			"auth-config.yaml",
			component.WithAdaptFunction(adaptAuthConfig),
		).
		WithManifestAdapter(
			"oauth-metadata.yaml",
			component.WithAdaptFunction(adaptOauthMetadata),
		).
		WithManifestAdapter(
			"authentication-token-webhook-config.yaml",
			component.WithAdaptFunction(adaptAuthenticationTokenWebhookConfigSecret),
		).
		WithManifestAdapter(
			"secret-encryption-config.yaml",
			component.WithPredicate(secretEncryptionConfigPredicate),
			component.WithAdaptFunction(adaptSecretEncryptionConfig),
		).
		WithManifestAdapter(
			"pdb.yaml",
			component.AdaptPodDisruptionBudget(),
		).
		WithManifestAdapter(
			"servicemonitor.yaml",
			component.WithAdaptFunction(adaptServiceMonitor),
		).
		WithManifestAdapter(
			"prometheus-recording-rules.yaml",
			component.WithAdaptFunction(adaptRecordingRules),
		).
		WithManifestAdapter(
			"aws-pod-identity-webhook-kubeconfig.yaml",
			component.EnableForPlatform(hyperv1.AWSPlatform),
			component.WithAdaptFunction(adaptAWSPodIdentityWebhookKubeconfigSecret),
		).
		WatchResource(&corev1.ConfigMap{}, "kas-config").
		WatchResource(&corev1.ConfigMap{}, "kas-audit-config").
		WatchResource(&corev1.ConfigMap{}, "auth-config").
		WatchResource(&corev1.Secret{}, "kas-secret-encryption-config").
		Build()
}
