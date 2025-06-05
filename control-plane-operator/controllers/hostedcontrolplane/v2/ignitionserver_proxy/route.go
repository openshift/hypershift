package ignitionserverproxy

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	routev1 "github.com/openshift/api/route/v1"
)

func routePredicate(cpContext component.WorkloadContext) bool {
	strategy := util.ServicePublishingStrategyByTypeForHCP(cpContext.HCP, hyperv1.Ignition)
	if strategy == nil {
		return false
	}
	return strategy.Type == hyperv1.Route
}

func (ign *ignitionServerProxy) adaptRoute(cpContext component.WorkloadContext, route *routev1.Route) error {
	hcp := cpContext.HCP
	if util.IsPrivateHCP(hcp) {
		return util.ReconcileInternalRoute(route, hcp.Name, ComponentName)
	}

	strategy := util.ServicePublishingStrategyByTypeForHCP(hcp, hyperv1.Ignition)
	if strategy == nil {
		return fmt.Errorf("ignition service strategy not specified")
	}

	hostname := ""
	if strategy.Route != nil {
		hostname = strategy.Route.Hostname
	}
	return util.ReconcileExternalRoute(route, hostname, ign.defaultIngressDomain, ComponentName, util.UseDedicatedDNSForIgnition(hcp))
}
