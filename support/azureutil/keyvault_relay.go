package azureutil

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// keyVaultRelayDialTimeout bounds a single connection attempt to the relay. The
// overall probe deadline is owned by the caller.
const keyVaultRelayDialTimeout = 30 * time.Second

// PrivateRouterKeyVaultClient builds the HTTP client the keys client uses to
// validate a Key Vault that only accepts traffic from the customer VNet.
//
// The management cluster has no route to the vault's private endpoint, but the
// private router does, and it already relays Key Vault connections for the
// azure-kms-provider sidecar (see the keyvault backend in the router config and
// the hostAlias on the KAS deployment). This resolves that same relay for the
// CPO's own client.
//
// Every error returned here means the relay is not usable yet: the router
// Service has no address, no https port, or no ready backend. Callers can treat
// that uniformly as "cannot tell yet" rather than a validation failure. The
// caller resolves keyVaultFQDN, so a vault that can never be resolved is
// reported as a configuration failure before this is reached.
func PrivateRouterKeyVaultClient(ctx context.Context, c client.Client, namespace, keyVaultFQDN string) (*http.Client, error) {
	routerService := manifests.PrivateRouterService(namespace)
	if err := c.Get(ctx, client.ObjectKeyFromObject(routerService), routerService); err != nil {
		return nil, fmt.Errorf("failed to get private-router service: %w", err)
	}

	clusterIP := routerService.Spec.ClusterIP
	if clusterIP == "" || clusterIP == corev1.ClusterIPNone {
		return nil, fmt.Errorf("private-router service %s/%s has no ClusterIP", routerService.Namespace, routerService.Name)
	}

	// Take the port from the Service rather than assuming 443 so this keeps
	// working if the relay is ever moved to a different port.
	var relayPort int32
	for _, port := range routerService.Spec.Ports {
		if port.Name == "https" {
			relayPort = port.Port
			break
		}
	}
	if relayPort == 0 {
		return nil, fmt.Errorf("private-router service %s/%s has no https port", routerService.Namespace, routerService.Name)
	}

	// The dial needs a ready backend behind the ClusterIP, which is what the
	// EndpointSlices report. The router Deployment's replica count is only a
	// proxy for that, and it is derived from the Service already read above, so
	// the relay's workload name does not have to be repeated here.
	endpointSlices := &discoveryv1.EndpointSliceList{}
	if err := c.List(ctx, endpointSlices,
		client.InNamespace(routerService.Namespace),
		client.MatchingLabels{discoveryv1.LabelServiceName: routerService.Name},
	); err != nil {
		return nil, fmt.Errorf("failed to list private-router endpoint slices: %w", err)
	}
	// A single ready endpoint is enough to attempt the probe. Waiting for a
	// completed rollout would unnecessarily skip validation during updates.
	if !hasReadyEndpoint(endpointSlices) {
		return nil, fmt.Errorf("private-router service %s/%s has no ready endpoints", routerService.Namespace, routerService.Name)
	}

	relayAddress := net.JoinHostPort(clusterIP, strconv.Itoa(int(relayPort)))
	return &http.Client{
		Transport: KeyVaultRelayTransport(keyVaultFQDN, relayAddress),
		// This client is handed to azcore as a policy.Transporter, so it is
		// http.Client.Do that follows redirects, not the SDK. A 3xx would
		// otherwise send the probe to a host the transport refuses to dial and
		// turn a redirect into a confusing dial error, so stop at the response
		// the vault actually returned.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}, nil
}

// hasReadyEndpoint reports whether any slice carries an endpoint that can serve
// traffic. An unset Ready condition means the publisher does not track
// readiness, which the EndpointSlice API defines as ready.
func hasReadyEndpoint(endpointSlices *discoveryv1.EndpointSliceList) bool {
	for _, slice := range endpointSlices.Items {
		for _, endpoint := range slice.Endpoints {
			if endpoint.Conditions.Ready == nil || *endpoint.Conditions.Ready {
				return true
			}
		}
	}
	return false
}

// KeyVaultRelayTransport returns a transport that opens connections addressed
// to keyVaultFQDN against relayAddress instead, and refuses to dial anything
// else.
//
// Only the TCP destination changes. The request still carries the vault
// hostname, so SNI, certificate verification and the bearer token are all
// unchanged, and the router forwards the stream to the vault's private endpoint
// without terminating TLS.
//
// The transport fails closed on any other host. Nothing legitimate reaches it:
// the credential authenticates to Entra over its own client, so the only way to
// arrive here with a different host is a redirect or a vault URL built from a
// malformed name. Dialing those would let a vault response steer a connection
// out of the CPO pod on the management cluster.
func KeyVaultRelayTransport(keyVaultFQDN, relayAddress string) *http.Transport {
	dialer := &net.Dialer{Timeout: keyVaultRelayDialTimeout, KeepAlive: 30 * time.Second}
	// http.DefaultTransport is a package-level variable that anything in the
	// process can replace, so it is not guaranteed to be an *http.Transport.
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		base = &http.Transport{}
	}
	transport := base.Clone()
	// The relay is a ClusterIP in this namespace. Honoring HTTP(S)_PROXY here
	// would hand the proxy address to DialContext instead of the vault FQDN, so
	// the rewrite below would never fire and the connection would be sent
	// somewhere that cannot reach the private endpoint.
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("refusing to dial malformed address %q from the private Key Vault probe: %w", address, err)
		}
		if !strings.EqualFold(host, keyVaultFQDN) {
			return nil, fmt.Errorf("refusing to dial %q from the private Key Vault probe: it may only reach %q", host, keyVaultFQDN)
		}
		return dialer.DialContext(ctx, network, relayAddress)
	}
	return transport
}
