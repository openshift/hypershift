package clusterpolicy

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	oapiv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/oapi"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/utils/ptr"
)

const (
	ComponentName = "cluster-policy-controller"
)

var _ component.ComponentOptions = &clusterPolicyController{}

type clusterPolicyController struct {
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (r *clusterPolicyController) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (r *clusterPolicyController) MultiZoneSpread() bool {
	return true
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (r *clusterPolicyController) NeedsManagementKASAccess() bool {
	return false
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &clusterPolicyController{}).
		WithAdaptFunction(adaptDeployment).
		WithManifestAdapter(
			"config.yaml",
			component.WithAdaptFunction(adaptConfigMap),
		).
		WithDependencies(oapiv2.ComponentName).
		InjectAvailabilityProberContainer(util.AvailabilityProberOpts{}).
		Build()
}

func adaptDeployment(cpContext component.ControlPlaneContext, deployment *appsv1.Deployment) error {
	deployment.Spec.Replicas = ptr.To[int32](2)
	if cpContext.HCP.Spec.ControllerAvailabilityPolicy == hyperv1.SingleReplica {
		deployment.Spec.Replicas = ptr.To[int32](1)
	}

	return nil
}
