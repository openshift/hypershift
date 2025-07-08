package kas

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	etcdv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/etcd"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/fg"
	component "github.com/openshift/hypershift/support/controlplane-component"
	hyperutils "github.com/openshift/hypershift/support/util"
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

// NewComponents returns the appropriate KAS component(s) based on environment configuration
// This function can be used during component registration to conditionally create
// KAS with or without Azure KMS dependencies based on global configuration
func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &KubeAPIServer{}).
		WithAdaptFunction(adaptDeployment).
		WithDependencies(etcdv2.ComponentName, fg.ComponentName).
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
			"custom-admin-kubeconfig.yaml",
			component.WithAdaptFunction(adaptCustomAdminKubeconfigSecret),
			component.WithPredicate(enableIfCustomKubeconfig),
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
			component.ReconcileExisting(),
		).
		Build()
}

// enableIfCustomKubeconfig is a helper predicate for the common use case of enabling a resource when a KubeAPICustomKubeconfig is specified.
func enableIfCustomKubeconfig(cpContext component.WorkloadContext) bool {
	return hyperutils.EnableIfCustomKubeconfig(cpContext.HCP)
}
