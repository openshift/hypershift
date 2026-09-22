package kas

import (
	"fmt"
	"slices"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	etcdv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/etcd"
	extoidc "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/external_oidc_webhook"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/fg"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/kas/kms"
	"github.com/openshift/hypershift/support/azureutil"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"
	hyperutils "github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
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
		WithPredicate(checkExternalOIDCWebhookMigration).
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
			"kas-bootstrap-container-kubeconfig.yaml",
			component.WithAdaptFunction(adaptKASBootstrapContainerKubeconfigSecret),
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
			component.WithPredicate(enableDirectOIDCAuthenticationConfig),
			component.WithAdaptFunction(adaptAuthConfig),
		).
		WithManifestAdapter(
			"oauth-metadata.yaml",
			component.WithPredicate(enableOAuthMetadata),
			component.WithAdaptFunction(adaptOauthMetadata),
		).
		WithManifestAdapter(
			"authentication-token-webhook-config.yaml",
			component.WithPredicate(enableTokenWebhookAuthenticator),
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
		WithManifestAdapter(
			"azure-workload-identity-webhook-kubeconfig.yaml",
			component.EnableForPlatform(hyperv1.AzurePlatform),
			component.WithAdaptFunction(adaptAzureWorkloadIdentityWebhookKubeconfigSecret),
			component.ReconcileExisting(),
		).
		WithManifestAdapter(
			"gcp-workload-identity-federation-webhook-kubeconfig.yaml",
			component.EnableForPlatform(hyperv1.GCPPlatform),
			component.WithAdaptFunction(adaptGCPWorkloadIdentityFederationWebhookKubeconfigSecret),
			component.ReconcileExisting(),
		).
		WithManifestAdapter(
			"azure-kms-secretprovider.yaml",
			component.WithAdaptFunction(kms.AdaptAzureSecretProvider),
			component.WithPredicate(enableAzureKMSSecretProvider),
		).
		Build()
}

func enableAzureKMSSecretProvider(cpContext component.WorkloadContext) bool {
	if cpContext.HCP.Spec.SecretEncryption != nil && cpContext.HCP.Spec.SecretEncryption.KMS != nil && cpContext.HCP.Spec.SecretEncryption.Type == hyperv1.KMS {
		return azureutil.IsAroHCPByHCP(cpContext.HCP)
	}
	return false
}

// enableIfCustomKubeconfig is a helper predicate for the common use case of enabling a resource when a KubeAPICustomKubeconfig is specified.
func enableIfCustomKubeconfig(cpContext component.WorkloadContext) bool {
	return hyperutils.EnableIfCustomKubeconfig(cpContext.HCP)
}

func enableDirectOIDCAuthenticationConfig(cpContext component.WorkloadContext) bool {
	configuration := cpContext.HCP.Spec.Configuration
	return configuration != nil && usesDirectOIDCAuthenticationConfig(configuration.Authentication)
}

func enableTokenWebhookAuthenticator(cpContext component.WorkloadContext) bool {
	configuration := cpContext.HCP.Spec.Configuration
	if configuration == nil {
		return usesTokenWebhookAuthenticator(nil)
	}
	return usesTokenWebhookAuthenticator(configuration.Authentication)
}

// checkExternalOIDCWebhookMigration delays switching an existing KAS from direct
// OIDC authentication to the webhook until the webhook Deployment and Service
// endpoints are ready. While waiting, KAS and its existing configuration are left unchanged.
// If no KAS Deployment exists, reconciliation proceeds so KAS can start and unblock
// the webhook's init container. Once KAS is configured to use the webhook, this check
// no longer blocks reconciliation, even if the webhook becomes unavailable.
// Waiting is reported as a reconciliation error to trigger retries, rather than a
// dedicated waiting condition. Always return true; false with a nil error would delete KAS.
func checkExternalOIDCWebhookMigration(cpContext component.WorkloadContext) (bool, error) {
	configuration := cpContext.HCP.Spec.Configuration
	if configuration == nil || !usesExternalOIDCAsWebhook(configuration.Authentication) {
		return true, nil
	}

	kasDeployment := &appsv1.Deployment{}
	if err := cpContext.Client.Get(cpContext, client.ObjectKey{Namespace: cpContext.HCP.Namespace, Name: ComponentName}, kasDeployment); err != nil {
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		return true, fmt.Errorf("checking external OIDC webhook migration: failed to get KAS deployment: %w", err)
	}

	// Legacy deployments mount auth-config; webhook-mode deployments do not.
	if !slices.ContainsFunc(kasDeployment.Spec.Template.Spec.Volumes, func(volume corev1.Volume) bool {
		return volume.Name == authConfigVolumeName
	}) {
		return true, nil
	}

	const webhookName = extoidc.ComponentName
	webhookDeployment := &appsv1.Deployment{}
	if err := cpContext.Client.Get(cpContext, client.ObjectKey{Namespace: cpContext.HCP.Namespace, Name: webhookName}, webhookDeployment); err != nil {
		return true, fmt.Errorf("waiting to migrate KAS authentication: failed to get external OIDC webhook deployment: %w", err)
	}
	if webhookDeployment.Status.AvailableReplicas == 0 || !podspec.IsDeploymentReady(cpContext, webhookDeployment) {
		return true, fmt.Errorf("waiting to migrate KAS authentication: external OIDC webhook deployment is not ready")
	}

	endpointSlices := &discoveryv1.EndpointSliceList{}
	if err := cpContext.Client.List(cpContext, endpointSlices, client.InNamespace(cpContext.HCP.Namespace), client.MatchingLabels{discoveryv1.LabelServiceName: webhookName}); err != nil {
		return true, fmt.Errorf("checking external OIDC webhook migration: failed to list webhook endpoints: %w", err)
	}
	for _, endpointSlice := range endpointSlices.Items {
		for _, endpoint := range endpointSlice.Endpoints {
			if len(endpoint.Addresses) > 0 && ptr.Deref(endpoint.Conditions.Ready, true) && !ptr.Deref(endpoint.Conditions.Terminating, false) {
				return true, nil
			}
		}
	}
	return true, fmt.Errorf("waiting to migrate KAS authentication: external OIDC webhook service has no ready endpoints")
}
