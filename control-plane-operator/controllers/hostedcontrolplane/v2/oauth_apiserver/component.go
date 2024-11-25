package oapi

import (
	kasv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/kas"
	oapiv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/oapi"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
)

const (
	ComponentName = "openshift-oauth-apiserver"
)

var _ component.ComponentOptions = &openshiftOAuthAPIServer{}

type openshiftOAuthAPIServer struct {
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (k *openshiftOAuthAPIServer) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (k *openshiftOAuthAPIServer) MultiZoneSpread() bool {
	return true
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (k *openshiftOAuthAPIServer) NeedsManagementKASAccess() bool {
	return false
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &openshiftOAuthAPIServer{}).
		WithAdaptFunction(adaptDeployment).
		WithPredicate(predicate).
		WithManifestAdapter(
			"audit-config.yaml",
			component.WithAdaptFunction(kasv2.AdaptAuditConfig),
		).
		WithManifestAdapter(
			"pdb.yaml",
			component.AdaptPodDisruptionBudget(),
		).
		WithDependencies(oapiv2.ComponentName).
		WatchResource(&corev1.ConfigMap{}, "openshift-oauth-apiserver-audit").
		InjectAvailabilityProberContainer(util.AvailabilityProberOpts{}).
		Build()
}

func predicate(cpContext component.WorkloadContext) (bool, error) {
	return util.HCPOAuthEnabled(cpContext.HCP), nil
}
