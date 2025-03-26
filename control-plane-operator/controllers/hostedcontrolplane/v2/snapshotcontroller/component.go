package snapshotcontroller

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	oapiv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/oapi"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	"k8s.io/apimachinery/pkg/runtime/schema"
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
		Build()
}

func isStorageAndCSIManaged(cpContext component.WorkloadContext) (bool, error) {
	if cpContext.HCP.Spec.Platform.Type == hyperv1.IBMCloudPlatform || cpContext.HCP.Spec.Platform.Type == hyperv1.PowerVSPlatform {
		return false, nil
	}
	return true, nil
}
