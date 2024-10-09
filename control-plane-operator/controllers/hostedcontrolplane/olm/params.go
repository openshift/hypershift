package olm

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
	"k8s.io/utils/ptr"
)

var packageServerLabels = map[string]string{
	"app":                         "packageserver",
	hyperv1.ControlPlaneComponent: "packageserver",
}

type OperatorLifecycleManagerParams struct {
	CLIImage                                 string
	OLMImage                                 string
	ProxyImage                               string
	OperatorRegistryImage                    string
	CertifiedOperatorsCatalogImageOverride   string
	CommunityOperatorsCatalogImageOverride   string
	RedHatMarketplaceCatalogImageOverride    string
	RedHatOperatorsCatalogImageOverride      string
	OLMCatalogsISRegistryOverridesAnnotation string
	ReleaseVersion                           string
	DeploymentConfig                         config.DeploymentConfig
	PackageServerConfig                      config.DeploymentConfig
	AvailabilityProberImage                  string
	NoProxy                                  []string
	config.OwnerRef
}

func NewOperatorLifecycleManagerParams(hcp *hyperv1.HostedControlPlane, releaseImageProvider imageprovider.ReleaseImageProvider, releaseVersion string, setDefaultSecurityContext bool) *OperatorLifecycleManagerParams {
	params := &OperatorLifecycleManagerParams{
		CLIImage:                releaseImageProvider.GetImage("cli"),
		OLMImage:                releaseImageProvider.GetImage("operator-lifecycle-manager"),
		ProxyImage:              releaseImageProvider.GetImage("socks5-proxy"),
		OperatorRegistryImage:   releaseImageProvider.GetImage("operator-registry"),
		ReleaseVersion:          releaseVersion,
		AvailabilityProberImage: releaseImageProvider.GetImage(util.AvailabilityProberImageName),
		NoProxy:                 []string{"kube-apiserver"},
		OwnerRef:                config.OwnerRefFrom(hcp),
	}
	params.DeploymentConfig = config.DeploymentConfig{
		Scheduling: config.Scheduling{
			PriorityClass: config.DefaultPriorityClass,
		},
	}
	if hcp.Annotations[hyperv1.ControlPlanePriorityClass] != "" {
		params.DeploymentConfig.Scheduling.PriorityClass = hcp.Annotations[hyperv1.ControlPlanePriorityClass]
	}
	params.DeploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)
	params.DeploymentConfig.SetDefaults(hcp, nil, ptr.To(1))
	params.DeploymentConfig.SetDefaultSecurityContext = setDefaultSecurityContext

	params.PackageServerConfig = config.DeploymentConfig{
		Scheduling: config.Scheduling{
			PriorityClass: config.APICriticalPriorityClass,
		},
	}
	if hcp.Annotations[hyperv1.APICriticalPriorityClass] != "" {
		params.PackageServerConfig.Scheduling.PriorityClass = hcp.Annotations[hyperv1.APICriticalPriorityClass]
	}
	params.PackageServerConfig.SetDefaults(hcp, packageServerLabels, nil)
	params.PackageServerConfig.SetRestartAnnotation(hcp.ObjectMeta)
	params.PackageServerConfig.SetDefaultSecurityContext = setDefaultSecurityContext

	if hcp.Spec.Platform.Type == hyperv1.IBMCloudPlatform && hcp.Spec.ControllerAvailabilityPolicy == hyperv1.HighlyAvailable {
		params.PackageServerConfig.Replicas = 2
	}

	if hcp.Spec.OLMCatalogPlacement == "management" {
		params.NoProxy = append(params.NoProxy, "certified-operators", "community-operators", "redhat-operators", "redhat-marketplace")
	}

	params.CertifiedOperatorsCatalogImageOverride = hcp.Annotations[hyperv1.CertifiedOperatorsCatalogImageAnnotation]
	params.CommunityOperatorsCatalogImageOverride = hcp.Annotations[hyperv1.CommunityOperatorsCatalogImageAnnotation]
	params.RedHatMarketplaceCatalogImageOverride = hcp.Annotations[hyperv1.RedHatMarketplaceCatalogImageAnnotation]
	params.RedHatOperatorsCatalogImageOverride = hcp.Annotations[hyperv1.RedHatOperatorsCatalogImageAnnotation]

	params.OLMCatalogsISRegistryOverridesAnnotation = hcp.Annotations[hyperv1.OLMCatalogsISRegistryOverridesAnnotation]

	return params
}
