package olm

import (
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
	"k8s.io/utils/pointer"
)

var packageServerLabels = map[string]string{
	"app":                         "packageserver",
	hyperv1.ControlPlaneComponent: "packageserver",
}

type OperatorLifecycleManagerParams struct {
	CLIImage                string
	OLMImage                string
	ProxyImage              string
	OperatorRegistryImage   string
	ReleaseVersion          string
	DeploymentConfig        config.DeploymentConfig
	PackageServerConfig     config.DeploymentConfig
	AvailabilityProberImage string
	NoProxy                 []string
	config.OwnerRef
}

func NewOperatorLifecycleManagerParams(hcp *hyperv1.HostedControlPlane, images map[string]string, releaseVersion string, setDefaultSecurityContext bool) *OperatorLifecycleManagerParams {
	params := &OperatorLifecycleManagerParams{
		CLIImage:                images["cli"],
		OLMImage:                images["operator-lifecycle-manager"],
		ProxyImage:              images["socks5-proxy"],
		OperatorRegistryImage:   images["operator-registry"],
		ReleaseVersion:          releaseVersion,
		AvailabilityProberImage: images[util.AvailabilityProberImageName],
		NoProxy:                 []string{"kube-apiserver"},
		OwnerRef:                config.OwnerRefFrom(hcp),
	}
	params.DeploymentConfig = config.DeploymentConfig{
		Scheduling: config.Scheduling{
			PriorityClass: config.DefaultPriorityClass,
		},
	}
	params.DeploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)
	params.DeploymentConfig.SetDefaults(hcp, nil, pointer.Int(1))
	params.DeploymentConfig.SetDefaultSecurityContext = setDefaultSecurityContext

	params.PackageServerConfig = config.DeploymentConfig{
		Scheduling: config.Scheduling{
			PriorityClass: config.APICriticalPriorityClass,
		},
	}
	params.PackageServerConfig.SetDefaults(hcp, packageServerLabels, nil)
	params.PackageServerConfig.SetRestartAnnotation(hcp.ObjectMeta)
	params.PackageServerConfig.SetDefaultSecurityContext = setDefaultSecurityContext

	if hcp.Spec.OLMCatalogPlacement == "management" {
		params.NoProxy = append(params.NoProxy, "certified-operators", "community-operators", "redhat-operators", "redhat-marketplace")
	}

	return params
}
