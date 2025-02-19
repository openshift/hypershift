package ingressoperator

import (
	oapiv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/oapi"
	"github.com/openshift/hypershift/support/azureutil"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"
)

const (
	ComponentName = "ingress-operator"
)

var _ component.ComponentOptions = &ingressOperator{}

type ingressOperator struct {
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (r *ingressOperator) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (r *ingressOperator) MultiZoneSpread() bool {
	return false
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (r *ingressOperator) NeedsManagementKASAccess() bool {
	return false
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &ingressOperator{}).
		WithAdaptFunction(adaptDeployment).
		WithManifestAdapter(
			"podmonitor.yaml",
			component.WithAdaptFunction(adaptPodMonitor),
		).
		WithManifestAdapter(
			"azure-secretprovider.yaml",
			component.WithAdaptFunction(adaptAzureSecretProvider),
			component.WithPredicate(isAroHCP),
		).
		WithDependencies(oapiv2.ComponentName).
		InjectKonnectivityContainer(component.KonnectivityContainerOptions{
			Mode: component.HTTPS,
			HTTPSOptions: component.HTTPSOptions{
				ConnectDirectlyToCloudAPIs: ptr.To(true),
			},
			KubeconfingVolumeName: "admin-kubeconfig",
		}).
		InjectAvailabilityProberContainer(util.AvailabilityProberOpts{
			KubeconfigVolumeName: component.ServiceAccountKubeconfigVolumeName,
			RequiredAPIs: []schema.GroupVersionKind{
				{Group: "route.openshift.io", Version: "v1", Kind: "Route"},
			},
		}).
		InjectServiceAccountKubeConfig(component.ServiceAccountKubeConfigOpts{
			Name:          "ingress-operator",
			Namespace:     "openshift-ingress-operator",
			MountPath:     "/etc/kubernetes",
			ContainerName: ComponentName,
		}).
		Build()
}

func isAroHCP(cpContext component.WorkloadContext) bool {
	return azureutil.IsAroHCP()
}
