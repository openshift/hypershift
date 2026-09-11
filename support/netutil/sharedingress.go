package netutil

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

const ExternalDNSLBPort = 443

// KASRouteHostname returns the configured route hostname for the KAS APIServer
// service publishing strategy, or an empty string if no route is configured.
func KASRouteHostname(hcp *hyperv1.HostedControlPlane) string {
	kasPublishStrategy := ServicePublishingStrategyByTypeForHCP(hcp, hyperv1.APIServer)
	if kasPublishStrategy == nil || kasPublishStrategy.Route == nil {
		return ""
	}
	return kasPublishStrategy.Route.Hostname
}
