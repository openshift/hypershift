package autoscaler

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	ComponentName = "cluster-autoscaler"
)

var _ component.ComponentOptions = &Autoscaler{}

type Autoscaler struct {
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (a *Autoscaler) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (a *Autoscaler) MultiZoneSpread() bool {
	return false
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (a *Autoscaler) NeedsManagementKASAccess() bool {
	return true
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &Autoscaler{}).
		WithAdaptFunction(adaptDeployment).
		WithManifestAdapter("podmonitor.yaml",
			component.WithAdaptFunction(adaptPodMonitor),
		).
		WithPredicate(predicate).
		InjectAvailabilityProberContainer(util.AvailabilityProberOpts{}).
		Build()
}

func predicate(cpContext component.WorkloadContext) (bool, error) {
	hcp := cpContext.HCP

	// Disable cluster-autoscaler component if DisableMachineManagement label is set.
	if _, exists := hcp.Annotations[hyperv1.DisableMachineManagement]; exists {
		return false, nil
	}

	// The deployment depends on the kubeconfig being reported.
	if hcp.Status.KubeConfig == nil {
		return false, nil
	}
	// Resolve the kubeconfig secret for CAPI which the autoscaler is deployed alongside of.
	capiKubeConfigSecret := manifests.KASServiceCAPIKubeconfigSecret(hcp.Namespace, hcp.Spec.InfraID)
	if err := cpContext.Client.Get(cpContext, client.ObjectKeyFromObject(capiKubeConfigSecret), capiKubeConfigSecret); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to get hosted controlplane kubeconfig secret %q: %w", capiKubeConfigSecret.Name, err)
	}

	return true, nil
}
