package ignitionserver

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

func (ign *ignitionServer) adaptRoute(cpContext component.WorkloadContext, route *routev1.Route) error {
	serviceName := "ignition-server-proxy"
	// For IBM Cloud, we don't deploy the ignition server proxy.
	// Instead, the ignition server service is directly exposed.
	if cpContext.HCP.Spec.Platform.Type == hyperv1.IBMCloudPlatform {
		serviceName = "ignition-server"
	}

	hcp := cpContext.HCP
	if util.IsPrivateHCP(hcp) {
		return util.ReconcileInternalRoute(route, hcp.Name, serviceName)
	}

	strategy := util.ServicePublishingStrategyByTypeForHCP(hcp, hyperv1.Ignition)
	if strategy == nil {
		return fmt.Errorf("ignition service strategy not specified")
	}

	hostname := ""
	if strategy.Route != nil {
		hostname = strategy.Route.Hostname
	}
	return util.ReconcileExternalRoute(route, hostname, ign.defaultIngressDomain, serviceName, util.UseDedicatedDNSForIgnition(hcp))
}
