package snapshotcontroller

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	oapiv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/oapi"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	ComponentName = "csi-snapshot-controller-operator"
)

var _ component.ComponentOptions = &snapshotController{}

type snapshotController struct {
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (r *snapshotController) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (r *snapshotController) MultiZoneSpread() bool {
	return false
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (r *snapshotController) NeedsManagementKASAccess() bool {
	return true
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &snapshotController{}).
		WithAdaptFunction(adaptDeployment).
		WithPredicate(isStorageAndCSIManaged).
		WithDependencies(oapiv2.ComponentName).
		InjectAvailabilityProberContainer(util.AvailabilityProberOpts{
			KubeconfigVolumeName: "guest-kubeconfig",
			RequiredAPIs: []schema.GroupVersionKind{
				{Group: "operator.openshift.io", Version: "v1", Kind: "CSISnapshotController"},
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

func checkOperandsRolloutStatus(cpContext component.WorkloadContext) (bool, error) {
	deployment := &appsv1.Deployment{}
	if err := cpContext.Client.Get(cpContext, client.ObjectKey{Namespace: cpContext.HCP.Namespace, Name: "csi-snapshot-controller"}, deployment); err != nil {
		return false, fmt.Errorf("failed to get deployment csi-snapshot-controller: %w", err)
	}

	expectedImage := cpContext.ReleaseImageProvider.GetImage("csi-snapshot-controller")
	for _, container := range deployment.Spec.Template.Spec.Containers {
		if container.Name == "snapshot-controller" {
			if container.Image != expectedImage {
				return false, fmt.Errorf("container %s in deployment %s is not using the expected image %s", "snapshot-controller", "csi-snapshot-controller", expectedImage)
			}
			break
		}
	}

	if !util.IsDeploymentReady(cpContext, deployment) {
		return false, fmt.Errorf("deployment csi-snapshot-controller is not ready")
	}

	return true, nil
}
