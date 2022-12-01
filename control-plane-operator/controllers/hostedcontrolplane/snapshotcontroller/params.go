package snapshotcontroller

import (
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
	utilpointer "k8s.io/utils/pointer"
)

const (
	snapshotControllerOperatorImageName = "cluster-csi-snapshot-controller-operator"
	snapshotControllerImageName         = "csi-snapshot-controller"
	snapshotWebhookImageName            = "csi-snapshot-validation-webhook"
)

type Params struct {
	OwnerRef                        config.OwnerRef
	SnapshotControllerOperatorImage string
	SnapshotControllerImage         string
	SnapshotWebhookImage            string
	AvailabilityProberImage         string
	Version                         string
	APIPort                         *int32
	config.DeploymentConfig
}

func NewParams(
	hcp *hyperv1.HostedControlPlane,
	version string,
	images map[string]string,
	setDefaultSecurityContext bool) *Params {

	params := Params{
		OwnerRef:                        config.OwnerRefFrom(hcp),
		SnapshotControllerOperatorImage: images[snapshotControllerOperatorImageName],
		SnapshotControllerImage:         images[snapshotControllerImageName],
		SnapshotWebhookImage:            images[snapshotWebhookImageName],
		AvailabilityProberImage:         images[util.AvailabilityProberImageName],
		Version:                         version,
		APIPort:                         util.APIPort(hcp),
	}

	params.DeploymentConfig.SetDefaultSecurityContext = setDefaultSecurityContext
	// Run only one replica of the operator
	params.DeploymentConfig.Scheduling = config.Scheduling{
		PriorityClass: config.DefaultPriorityClass,
	}
	params.DeploymentConfig.SetDefaults(hcp, nil, utilpointer.IntPtr(1))
	params.DeploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)

	return &params
}
