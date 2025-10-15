package kas

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/sharedingress"
	"github.com/openshift/hypershift/support/config"

	routev1 "github.com/openshift/api/route/v1"
)

// GetHealthcheckEndpoint determines the appropriate endpoint and port for healthcheck based on the route and configuration
func GetHealthcheckEndpointForRoute(externalRoute *routev1.Route, hcp *hyperv1.HostedControlPlane) (endpoint string, port int, err error) {
	if len(externalRoute.Status.Ingress) == 0 || externalRoute.Status.Ingress[0].RouterCanonicalHostname == "" {
		return "", 0, fmt.Errorf("APIServer external route not admitted")
	}

	endpoint = externalRoute.Status.Ingress[0].RouterCanonicalHostname
	port = 443

	if sharedingress.UseSharedIngress() {
		endpoint = externalRoute.Spec.Host
		port = sharedingress.ExternalDNSLBPort
	}

	if sharedingress.UseSharedIngress() &&
		hcp.Spec.Networking.APIServer != nil && len(hcp.Spec.Networking.APIServer.AllowedCIDRBlocks) > 0 {
		// When there's AllowedCIDRBlocks input, we have no guarantees the healthcheck can roundtrip through the haproxy load balancer.
		// Hence we use KubeAPIServerService as a best effort.
		endpoint = manifests.KubeAPIServerService("").Name
		port = config.KASSVCPort
	}

	return endpoint, port, nil
}
