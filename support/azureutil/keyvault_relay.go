package azureutil

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"

	corev1 "k8s.io/api/core/v1"

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
// Returns an error while the router Service or Deployment is unavailable;
// callers should treat that as "cannot tell yet" rather than a validation failure.
func PrivateRouterKeyVaultClient(ctx context.Context, c client.Client, hcp *hyperv1.HostedControlPlane) (*http.Client, error) {
	keyVaultFQDN, err := GetKeyVaultFQDN(hcp)
	if err != nil {
		return nil, fmt.Errorf("failed to get Key Vault FQDN: %w", err)
	}

	routerService := manifests.PrivateRouterService(hcp.Namespace)
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

	routerDeployment := manifests.RouterDeployment(hcp.Namespace)
	if err := c.Get(ctx, client.ObjectKeyFromObject(routerDeployment), routerDeployment); err != nil {
		return nil, fmt.Errorf("failed to get router deployment: %w", err)
	}
	// An available replica is sufficient to attempt the probe. Requiring a
	// completed rollout would unnecessarily skip validation during updates.
	if routerDeployment.Status.AvailableReplicas == 0 {
		return nil, fmt.Errorf("router deployment %s/%s has no available replicas", routerDeployment.Namespace, routerDeployment.Name)
	}

	relayAddress := net.JoinHostPort(clusterIP, strconv.Itoa(int(relayPort)))
	return &http.Client{Transport: KeyVaultRelayTransport(keyVaultFQDN, relayAddress)}, nil
}

// KeyVaultRelayTransport returns a transport that opens connections addressed
// to keyVaultFQDN against relayAddress instead, and leaves every other host
// alone.
//
// Only the TCP destination changes. The request still carries the vault
// hostname, so SNI, certificate verification and the bearer token are all
// unchanged, and the router forwards the stream to the vault's private endpoint
// without terminating TLS. The host check keeps the rewrite off unrelated
// traffic the pipeline might emit.
func KeyVaultRelayTransport(keyVaultFQDN, relayAddress string) *http.Transport {
	dialer := &net.Dialer{Timeout: keyVaultRelayDialTimeout, KeepAlive: 30 * time.Second}
	// http.DefaultTransport is a package-level variable that anything in the
	// process can replace, so it is not guaranteed to be an *http.Transport.
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		base = &http.Transport{}
	}
	transport := base.Clone()
	// The relay is a ClusterIP in this namespace. Honouring HTTP(S)_PROXY here
	// would hand the proxy address to DialContext instead of the vault FQDN, so
	// the rewrite below would never fire and the connection would be sent
	// somewhere that cannot reach the private endpoint.
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if host, _, err := net.SplitHostPort(address); err == nil && strings.EqualFold(host, keyVaultFQDN) {
			return dialer.DialContext(ctx, network, relayAddress)
		}
		return dialer.DialContext(ctx, network, address)
	}
	return transport
}
