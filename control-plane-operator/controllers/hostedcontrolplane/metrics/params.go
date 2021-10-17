package metrics

import (
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
)

type ROKSMetricParams struct {
	Image            string
	OwnerRef         config.OwnerRef
	DeploymentConfig config.DeploymentConfig
}

func NewROKSMetricsParams(hcp *hyperv1.HostedControlPlane, images map[string]string) *ROKSMetricParams {
	p := &ROKSMetricParams{
		Image:    images["roks-metrics"],
		OwnerRef: config.OwnerRefFrom(hcp),
	}
	p.DeploymentConfig.Scheduling.PriorityClass = config.DefaultPriorityClass
	p.DeploymentConfig.SetColocation(hcp)
	p.DeploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)
	p.DeploymentConfig.SetControlPlaneIsolation(hcp)
	p.DeploymentConfig.Replicas = 1
	return p
}
