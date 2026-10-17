package configuration

import (
	"context"
	"fmt"

	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/cco"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	"github.com/openshift/hypershift/support/globalconfig"

	configv1 "github.com/openshift/api/config/v1"

	"k8s.io/apimachinery/pkg/api/equality"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
)

func ReconcileGlobal(ctx context.Context, hosted HostedCluster, controlPlane ControlPlane, params GlobalConfigParams) error {
	hcp := params.hostedControlPlane()
	var errs []error

	apiServerAddress := hcp.Status.ControlPlaneEndpoint.Host

	if len(apiServerAddress) == 0 {
		return fmt.Errorf("hosted control plane does not have an APIServer endpoint address")
	}

	infra := globalconfig.InfrastructureConfig()
	var currentInfra *configv1.Infrastructure
	if _, err := hosted.upsert(ctx, infra, func() error {
		currentInfra = infra.DeepCopy()
		globalconfig.ReconcileInfrastructure(infra, hcp)
		return nil
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile infrastructure config spec: %w", err))
	} else {
		globalconfig.ReconcileInfrastructure(infra, hcp)
		if !equality.Semantic.DeepEqual(infra.Status, currentInfra.Status) {
			if err := hosted.updateStatus(ctx, infra); err != nil {
				errs = append(errs, fmt.Errorf("failed to update infrastructure status: %w", err))
			}
		}
	}

	dns := globalconfig.DNSConfig()
	if _, err := hosted.upsert(ctx, dns, func() error {
		globalconfig.ReconcileDNSConfig(dns, hcp)
		return nil
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile dns config: %w", err))
	}

	image := globalconfig.ImageConfig()
	if _, err := hosted.upsert(ctx, image, func() error {
		globalconfig.ReconcileImageConfig(image, hcp)
		return nil
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile image config: %w", err))
	}

	ingress := globalconfig.IngressConfig()
	if _, err := hosted.upsert(ctx, ingress, func() error {
		globalconfig.ReconcileIngressConfig(ingress, hcp)
		return nil
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile ingress config: %w", err))
	}

	networkConfig := globalconfig.NetworkConfig()
	if _, err := hosted.upsert(ctx, networkConfig, func() error {
		if err := globalconfig.ReconcileNetworkConfig(networkConfig, hcp); err != nil {
			errs = append(errs, fmt.Errorf("failed to reconcile network config: %w", err))
		}
		return nil
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to create network config: %w", err))
	}

	if err := ReconcileProxyTrustedCA(ctx, hosted, controlPlane, ProxyParamsFor(hcp.Namespace, hcp.Spec.Configuration)); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile proxy TrustedCA configmap: %w", err))
	}

	proxy := globalconfig.ProxyConfig()
	if _, err := hosted.upsert(ctx, proxy, func() error {
		globalconfig.ReconcileInClusterProxyConfig(proxy, hcp.Spec.Configuration)
		return nil
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile proxy config: %w", err))
	}

	err := ReconcileImagePolicy(ctx, hosted, hcp.Spec.ImageContentSources)
	if err != nil {
		errs = append(errs, err)
	}

	installConfigCM := manifests.InstallConfigConfigMap()
	if _, err := hosted.upsert(ctx, installConfigCM, func() error {
		installConfigCM.Data = map[string]string{
			"install-config": globalconfig.NewInstallConfig(hcp).String(),
		}
		return nil
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile dns config: %w", err))
	}

	cloudCredentialConfig := manifests.CloudCredential()
	if _, err := hosted.upsert(ctx, cloudCredentialConfig, func() error {
		cco.ReconcileCloudCredentialConfig(cloudCredentialConfig)
		return nil
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile cloud credential config: %w", err))
	}

	authenticationConfig := globalconfig.AuthenticationConfiguration()
	if _, err := hosted.upsert(ctx, authenticationConfig, func() error {
		return globalconfig.ReconcileAuthenticationConfiguration(authenticationConfig, hcp.Spec.Configuration, hcp.Spec.IssuerURL)
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile authentication config: %w", err))
	}

	apiServerConfig := globalconfig.APIServerConfiguration()
	if _, err := hosted.upsert(ctx, apiServerConfig, func() error {
		return globalconfig.ReconcileAPIServerConfiguration(apiServerConfig, hcp.Spec.Configuration)
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile apiserver config: %w", err))
	}

	return utilerrors.NewAggregate(errs)
}
