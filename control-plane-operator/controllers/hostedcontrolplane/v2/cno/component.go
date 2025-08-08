package cno

import (
	"context"
	"errors"
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
		WithCustomOperandsRolloutCheckFunc(checkOperandsRolloutStatus).
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

type operand struct {
	DeploymentName  string
	ContainerName   string
	ReleaseImageKey string
}

func checkOperandsRolloutStatus(cpContext component.WorkloadContext) (bool, error) {
	operandsDeploymentsList := []operand{
		{
			DeploymentName:  "ovnkube-control-plane",
			ContainerName:   "ovnkube-control-plane",
			ReleaseImageKey: "ovn-kubernetes",
		},
		{
			DeploymentName:  "network-node-identity",
			ContainerName:   "approver",
			ReleaseImageKey: "ovn-kubernetes",
		},
		{
			DeploymentName:  "cloud-network-config-controller",
			ContainerName:   "controller",
			ReleaseImageKey: "cloud-network-config-controller",
		},
	}
	if !util.IsDisableMultiNetwork(cpContext.HCP) {
		operandsDeploymentsList = append(operandsDeploymentsList, operand{
			DeploymentName:  "multus-admission-controller",
			ContainerName:   "multus-admission-controller",
			ReleaseImageKey: "multus-admission-controller",
		})
	}

	var errs []error
	for _, operand := range operandsDeploymentsList {
		deployment := &appsv1.Deployment{}
		if err := cpContext.Client.Get(cpContext, client.ObjectKey{Namespace: cpContext.HCP.Namespace, Name: operand.DeploymentName}, deployment); err != nil {
			errs = append(errs, fmt.Errorf("failed to get deployment %s: %w", operand.DeploymentName, err))
			continue
		}

		expectedImage := cpContext.ReleaseImageProvider.GetImage(operand.ReleaseImageKey)
		for _, container := range deployment.Spec.Template.Spec.Containers {
			if container.Name == operand.ContainerName {
				if container.Image != expectedImage {
					errs = append(errs, fmt.Errorf("container %s in deployment %s is not using the expected image %s", operand.ContainerName, operand.DeploymentName, expectedImage))
				}
				break
			}
		}

		if !util.IsDeploymentReady(cpContext, deployment) {
			errs = append(errs, fmt.Errorf("deployment %s is not ready", operand.DeploymentName))
		}
	}

	if len(errs) > 0 {
		return false, errors.Join(errs...)
	}

	return true, nil
}
