package metricsproxy

import (
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	routev1 "github.com/openshift/api/route/v1"
)

func (mp *metricsProxy) adaptRoute(cpContext component.WorkloadContext, route *routev1.Route) error {
	hcp := cpContext.HCP
	serviceName := ComponentName

	if util.IsPrivateHCP(hcp) {
		return util.ReconcileInternalRoute(route, hcp.Name, serviceName)
	}

	return util.ReconcileExternalRoute(route, "", mp.defaultIngressDomain, serviceName, false)
}
