package konnectivityproxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/armon/go-socks5"
	"github.com/go-logr/logr"
)

// guestClusterResolver uses the Konnectivity dialer to perform a DNS lookup using
// the CoreDNS service of the hosted cluster. It does an initial lookup of the DNS
// service using a hosted cluster client to create an internal resolver that performs
// a TCP lookup on that service.
type guestClusterResolver struct {
	log                  logr.Logger
	client               client.Client
	konnectivityDialFunc func(ctx context.Context, network string, addr string) (net.Conn, error)
	preferIPv4           bool
	resolver             *net.Resolver
	resolverLock         sync.Mutex
}

func (gr *guestClusterResolver) getResolver(ctx context.Context) (*net.Resolver, error) {
	gr.resolverLock.Lock()
	defer gr.resolverLock.Unlock()
	if gr.resolver != nil {
		return gr.resolver, nil
	}
	dnsService := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: "openshift-dns", Name: "dns-default"}}
	if err := gr.client.Get(ctx, client.ObjectKeyFromObject(dnsService), dnsService); err != nil {
		return nil, fmt.Errorf("failed to get dns service from guest cluster: %w", err)
	}
	dnsIP := dnsService.Spec.ClusterIP
	if net.ParseIP(dnsIP) != nil && strings.Contains(dnsIP, ":") && !strings.HasPrefix(dnsIP, "[") {
		dnsIP = fmt.Sprintf("[%s]", dnsIP)
	}
	clusterDNSAddress := dnsIP + ":53"
	gr.resolver = &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			return gr.konnectivityDialFunc(ctx, "tcp", clusterDNSAddress)
		},
	}

	return gr.resolver, nil
}

func (gr *guestClusterResolver) resolve(ctx context.Context, name string) (net.IP, error) {
	resolver, err := gr.getResolver(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get resolver: %w", err)

	}
	addresses, err := resolver.LookupHost(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve %q: %w", name, err)
	}
	if len(addresses) == 0 {
		return nil, errors.New("no addresses found")
	}

	// On single-stack IPv4 data planes, filter to IPv4 so the konnectivity-agent
	// doesn't try to connect to unreachable IPv6 endpoints.
	if gr.preferIPv4 {
		if filtered := filterIPv4(addresses); len(filtered) > 0 {
			addresses = filtered
		}
	}

	address := net.ParseIP(addresses[0])
	if address == nil {
		return nil, fmt.Errorf("failed to parse address %q as IP", addresses[0])
	}
	return address, nil
}

// proxyResolver tries to resolve addresses using the following steps in order:
// 1. Not at all for cloud provider apis (we do not want to tunnel them through Konnectivity) or when disableResolver is true.
// 2. If the address is a valid Kubernetes service and that service exists in the guest cluster, its clusterIP is returned.
// 3. If --resolve-from-guest-cluster-dns is set, it uses the guest clusters dns. If that fails, fallback to the management cluster's resolution.
// 4. Lastly, Golang's default resolver is used.
type proxyResolver struct {
	client                       client.Client
	disableResolver              bool
	resolveFromGuestCluster      bool
	resolveFromManagementCluster bool
	mustResolve                  bool
	preferIPv4                   bool
	konnectivityHealth           *konnectivityHealth
	guestClusterResolver         *guestClusterResolver
	log                          logr.Logger
	isCloudAPI                   func(string) bool
}

func (d proxyResolver) Resolve(ctx context.Context, name string) (context.Context, net.IP, error) {
	// Preserve the host so we can recognize it
	if d.isCloudAPI(name) || d.disableResolver {
		return d.defaultResolve(ctx, name)
	}
	l := d.log.WithValues("name", name)
	_, ip, err := d.ResolveK8sService(ctx, l, name)
	if err != nil {
		l.Info("failed to resolve address from Kubernetes service", "err", err.Error())
		if !d.resolveFromGuestCluster {
			if d.preferIPv4 {
				return resolvePreferIPv4(ctx, name)
			}
			return socks5.DNSResolver{}.Resolve(ctx, name)
		}

		// Check if we should attempt guest cluster DNS resolution
		// This implements retry logic with debouncing to avoid multiple simultaneous retries
		if !d.konnectivityHealth.beginRetry() {
			// In fallback mode and either another retry is in progress or too soon to retry
			if d.resolveFromManagementCluster {
				l.Info("Using management cluster resolution (konnectivity in fallback mode)")
				return d.defaultResolve(ctx, name)
			}
			return ctx, nil, fmt.Errorf("konnectivity unavailable, cannot resolve %s", name)
		}

		// Always clear retry-in-progress regardless of outcome.
		defer d.konnectivityHealth.endRetry()

		l.Info("attempting to resolve address from guest cluster cluster-dns")
		address, err := d.guestClusterResolver.resolve(ctx, name)
		if err != nil {
			l.Error(err, "failed to look up address from guest cluster")

			if d.resolveFromManagementCluster {
				l.Info("Attempting management cluster resolution to determine if konnectivity is down")

				// Use the standard DNS resolver to actually resolve the name via management cluster DNS.
				// We can't use defaultResolve because it returns nil,nil for SOCKS5 proxy.
				var mgmtCtx context.Context
				var mgmtIP net.IP
				var mgmtErr error
				if d.preferIPv4 {
					mgmtCtx, mgmtIP, mgmtErr = resolvePreferIPv4(ctx, name)
				} else {
					mgmtCtx, mgmtIP, mgmtErr = socks5.DNSResolver{}.Resolve(ctx, name)
				}

				// Only mark konnectivity as unhealthy if management cluster resolution succeeds
				// If management cluster also fails, it's likely a legitimate DNS failure (name doesn't exist)
				// rather than konnectivity being down
				if mgmtErr == nil && mgmtIP != nil {
					l.Info("Management cluster resolution succeeded - marking konnectivity as unhealthy")
					d.konnectivityHealth.markFailure()
				} else {
					l.Info("Management cluster resolution also failed - likely legitimate DNS failure, not marking konnectivity as unhealthy")
				}

				// Return the result from management cluster resolution
				// If mustResolve is false (SOCKS5), return nil to let the proxy use system resolver
				// If mustResolve is true (HTTPS), return the actual resolved IP
				if d.mustResolve {
					return mgmtCtx, mgmtIP, mgmtErr
				}
				return ctx, nil, nil
			}

			return ctx, nil, fmt.Errorf("failed to look up name %s from guest cluster cluster-dns: %w", name, err)
		}

		// DNS resolution succeeded - konnectivity is healthy
		d.konnectivityHealth.markSuccess()
		l.WithValues("address", address.String()).Info("Successfully looked up address from guest cluster")
		return ctx, address, nil
	}

	return ctx, ip, nil
}

func (d proxyResolver) defaultResolve(ctx context.Context, name string) (context.Context, net.IP, error) {
	// When the resolver is used by the socks5 proxy, a nil response by the resolver
	// results in the proxy just using the default system resolver. However, when used by
	// the http proxy, a nil response will cause an invalid CONNECT string to be created,
	// so we must have a valid response.
	// d.mustResolve will be set to true if the dialer needs to resolve names before
	// dialing (which is the case of the https proxy)
	if d.mustResolve {
		if d.preferIPv4 {
			return resolvePreferIPv4(ctx, name)
		}
		return socks5.DNSResolver{}.Resolve(ctx, name)
	}
	return ctx, nil, nil
}

func (d proxyResolver) ResolveK8sService(ctx context.Context, l logr.Logger, name string) (context.Context, net.IP, error) {
	namespaceNamedService := strings.Split(name, ".")
	if len(namespaceNamedService) < 2 {
		return nil, nil, fmt.Errorf("unable to derive namespacedName from %v", name)
	}
	namespacedName := types.NamespacedName{
		Namespace: namespaceNamedService[1],
		Name:      namespaceNamedService[0],
	}

	service := &corev1.Service{}
	err := d.client.Get(ctx, namespacedName, service)
	if err != nil {
		return nil, nil, err
	}

	// Convert service name to ip address...
	ip := net.ParseIP(service.Spec.ClusterIP)
	if ip == nil {
		return nil, nil, fmt.Errorf("unable to parse IP %v", ip)
	}

	l.Info("resolved address from Kubernetes service", "ip", ip.String())

	return ctx, ip, nil
}

// resolvePreferIPv4 wraps DNS resolution to prefer IPv4 addresses so
// the konnectivity-agent on single-stack data planes can reach the target.
// Uses net.DefaultResolver.LookupIPAddr to honor context cancellation/deadlines.
func resolvePreferIPv4(ctx context.Context, name string) (context.Context, net.IP, error) {
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, name)
	if err != nil {
		// Don't fall through to uncancelable socks5.DNSResolver if context is done.
		if ctx.Err() != nil {
			return ctx, nil, ctx.Err()
		}
		// Fallback intentionally does not filter for IPv4 — resolving something is better than failing.
		return socks5.DNSResolver{}.Resolve(ctx, name)
	}
	// Return the first IPv4 address if available, otherwise the first address.
	for _, a := range addrs {
		if a.IP.To4() != nil {
			return ctx, a.IP, nil
		}
	}
	if len(addrs) > 0 {
		return ctx, addrs[0].IP, nil
	}
	// Don't fall through to uncancelable socks5.DNSResolver if context is done.
	if ctx.Err() != nil {
		return ctx, nil, ctx.Err()
	}
	// Fallback intentionally does not filter for IPv4 — resolving something is better than failing.
	return socks5.DNSResolver{}.Resolve(ctx, name)
}

// filterIPv4 returns only IPv4 addresses from the input slice.
func filterIPv4(addresses []string) []string {
	var filtered []string
	for _, addr := range addresses {
		if ip := net.ParseIP(addr); ip != nil && ip.To4() != nil {
			filtered = append(filtered, addr)
		}
	}
	return filtered
}
