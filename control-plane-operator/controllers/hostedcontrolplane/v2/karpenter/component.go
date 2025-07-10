package karpenter

import (
	"fmt"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	karpenteroperatorv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/karpenteroperator"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	ComponentName = "karpenter"
)

var _ component.ComponentOptions = &karpenterOptions{}

type karpenterOptions struct{}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (c *karpenterOptions) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (c *karpenterOptions) MultiZoneSpread() bool {
	return false
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (c *karpenterOptions) NeedsManagementKASAccess() bool {
	return true
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &karpenterOptions{}).
		WithAdaptFunction(adaptDeployment).
		WithManifestAdapter("podmonitor.yaml",
			component.WithAdaptFunction(adaptPodMonitor),
		).
		WithPredicate(predicate).
		WithDependencies(karpenteroperatorv2.ComponentName).
		InjectAvailabilityProberContainer(util.AvailabilityProberOpts{}).
		Build()
}

func predicate(cpContext component.WorkloadContext) (bool, error) {
	hcp := cpContext.HCP

	// The deployment depends on the kubeconfig being reported.
	if hcp.Status.KubeConfig == nil {
		return false, nil
	}
	// Resolve the kubeconfig secret for CAPI which karpenter is deployed alongside of.
	capiKubeConfigSecret := manifests.KASServiceCAPIKubeconfigSecret(hcp.Namespace, hcp.Spec.InfraID)
	if err := cpContext.Client.Get(cpContext, client.ObjectKeyFromObject(capiKubeConfigSecret), capiKubeConfigSecret); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to get hosted controlplane kubeconfig secret %q: %w", capiKubeConfigSecret.Name, err)
	}

	return true, nil
}
