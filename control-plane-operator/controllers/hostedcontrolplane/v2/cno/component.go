package cno

import (
	oapiv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/oapi"
	"github.com/openshift/hypershift/support/azureutil"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"
)

const (
	ComponentName = "cluster-network-operator"
)

var _ component.ComponentOptions = &clusterNetworkOperator{}

type clusterNetworkOperator struct {
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (r *clusterNetworkOperator) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (r *clusterNetworkOperator) MultiZoneSpread() bool {
	return false
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (r *clusterNetworkOperator) NeedsManagementKASAccess() bool {
	return true
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &clusterNetworkOperator{}).
		WithAdaptFunction(adaptDeployment).
		WithManifestAdapter(
			"role.yaml",
			component.WithAdaptFunction(adaptRole),
		).
		WithManifestAdapter(
			"azure-secretprovider.yaml",
			component.WithAdaptFunction(adaptAzureSecretProvider),
			component.WithPredicate(isAroHCP),
		).
		WithDependencies(oapiv2.ComponentName).
		InjectKonnectivityContainer(component.KonnectivityContainerOptions{
			Mode: component.Socks5, // CNO uses konnectivity-proxy to perform proxy readiness checks through the hosted cluster's network
			Socks5Options: component.Socks5Options{
				DisableResolver: ptr.To(true),
			},
			KubeconfingVolumeName: "hosted-etc-kube",
		}).
		InjectAvailabilityProberContainer(util.AvailabilityProberOpts{
			KubeconfigVolumeName: "hosted-etc-kube",
			RequiredAPIs: []schema.GroupVersionKind{
				{Group: "operator.openshift.io", Version: "v1", Kind: "Network"},
				{Group: "network.operator.openshift.io", Version: "v1", Kind: "EgressRouter"},
				{Group: "network.operator.openshift.io", Version: "v1", Kind: "OperatorPKI"},
			},
			WaitForClusterRolebinding:     ComponentName,
			WaitForInfrastructureResource: true,
		}).
		Build()
}

func isAroHCP(cpContext component.WorkloadContext) bool {
	return azureutil.IsAroHCP()
}
