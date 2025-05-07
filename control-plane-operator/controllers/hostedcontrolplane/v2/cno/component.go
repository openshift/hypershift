package cno

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	oapiv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/oapi"
	"github.com/openshift/hypershift/support/azureutil"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
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

func SetRestartAnnotationAndPatch(ctx context.Context, crclient client.Client, dep *appsv1.Deployment, restartAnnotation string) error {
	if err := crclient.Get(ctx, client.ObjectKeyFromObject(dep), dep); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed retrieve deployment: %w", err)
	}

	patch := dep.DeepCopy()
	podMeta := patch.Spec.Template.ObjectMeta
	if podMeta.Annotations == nil {
		podMeta.Annotations = map[string]string{}
	}
	podMeta.Annotations[hyperv1.RestartDateAnnotation] = restartAnnotation

	if err := crclient.Patch(ctx, patch, client.MergeFrom(dep)); err != nil {
		return fmt.Errorf("failed to set restart annotation: %w", err)
	}

	return nil
}
