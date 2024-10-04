package snapshotcontroller

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
	"k8s.io/utils/ptr"
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
	config.DeploymentConfig
}

func NewParams(
	hcp *hyperv1.HostedControlPlane,
	version string,
	releaseImageProvider imageprovider.ReleaseImageProvider,
	setDefaultSecurityContext bool) *Params {

	params := Params{
		OwnerRef:                        config.OwnerRefFrom(hcp),
		SnapshotControllerOperatorImage: releaseImageProvider.GetImage(snapshotControllerOperatorImageName),
		SnapshotControllerImage:         releaseImageProvider.GetImage(snapshotControllerImageName),
		SnapshotWebhookImage:            releaseImageProvider.GetImage(snapshotWebhookImageName),
		AvailabilityProberImage:         releaseImageProvider.GetImage(util.AvailabilityProberImageName),
		Version:                         version,
	}

	params.DeploymentConfig.SetDefaultSecurityContext = setDefaultSecurityContext
	// Run only one replica of the operator
	params.DeploymentConfig.Scheduling = config.Scheduling{
		PriorityClass: config.DefaultPriorityClass,
	}
	params.DeploymentConfig.AdditionalLabels = map[string]string{
		config.NeedManagementKASAccessLabel: "true",
	}
	params.DeploymentConfig.SetDefaults(hcp, nil, ptr.To(1))
	params.DeploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)

	return &params
}
