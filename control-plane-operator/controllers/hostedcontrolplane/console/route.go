// Package console reconciles the exposure Routes for the Phase 1
// control-plane-side OpenShift console and CLI-downloads server.
//
// These are GCP-only, and (for now) ungated by the console capability — the
// spike runs the console regardless of capability; a capability gate is a
// Phase 2 concern. CPO owns the Routes (host, TLS, visibility label) mirroring
// the KAS public/private route model:
//   - public route: no visibility label -> external-dns publishes to the
//     public router LB. Present under Public/PublicAndPrivate.
//   - private route: labeled route-visibility=private so external-dns ignores
//     it, leaving the console/downloads ExternalName service to own the DNS
//     record (-> PSC endpoint IP). Present under Private.
//
// The console/downloads user-facing hosts are derived from the APIServer host
// by swapping the first DNS label (api.<domain> -> console.<domain> /
// downloads.<domain>), so they live in the same zone as api/oauth with no
// separate ingress-domain plumbing.
package console

import (
	"fmt"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/netutil"

	routev1 "github.com/openshift/api/route/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// ConsoleServiceName / DownloadsServiceName are the in-namespace Services the
// Routes target. They match the hand-applied kustomize Services.
const (
	ConsoleServiceName   = "console"
	DownloadsServiceName = "downloads"
	// consoleBackendPort is the port both the console and downloads Services
	// expose (the download-server runs a TLS-terminating sidecar on 8443, same
	// as the console pod), and the port the HCP router dials.
	consoleBackendPort = 8443
)

// HostForService derives the user-facing host for a console-family service from
// the APIServer host by replacing the first DNS label. e.g.
// api.foo.example.com + "console" -> console.foo.example.com.
func HostForService(apiServerHost, label string) (string, error) {
	if apiServerHost == "" {
		return "", fmt.Errorf("APIServer host is empty; cannot derive %s host", label)
	}
	_, domain, found := strings.Cut(apiServerHost, ".")
	if !found || domain == "" {
		return "", fmt.Errorf("APIServer host %q has no domain part; cannot derive %s host", apiServerHost, label)
	}
	return fmt.Sprintf("%s.%s", label, domain), nil
}

// APIServerHost returns the APIServer Route hostname from the HCP's service
// publishing strategy, or "" if not configured as a Route with a hostname.
func APIServerHost(hcp *hyperv1.HostedControlPlane) string {
	s := netutil.ServicePublishingStrategyByTypeForHCP(hcp, hyperv1.APIServer)
	if s != nil && s.Type == hyperv1.Route && s.Route != nil {
		return s.Route.Hostname
	}
	return ""
}

func reconcilePassthroughRoute(route *routev1.Route, owner config.OwnerRef, host, serviceName string) {
	owner.ApplyTo(route)
	netutil.AddHCPRouteLabel(route)
	route.Spec.Host = host
	route.Spec.To = routev1.RouteTargetReference{
		Kind:   "Service",
		Name:   serviceName,
		Weight: ptrInt32(100),
	}
	route.Spec.Port = &routev1.RoutePort{TargetPort: intOrString(consoleBackendPort)}
	route.Spec.TLS = &routev1.TLSConfig{
		Termination:                   routev1.TLSTerminationPassthrough,
		InsecureEdgeTerminationPolicy: routev1.InsecureEdgeTerminationPolicyNone,
	}
}

// ReconcileExternalPublicRoute configures the public console/downloads route.
func ReconcileExternalPublicRoute(route *routev1.Route, owner config.OwnerRef, host, serviceName string) error {
	if host == "" {
		return fmt.Errorf("host is required for route %s", route.Name)
	}
	reconcilePassthroughRoute(route, owner, host, serviceName)
	// Public: external-dns registers the host in-zone; no explicit annotation.
	delete(route.Annotations, hyperv1.ExternalDNSHostnameAnnotation)
	return nil
}

// ReconcileExternalPrivateRoute configures the private console/downloads route:
// same host, but labeled route-visibility=private so external-dns ignores it
// (the ExternalName service owns the record under Private).
func ReconcileExternalPrivateRoute(route *routev1.Route, owner config.OwnerRef, host, serviceName string) error {
	if host == "" {
		return fmt.Errorf("host is required for route %s", route.Name)
	}
	reconcilePassthroughRoute(route, owner, host, serviceName)
	if route.Labels == nil {
		route.Labels = map[string]string{}
	}
	route.Labels[hyperv1.RouteVisibilityLabel] = hyperv1.RouteVisibilityPrivate
	netutil.AddInternalRouteLabel(route)
	return nil
}

func ptrInt32(i int32) *int32 { return &i }

func intOrString(i int32) intstr.IntOrString { return intstr.FromInt32(i) }
