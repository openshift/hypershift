package kas

import (
	"fmt"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/sharedingress"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"

	routev1 "github.com/openshift/api/route/v1"
)

// GetHealthcheckEndpoint determines the appropriate endpoint and port for healthcheck based on the route and configuration
func GetHealthcheckEndpointForRoute(externalRoute *routev1.Route, hcp *hyperv1.HostedControlPlane) (endpoint string, port int, err error) {
	if len(externalRoute.Status.Ingress) == 0 {
		return "", 0, fmt.Errorf("APIServer external route %s/%s (host: %s) not admitted: route has no ingress status; last status writer: %s",
			externalRoute.Namespace, externalRoute.Name, externalRoute.Spec.Host,
			externalRoute.Annotations[netutil.RouteStatusWriterAnnotation])
	}
	if externalRoute.Status.Ingress[0].RouterCanonicalHostname == "" {
		return "", 0, fmt.Errorf("APIServer external route %s/%s (host: %s) not admitted: %s; last status writer: %s",
			externalRoute.Namespace, externalRoute.Name, externalRoute.Spec.Host,
			routeIngressDiagnostic(externalRoute.Status.Ingress[0]),
			externalRoute.Annotations[netutil.RouteStatusWriterAnnotation])
	}

	endpoint = externalRoute.Status.Ingress[0].RouterCanonicalHostname
	port = 443

	if util.UseSharedIngressHCP(hcp) {
		endpoint = externalRoute.Spec.Host
		port = sharedingress.ExternalDNSLBPort
	}

	if util.UseSharedIngressHCP(hcp) &&
		hcp.Spec.Networking.APIServer != nil && len(hcp.Spec.Networking.APIServer.AllowedCIDRBlocks) > 0 {
		// When there's AllowedCIDRBlocks input, we have no guarantees the healthcheck can roundtrip through the haproxy load balancer.
		// Hence we use KubeAPIServerService as a best effort.
		endpoint = manifests.KubeAPIServerService("").Name
		port = config.KASSVCPort
	}

	return endpoint, port, nil
}

// routeIngressDiagnostic produces a human-readable summary of a RouteIngress
// entry that has been populated but lacks a RouterCanonicalHostname.
func routeIngressDiagnostic(ingress routev1.RouteIngress) string {
	parts := []string{
		fmt.Sprintf("router %q has not set a canonical hostname", ingress.RouterName),
	}
	for _, cond := range ingress.Conditions {
		detail := fmt.Sprintf("%s=%s", cond.Type, cond.Status)
		if cond.Reason != "" {
			detail += fmt.Sprintf(" reason=%s", cond.Reason)
		}
		if cond.Message != "" {
			detail += fmt.Sprintf(" message=%q", cond.Message)
		}
		parts = append(parts, detail)
	}
	return strings.Join(parts, "; ")
}
