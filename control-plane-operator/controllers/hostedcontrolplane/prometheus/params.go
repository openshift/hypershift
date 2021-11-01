package prometheus

import (
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/util"
)

type PrometheusParams struct {
	Image                   string
	AvailabilityProberImage string
	TokenMinterImage        string
	DeploymentConfig        config.DeploymentConfig
	OwnerRef                config.OwnerRef
	TokenAudience           string
	APIServerPort           *int32
}

func NewPrometheusParams(hcp *hyperv1.HostedControlPlane, images map[string]string) *PrometheusParams {
	params := &PrometheusParams{
		Image:                   images["prometheus"],
		AvailabilityProberImage: images[util.AvailabilityProberImageName],
		TokenMinterImage:        images[util.TokenMinterImageName],
		OwnerRef:                config.OwnerRefFrom(hcp),
		TokenAudience:           hcp.Spec.IssuerURL,
		APIServerPort:           hcp.Spec.APIPort,
	}

	params.DeploymentConfig.SetColocation(hcp)
	params.DeploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)
	params.DeploymentConfig.SetControlPlaneIsolation(hcp)
	params.DeploymentConfig.Replicas = 1
	params.DeploymentConfig.Scheduling = config.Scheduling{
		PriorityClass: config.DefaultPriorityClass,
	}
	return params
}
