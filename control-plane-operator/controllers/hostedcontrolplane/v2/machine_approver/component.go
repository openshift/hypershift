package machineapprover

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
	ComponentName = "machine-approver"
)

var _ component.ComponentOptions = &machineApprover{}

type machineApprover struct {
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (a *machineApprover) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (a *machineApprover) MultiZoneSpread() bool {
	return false
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (a *machineApprover) NeedsManagementKASAccess() bool {
	return true
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &machineApprover{}).
		WithAdaptFunction(adaptDeployment).
		WithPredicate(predicate).
		InjectAvailabilityProberContainer(util.AvailabilityProberOpts{}).
		Build()
}

func predicate(cpContext component.WorkloadContext) (bool, error) {
	hcp := cpContext.HCP

	// Disable machineApprover component if DisableMachineManagement label is set.
	if _, exists := hcp.Annotations[hyperv1.DisableMachineManagement]; exists {
		return false, nil
	}

	// The deployment depends on the kubeconfig being reported.
	if hcp.Status.KubeConfig == nil {
		return false, nil
	}

	kubeConfigSecret := manifests.KASServiceKubeconfigSecret(hcp.Namespace)
	if err := cpContext.Client.Get(cpContext, client.ObjectKeyFromObject(kubeConfigSecret), kubeConfigSecret); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to get hosted controlplane kubeconfig secret %q: %w", kubeConfigSecret.Name, err)
	}

	return true, nil
}
