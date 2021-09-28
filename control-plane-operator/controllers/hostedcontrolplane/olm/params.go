package olm

import (
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
)

var packageServerLabels = map[string]string{"app": "packageserver"}

type OperatorLifecycleManagerParams struct {
	CLIImage              string
	OLMImage              string
	OperatorRegistryImage string
	ReleaseVersion        string
	DeploymentConfig      config.DeploymentConfig
	PackageServerConfig   config.DeploymentConfig
	config.OwnerRef
}

func NewOperatorLifecycleManagerParams(hcp *hyperv1.HostedControlPlane, images map[string]string, releaseVersion string) *OperatorLifecycleManagerParams {
	params := &OperatorLifecycleManagerParams{
		CLIImage:              images["cli"],
		OLMImage:              images["operator-lifecycle-manager"],
		OperatorRegistryImage: images["operator-registry"],
		ReleaseVersion:        releaseVersion,
		OwnerRef:              config.OwnerRefFrom(hcp),
	}
	params.DeploymentConfig = config.DeploymentConfig{
		Replicas: 1,
		Scheduling: config.Scheduling{
			PriorityClass: config.DefaultPriorityClass,
		},
	}
	params.DeploymentConfig.SetColocation(hcp)
	params.DeploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)
	params.DeploymentConfig.SetControlPlaneIsolation(hcp)

	params.PackageServerConfig = config.DeploymentConfig{
		Scheduling: config.Scheduling{
			PriorityClass: config.APICriticalPriorityClass,
		},
	}
	params.PackageServerConfig.SetColocation(hcp)
	params.PackageServerConfig.SetRestartAnnotation(hcp.ObjectMeta)
	params.PackageServerConfig.SetControlPlaneIsolation(hcp)
	switch hcp.Spec.ControllerAvailabilityPolicy {
	case hyperv1.HighlyAvailable:
		params.PackageServerConfig.Replicas = 3
		params.PackageServerConfig.SetMultizoneSpread(packageServerLabels)
	default:
		params.PackageServerConfig.Replicas = 1
	}

	return params
}
