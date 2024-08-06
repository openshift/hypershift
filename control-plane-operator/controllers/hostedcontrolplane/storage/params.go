package storage

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
	utilpointer "k8s.io/utils/pointer"
)

const (
	storageOperatorImageName = "cluster-storage-operator"
)

type Params struct {
	StorageMSIClientIdExists bool
	StorageOperatorImage     string
	AvailabilityProberImage  string
	OwnerRef                 config.OwnerRef
	ImageReplacer            *environmentReplacer
	platform                 hyperv1.PlatformType

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

	storageMSIClientIdExists := hcp.Spec.Platform.Type == hyperv1.AzurePlatform && hcp.Spec.Platform.Azure.MSIClientIDs != nil && len(hcp.Spec.Platform.Azure.MSIClientIDs.StorageMSIClientID) > 0
	params := Params{
		OwnerRef:                 config.OwnerRefFrom(hcp),
		StorageOperatorImage:     releaseImageProvider.GetImage(storageOperatorImageName),
		AvailabilityProberImage:  releaseImageProvider.GetImage(util.AvailabilityProberImageName),
		ImageReplacer:            ir,
		StorageMSIClientIdExists: storageMSIClientIdExists,
		platform:                 hcp.Spec.Platform.Type,
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
