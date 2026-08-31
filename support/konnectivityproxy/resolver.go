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

func (gr *guestClusterResolver) resolve(ctx context.Context, name string) ([]net.IP, error) {
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

	var ips []net.IP
	for _, addr := range addresses {
		if ip := net.ParseIP(addr); ip != nil {
			ips = append(ips, ip)
		}
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("no valid IPs parsed for %q", name)
	}
	return ips, nil
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
			ips, resolveErr := resolveAllIPs(ctx, name, d.preferIPv4)
			if resolveErr != nil {
				return ctx, nil, resolveErr
			}
			if len(ips) > 1 {
				ctx = contextWithFallbackIPs(ctx, ips[1:])
			}
			return ctx, ips[0], nil
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
		addresses, err := d.guestClusterResolver.resolve(ctx, name)
		if err != nil {
			l.Error(err, "failed to look up address from guest cluster")

			if d.resolveFromManagementCluster {
				l.Info("Attempting management cluster resolution to determine if konnectivity is down")

				// Use resolveAllIPs to get all management cluster DNS results.
				// We can't use defaultResolve because it returns nil,nil for SOCKS5 proxy.
				mgmtIPs, mgmtErr := resolveAllIPs(ctx, name, d.preferIPv4)

				// Only mark konnectivity as unhealthy if management cluster resolution succeeds.
				// If management cluster also fails, it's likely a legitimate DNS failure
				// rather than konnectivity being down.
				if mgmtErr == nil && len(mgmtIPs) > 0 {
					l.Info("Management cluster resolution succeeded - marking konnectivity as unhealthy")
					d.konnectivityHealth.markFailure()
				} else {
					l.Info("Management cluster resolution also failed - likely legitimate DNS failure, not marking konnectivity as unhealthy")
				}

				// Return the result from management cluster resolution.
				// If mustResolve is false (SOCKS5), return nil to let the proxy use system resolver.
				// If mustResolve is true (HTTPS), return the actual resolved IP.
				if d.mustResolve {
					if mgmtErr != nil {
						return ctx, nil, mgmtErr
					}
					if len(mgmtIPs) > 1 {
						ctx = contextWithFallbackIPs(ctx, mgmtIPs[1:])
					}
					return ctx, mgmtIPs[0], nil
				}
				return ctx, nil, nil
			}

			return ctx, nil, fmt.Errorf("failed to look up name %s from guest cluster cluster-dns: %w", name, err)
		}

		// DNS resolution succeeded - konnectivity is healthy
		d.konnectivityHealth.markSuccess()
		l.WithValues("address", addresses[0].String()).Info("Successfully looked up address from guest cluster")
		if len(addresses) > 1 {
			ctx = contextWithFallbackIPs(ctx, addresses[1:])
		}
		return ctx, addresses[0], nil
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
		ips, err := resolveAllIPs(ctx, name, d.preferIPv4)
		if err != nil {
			return ctx, nil, err
		}
		if len(ips) > 1 {
			ctx = contextWithFallbackIPs(ctx, ips[1:])
		}
		return ctx, ips[0], nil
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

// fallbackIPsKey is a context key for passing alternative resolved IPs from
// Resolve to DialContext. The socks5.NameResolver interface returns a single IP,
// so remaining IPs are carried in context for the dialer to try on failure.
type fallbackIPsKey struct{}

func contextWithFallbackIPs(ctx context.Context, ips []net.IP) context.Context {
	return context.WithValue(ctx, fallbackIPsKey{}, ips)
}

func fallbackIPsFromContext(ctx context.Context) []net.IP {
	ips, _ := ctx.Value(fallbackIPsKey{}).([]net.IP)
	return ips
}

// resolveAllIPs performs DNS resolution and returns all resolved IPs.
// When preferIPv4 is true, IPv4 addresses are sorted first.
// Falls back to socks5.DNSResolver on lookup failure.
func resolveAllIPs(ctx context.Context, name string, preferIPv4 bool) ([]net.IP, error) {
	// Short-circuit before touching the network when context is already done.
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, name)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// Fallback to socks5 resolver (single IP, best-effort).
		_, ip, sErr := socks5.DNSResolver{}.Resolve(ctx, name)
		if sErr != nil {
			return nil, fmt.Errorf("fallback DNS resolution failed for %q: %w", name, sErr)
		}
		if ip != nil {
			return []net.IP{ip}, nil
		}
		return nil, fmt.Errorf("no addresses found for %q", name)
	}

	var ips []net.IP
	if preferIPv4 {
		var ipv6 []net.IP
		for _, a := range addrs {
			if a.IP.To4() != nil {
				ips = append(ips, a.IP)
			} else {
				ipv6 = append(ipv6, a.IP)
			}
		}
		ips = append(ips, ipv6...)
	} else {
		for _, a := range addrs {
			ips = append(ips, a.IP)
		}
	}

	if len(ips) == 0 {
		return nil, fmt.Errorf("no addresses found for %q", name)
	}
	return ips, nil
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
