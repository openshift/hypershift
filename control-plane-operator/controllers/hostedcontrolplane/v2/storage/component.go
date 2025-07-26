package storage

import (
	"errors"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	oapiv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/oapi"
	"github.com/openshift/hypershift/support/azureutil"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	ComponentName = "cluster-storage-operator"
)

var _ component.ComponentOptions = &clusterStorageOperator{}

type clusterStorageOperator struct {
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (r *clusterStorageOperator) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (r *clusterStorageOperator) MultiZoneSpread() bool {
	return false
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (r *clusterStorageOperator) NeedsManagementKASAccess() bool {
	return true
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &clusterStorageOperator{}).
		WithAdaptFunction(adaptDeployment).
		WithPredicate(isStorageAndCSIManaged).
		WithManifestAdapter(
			"azure-disk-csi-config.yaml",
			component.WithAdaptFunction(adaptAzureCSIDiskSecret),
			component.WithPredicate(isAroHCP),
		).
		WithManifestAdapter(
			"azure-disk-csi-secretprovider.yaml",
			component.WithAdaptFunction(adaptAzureCSIDiskSecretProvider),
			component.WithPredicate(isAroHCP),
		).
		WithManifestAdapter(
			"azure-file-csi-config.yaml",
			component.WithAdaptFunction(adaptAzureCSIFileSecret),
			component.WithPredicate(isAroHCP),
		).
		WithManifestAdapter(
			"azure-file-csi-secretprovider.yaml",
			component.WithAdaptFunction(adaptAzureCSIFileSecretProvider),
			component.WithPredicate(isAroHCP),
		).
		WithDependencies(oapiv2.ComponentName).
		InjectAvailabilityProberContainer(util.AvailabilityProberOpts{
			KubeconfigVolumeName: "guest-kubeconfig",
			RequiredAPIs: []schema.GroupVersionKind{
				{Group: "operator.openshift.io", Version: "v1", Kind: "Storage"},
			},
		}).
		WithCustomOperandsRolloutCheckFunc(checkOperandsRolloutStatus).
		Build()
}

func isStorageAndCSIManaged(cpContext component.WorkloadContext) (bool, error) {
	if cpContext.HCP.Spec.Platform.Type == hyperv1.IBMCloudPlatform || cpContext.HCP.Spec.Platform.Type == hyperv1.PowerVSPlatform {
		return false, nil
	}
	return true, nil
}

func isAroHCP(cpContext component.WorkloadContext) bool {
	return azureutil.IsAroHCP()
}

type operand struct {
	DeploymentName  string
	ContainerName   string
	ReleaseImageKey string
}

func checkOperandsRolloutStatus(cpContext component.WorkloadContext) (bool, error) {
	var operandsDeploymentsList []operand
	switch cpContext.HCP.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		operandsDeploymentsList = []operand{
			{
				DeploymentName:  "aws-ebs-csi-driver-operator",
				ContainerName:   "aws-ebs-csi-driver-operator",
				ReleaseImageKey: "aws-ebs-csi-driver-operator",
			},
			{
				DeploymentName:  "aws-ebs-csi-driver-controller",
				ContainerName:   "csi-driver",
				ReleaseImageKey: "aws-ebs-csi-driver",
			},
		}
	case hyperv1.AzurePlatform:
		operandsDeploymentsList = []operand{
			{
				DeploymentName:  "azure-disk-csi-driver-operator",
				ContainerName:   "azure-disk-csi-driver-operator",
				ReleaseImageKey: "azure-disk-csi-driver-operator",
			},
			{
				DeploymentName:  "azure-disk-csi-driver-controller",
				ContainerName:   "csi-driver",
				ReleaseImageKey: "azure-disk-csi-driver",
			},
			{
				DeploymentName:  "azure-file-csi-driver-operator",
				ContainerName:   "azure-file-csi-driver-operator",
				ReleaseImageKey: "azure-file-csi-driver-operator",
			},
			{
				DeploymentName:  "azure-file-csi-driver-controller",
				ContainerName:   "csi-driver",
				ReleaseImageKey: "azure-file-csi-driver",
			},
		}
	default:
		return true, nil
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
