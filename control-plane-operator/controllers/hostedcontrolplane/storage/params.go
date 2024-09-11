package storage

import (
	"context"
	"fmt"
	"os"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	"github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
	utilpointer "k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	storageOperatorImageName = "cluster-storage-operator"
)

type Params struct {
	OwnerRef                 config.OwnerRef
	StorageOperatorImage     string
	AzureDiskManagedIdentity string
	AzureFileManagedIdentity string
	ClientIDSecret           string
	TenantID                 string
	ImageReplacer            *environmentReplacer

	AvailabilityProberImage string
	config.DeploymentConfig
}

func NewParams(
	ctx context.Context,
	c client.Client,
	hcp *hyperv1.HostedControlPlane,
	version string,
	releaseImageProvider *imageprovider.ReleaseImageProvider,
	userReleaseImageProvider *imageprovider.ReleaseImageProvider,
	setDefaultSecurityContext bool) (*Params, error) {

	ir := newEnvironmentReplacer()
	ir.setVersions(version)
	ir.setOperatorImageReferences(releaseImageProvider.ComponentImages(), userReleaseImageProvider.ComponentImages())

	params := Params{
		OwnerRef:                config.OwnerRefFrom(hcp),
		StorageOperatorImage:    releaseImageProvider.GetImage(storageOperatorImageName),
		AvailabilityProberImage: releaseImageProvider.GetImage(util.AvailabilityProberImageName),
		ImageReplacer:           ir,
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

	if os.Getenv("MANAGED_SERVICE") == hyperv1.AroHCP {
		azureCredentials, err := azureutil.GetAzureCredentialsFromSecret(ctx, c, hcp.Namespace, hcp.Spec.Platform.Azure.Credentials.Name)
		if err != nil {
			return nil, fmt.Errorf("failed to get Azure credentials: %w", err)
		}

		params.AzureDiskManagedIdentity = string(hcp.Spec.Platform.Azure.ManagedIdentities.ControlPlaneManagedIdentities.AzureDiskManagedIdentityClientID)
		params.AzureFileManagedIdentity = string(hcp.Spec.Platform.Azure.ManagedIdentities.ControlPlaneManagedIdentities.AzureFileManagedIdentityClientID)
		params.ClientIDSecret = string(azureCredentials.Data["AZURE_CLIENT_SECRET"])
		params.TenantID = string(azureCredentials.Data["AZURE_TENANT_ID"])
	}

	return &params, nil
}
