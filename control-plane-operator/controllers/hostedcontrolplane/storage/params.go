package storage

import (
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
	utilpointer "k8s.io/utils/pointer"
)

const (
	storageOperatorImageName = "cluster-storage-operator"
)

type Params struct {
	OwnerRef             config.OwnerRef
	StorageOperatorImage string
	ImageReplacer        *environmentReplacer

	AvailabilityProberImage string
	APIPort                 *int32
	config.DeploymentConfig
}

func NewParams(
	hcp *hyperv1.HostedControlPlane,
	version string,
	releaseImageProvider *imageprovider.ReleaseImageProvider,
	userReleaseImageProvider *imageprovider.ReleaseImageProvider,
	setDefaultSecurityContext bool) *Params {

	ir := newEnvironmentReplacer()
	ir.setVersions(version)
	ir.setOperatorImageReferences(releaseImageProvider.ComponentImages(), userReleaseImageProvider.ComponentImages())

	params := Params{
		OwnerRef:                config.OwnerRefFrom(hcp),
		StorageOperatorImage:    releaseImageProvider.GetImage(storageOperatorImageName),
		AvailabilityProberImage: releaseImageProvider.GetImage(util.AvailabilityProberImageName),
		ImageReplacer:           ir,
		APIPort:                 util.APIPort(hcp),
	}
	params.DeploymentConfig = config.DeploymentConfig{
		AdditionalLabels: map[string]string{
			config.NeedManagementKASAccessLabel: "true",
		},
	}
	params.DeploymentConfig.SetDefaultSecurityContext = setDefaultSecurityContext
	// Run only one replica of the operator
	params.DeploymentConfig.Scheduling = config.Scheduling{
		PriorityClass: config.DefaultPriorityClass,
	}
	params.DeploymentConfig.SetDefaults(hcp, nil, utilpointer.Int(1))
	params.DeploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)

	return &params
}
