package capiprovider

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	rbacv1 "k8s.io/api/rbac/v1"
)

const (
	ComponentName = "capi-provider"
)

var _ component.ComponentOptions = &CAPIProviderOptions{}

type CAPIProviderOptions struct {
	deploymentSpec      *appsv1.DeploymentSpec
	platformPolicyRules []rbacv1.PolicyRule
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (c *CAPIProviderOptions) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (c *CAPIProviderOptions) MultiZoneSpread() bool {
	return false
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (c *CAPIProviderOptions) NeedsManagementKASAccess() bool {
	return true
}

func NewComponent(deploymentSpec *appsv1.DeploymentSpec, platformPolicyRules []rbacv1.PolicyRule) component.ControlPlaneComponent {
	capi := &CAPIProviderOptions{
		deploymentSpec:      deploymentSpec,
		platformPolicyRules: platformPolicyRules,
	}

	return component.NewDeploymentComponent(ComponentName, capi).
		WithAdaptFunction(capi.adaptDeployment).
		WithPredicate(predicate).
		InjectAvailabilityProberContainer(util.AvailabilityProberOpts{}).
		WithManifestAdapter(
			"role.yaml",
			component.WithAdaptFunction(capi.adaptRole),
		).
		WithManifestAdapter(
			"rolebinding.yaml",
			component.SetHostedClusterAnnotation(),
		).
		WithManifestAdapter(
			"serviceaccount.yaml",
			component.SetHostedClusterAnnotation(),
		).
		Build()
}

func predicate(cpContext component.WorkloadContext) (bool, error) {
	_, disable := cpContext.HCP.Annotations[hyperv1.DisableMachineManagement]
	return !disable, nil
}
