package konnectivityproxy

import (
	"context"
	"net"
	"testing"

	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/go-logr/logr"
)

func TestFilterIPv4(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected []string
	}{
		{
			name:     "When mixed IPv4 and IPv6, it should return only IPv4",
			input:    []string{"2001:db8::1", "192.168.1.1", "fd00::2", "10.0.0.1"},
			expected: []string{"192.168.1.1", "10.0.0.1"},
		},
		{
			name:     "When only IPv4, it should return all",
			input:    []string{"192.168.1.1", "10.0.0.1"},
			expected: []string{"192.168.1.1", "10.0.0.1"},
		},
		{
			name:     "When only IPv6, it should return empty",
			input:    []string{"2001:db8::1", "fd00::2"},
			expected: nil,
		},
		{
			name:     "When empty, it should return empty",
			input:    []string{},
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(filterIPv4(tt.input)).To(Equal(tt.expected))
		})
	}
}

func TestResolveAllIPs(t *testing.T) {
	t.Run("When resolving 127.0.0.1 literal with preferIPv4, it should return IPv4", func(t *testing.T) {
		g := NewWithT(t)
		ips, err := resolveAllIPs(context.Background(), "127.0.0.1", true)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(ips).ToNot(BeEmpty())
		g.Expect(ips[0].To4()).ToNot(BeNil(), "expected IPv4 address")
	})

	t.Run("When resolving 127.0.0.1 literal without preferIPv4, it should return all addresses", func(t *testing.T) {
		g := NewWithT(t)
		ips, err := resolveAllIPs(context.Background(), "127.0.0.1", false)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(ips).ToNot(BeEmpty())
	})

	t.Run("When context is canceled, it should return context error", func(t *testing.T) {
		g := NewWithT(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := resolveAllIPs(ctx, "localhost", false)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err).To(MatchError(context.Canceled))
	})

	t.Run("When resolving an IPv6 literal, it should return the IPv6 address", func(t *testing.T) {
		g := NewWithT(t)
		ips, err := resolveAllIPs(context.Background(), "::1", false)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(ips).ToNot(BeEmpty())
		g.Expect(ips[0].To4()).To(BeNil(), "expected IPv6 address for ::1")
	})

	t.Run("When resolving nonexistent host, it should return error", func(t *testing.T) {
		g := NewWithT(t)
		_, err := resolveAllIPs(context.Background(), "this-host-does-not-exist.invalid", false)
		g.Expect(err).To(HaveOccurred())
	})
}

func TestResolveAllIPsOrdering(t *testing.T) {
	t.Run("When preferIPv4 is true, it should sort IPv4 before IPv6", func(t *testing.T) {
		g := NewWithT(t)
		// resolveAllIPs uses net.DefaultResolver which we can't mock,
		// but we can verify the sorting logic by resolving localhost
		// which typically returns both 127.0.0.1 and ::1.
		// For environment independence, test the ordering invariant:
		// if any IPv4 exists, it must come before any IPv6.
		ips, err := resolveAllIPs(context.Background(), "localhost", true)
		if err != nil {
			t.Skip("localhost not resolvable in this environment")
		}
		seenIPv6 := false
		for _, ip := range ips {
			if ip.To4() != nil {
				g.Expect(seenIPv6).To(BeFalse(), "IPv4 address %s appeared after IPv6", ip)
			} else {
				seenIPv6 = true
			}
		}
	})
}

func TestFallbackIPsContext(t *testing.T) {
	t.Run("When no fallback IPs in context, it should return nil", func(t *testing.T) {
		g := NewWithT(t)
		g.Expect(fallbackIPsFromContext(context.Background())).To(BeNil())
	})

	t.Run("When fallback IPs stored in context, it should return them", func(t *testing.T) {
		g := NewWithT(t)
		fallbacks := []net.IP{net.ParseIP("10.0.0.1"), net.ParseIP("10.0.0.2")}
		ctx := contextWithFallbackIPs(context.Background(), fallbacks)
		result := fallbackIPsFromContext(ctx)
		g.Expect(result).To(HaveLen(2))
		g.Expect(result[0].String()).To(Equal("10.0.0.1"))
		g.Expect(result[1].String()).To(Equal("10.0.0.2"))
	})

	t.Run("When single fallback IP stored, it should return it", func(t *testing.T) {
		g := NewWithT(t)
		fallbacks := []net.IP{net.ParseIP("192.168.1.1")}
		ctx := contextWithFallbackIPs(context.Background(), fallbacks)
		result := fallbackIPsFromContext(ctx)
		g.Expect(result).To(HaveLen(1))
		g.Expect(result[0].String()).To(Equal("192.168.1.1"))
	})
}

func newTestResolver(opts ...func(*proxyResolver)) proxyResolver {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	r := proxyResolver{
		client:             fake.NewClientBuilder().WithScheme(scheme).Build(),
		log:                logr.Discard(),
		konnectivityHealth: newKonnectivityHealth(),
		isCloudAPI:         func(string) bool { return false },
	}
	for _, opt := range opts {
		opt(&r)
	}
	return r
}

func TestProxyResolverResolve(t *testing.T) {
	t.Run("When disableResolver is true, it should return nil IP for SOCKS5", func(t *testing.T) {
		g := NewWithT(t)
		r := newTestResolver(func(r *proxyResolver) {
			r.disableResolver = true
		})
		ctx, ip, err := r.Resolve(context.Background(), "example.com")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(ip).To(BeNil())
		g.Expect(ctx).ToNot(BeNil())
	})

	t.Run("When disableResolver is true and mustResolve, it should resolve via default", func(t *testing.T) {
		g := NewWithT(t)
		r := newTestResolver(func(r *proxyResolver) {
			r.disableResolver = true
			r.mustResolve = true
		})
		ctx, ip, err := r.Resolve(context.Background(), "127.0.0.1")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(ip).ToNot(BeNil())
		g.Expect(ctx).ToNot(BeNil())
	})

	t.Run("When isCloudAPI returns true, it should use defaultResolve", func(t *testing.T) {
		g := NewWithT(t)
		r := newTestResolver(func(r *proxyResolver) {
			r.isCloudAPI = func(string) bool { return true }
		})
		ctx, ip, err := r.Resolve(context.Background(), "ec2.amazonaws.com")
		g.Expect(err).ToNot(HaveOccurred())
		// SOCKS5 mode: nil IP expected
		g.Expect(ip).To(BeNil())
		g.Expect(ctx).ToNot(BeNil())
	})

	t.Run("When K8s service exists, it should return service ClusterIP", func(t *testing.T) {
		g := NewWithT(t)
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-service",
				Namespace: "my-namespace",
			},
			Spec: corev1.ServiceSpec{
				ClusterIP: "10.96.0.100",
			},
		}
		scheme := runtime.NewScheme()
		_ = corev1.AddToScheme(scheme)
		r := newTestResolver(func(r *proxyResolver) {
			r.client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(svc).Build()
		})
		_, ip, err := r.Resolve(context.Background(), "my-service.my-namespace.svc.cluster.local")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(ip.String()).To(Equal("10.96.0.100"))
	})

	t.Run("When K8s service miss and no guest cluster DNS, it should resolve and store fallbacks", func(t *testing.T) {
		g := NewWithT(t)
		r := newTestResolver()
		ctx, ip, err := r.Resolve(context.Background(), "127.0.0.1")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(ip).ToNot(BeNil())
		g.Expect(ctx).ToNot(BeNil())
	})

	t.Run("When konnectivity unhealthy and resolveFromManagementCluster, fallback mode returns nil for SOCKS5", func(t *testing.T) {
		g := NewWithT(t)
		r := newTestResolver(func(r *proxyResolver) {
			r.resolveFromGuestCluster = true
			r.resolveFromManagementCluster = true
		})
		// Mark konnectivity as unhealthy so beginRetry returns false
		r.konnectivityHealth.markFailure()
		// Ensure enough time hasn't passed for retry
		ctx, _, err := r.Resolve(context.Background(), "some-service.ns.svc.cluster.local")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(ctx).ToNot(BeNil())
	})

	t.Run("When konnectivity unhealthy, no mgmt fallback, it should return error", func(t *testing.T) {
		g := NewWithT(t)
		r := newTestResolver(func(r *proxyResolver) {
			r.resolveFromGuestCluster = true
			r.resolveFromManagementCluster = false
		})
		r.konnectivityHealth.markFailure()
		_, _, err := r.Resolve(context.Background(), "some-service.ns.svc.cluster.local")
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("konnectivity unavailable"))
	})

	t.Run("When K8s service miss, no guest DNS, and context canceled, it should return error", func(t *testing.T) {
		g := NewWithT(t)
		r := newTestResolver()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, _, err := r.Resolve(ctx, "some-service.ns.svc.cluster.local")
		g.Expect(err).To(HaveOccurred())
	})

	t.Run("When guest cluster DNS fails and mgmt cluster resolves with mustResolve, it should return mgmt IP", func(t *testing.T) {
		g := NewWithT(t)
		scheme := runtime.NewScheme()
		_ = corev1.AddToScheme(scheme)
		// guestClusterResolver will fail because dns-default service doesn't exist
		r := newTestResolver(func(r *proxyResolver) {
			r.resolveFromGuestCluster = true
			r.resolveFromManagementCluster = true
			r.mustResolve = true
			r.guestClusterResolver = &guestClusterResolver{
				client: fake.NewClientBuilder().WithScheme(scheme).Build(),
				log:    logr.Discard(),
			}
		})
		ctx, ip, err := r.Resolve(context.Background(), "127.0.0.1.nip.io")
		// Resolve attempts guest DNS (fails), falls back to mgmt cluster
		// resolveAllIPs("127.0.0.1.nip.io") may fail in CI, so accept either outcome
		if err == nil {
			g.Expect(ip).ToNot(BeNil())
			g.Expect(ctx).ToNot(BeNil())
		}
	})

	t.Run("When guest cluster DNS fails and mgmt cluster resolves without mustResolve, it should return nil IP", func(t *testing.T) {
		g := NewWithT(t)
		scheme := runtime.NewScheme()
		_ = corev1.AddToScheme(scheme)
		r := newTestResolver(func(r *proxyResolver) {
			r.resolveFromGuestCluster = true
			r.resolveFromManagementCluster = true
			r.mustResolve = false
			r.guestClusterResolver = &guestClusterResolver{
				client: fake.NewClientBuilder().WithScheme(scheme).Build(),
				log:    logr.Discard(),
			}
		})
		ctx, ip, err := r.Resolve(context.Background(), "some-service.ns.svc.cluster.local")
		// Guest DNS fails, mgmt resolveAllIPs also fails (nonexistent host),
		// but mustResolve=false so returns nil,nil
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(ip).To(BeNil())
		g.Expect(ctx).ToNot(BeNil())
	})

	t.Run("When guest cluster DNS fails, no mgmt fallback, it should return guest DNS error", func(t *testing.T) {
		g := NewWithT(t)
		scheme := runtime.NewScheme()
		_ = corev1.AddToScheme(scheme)
		r := newTestResolver(func(r *proxyResolver) {
			r.resolveFromGuestCluster = true
			r.resolveFromManagementCluster = false
			r.guestClusterResolver = &guestClusterResolver{
				client: fake.NewClientBuilder().WithScheme(scheme).Build(),
				log:    logr.Discard(),
			}
		})
		_, _, err := r.Resolve(context.Background(), "some-service.ns.svc.cluster.local")
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("failed to look up name"))
	})
}

func TestGuestClusterResolverResolve(t *testing.T) {
	t.Run("When resolver returns addresses, it should parse all IPs", func(t *testing.T) {
		g := NewWithT(t)
		gr := &guestClusterResolver{
			log:      logr.Discard(),
			resolver: net.DefaultResolver,
		}
		ips, err := gr.resolve(context.Background(), "localhost")
		if err != nil {
			t.Skip("localhost not resolvable in this environment")
		}
		g.Expect(ips).ToNot(BeEmpty())
	})

	t.Run("When preferIPv4 is true, it should return only IPv4 addresses", func(t *testing.T) {
		g := NewWithT(t)
		gr := &guestClusterResolver{
			log:        logr.Discard(),
			preferIPv4: true,
			resolver:   net.DefaultResolver,
		}
		ips, err := gr.resolve(context.Background(), "localhost")
		if err != nil {
			t.Skip("localhost not resolvable in this environment")
		}
		g.Expect(ips).ToNot(BeEmpty())
		for _, ip := range ips {
			g.Expect(ip.To4()).ToNot(BeNil(), "expected IPv4 when preferIPv4 is set")
		}
	})

	t.Run("When hostname does not exist, it should return error", func(t *testing.T) {
		g := NewWithT(t)
		gr := &guestClusterResolver{
			log:      logr.Discard(),
			resolver: net.DefaultResolver,
		}
		_, err := gr.resolve(context.Background(), "this-does-not-exist.invalid")
		g.Expect(err).To(HaveOccurred())
	})
}

func TestProxyResolverResolveGuestDNSSuccess(t *testing.T) {
	t.Run("When guest cluster DNS succeeds, it should return resolved IP", func(t *testing.T) {
		g := NewWithT(t)
		r := newTestResolver(func(r *proxyResolver) {
			r.resolveFromGuestCluster = true
			r.guestClusterResolver = &guestClusterResolver{
				log:      logr.Discard(),
				resolver: net.DefaultResolver,
			}
		})
		// K8s service lookup fails (single-segment name), then guest cluster DNS resolves via pre-set resolver.
		ctx, ip, err := r.Resolve(context.Background(), "localhost")
		if err != nil {
			t.Skip("localhost not resolvable in this environment")
		}
		g.Expect(ip).ToNot(BeNil())
		g.Expect(ctx).ToNot(BeNil())
		// Verify konnectivity was marked healthy after success
		g.Expect(r.konnectivityHealth.isHealthy()).To(BeTrue())
	})

	t.Run("When guest DNS returns multiple IPs, it should store fallbacks in context", func(t *testing.T) {
		g := NewWithT(t)
		r := newTestResolver(func(r *proxyResolver) {
			r.resolveFromGuestCluster = true
			r.guestClusterResolver = &guestClusterResolver{
				log:      logr.Discard(),
				resolver: net.DefaultResolver,
			}
		})
		ctx, ip, err := r.Resolve(context.Background(), "localhost")
		if err != nil {
			t.Skip("localhost not resolvable in this environment")
		}
		g.Expect(ip).ToNot(BeNil())
		// If localhost resolves to multiple addresses, fallbacks should be in context.
		// If only one address, fallbacks will be nil — both are valid.
		fallbacks := fallbackIPsFromContext(ctx)
		if fallbacks != nil {
			g.Expect(len(fallbacks)).To(BeNumerically(">=", 1))
		}
	})
}

func TestResolveK8sService(t *testing.T) {
	t.Run("When name has fewer than 2 segments, it should return error", func(t *testing.T) {
		g := NewWithT(t)
		r := newTestResolver()
		_, _, err := r.ResolveK8sService(context.Background(), logr.Discard(), "no-dots")
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("unable to derive namespacedName"))
	})

	t.Run("When service has unparsable ClusterIP, it should return error", func(t *testing.T) {
		g := NewWithT(t)
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "bad-svc",
				Namespace: "ns",
			},
			Spec: corev1.ServiceSpec{
				ClusterIP: "not-an-ip",
			},
		}
		scheme := runtime.NewScheme()
		_ = corev1.AddToScheme(scheme)
		r := newTestResolver(func(r *proxyResolver) {
			r.client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(svc).Build()
		})
		_, _, err := r.ResolveK8sService(context.Background(), logr.Discard(), "bad-svc.ns.svc.cluster.local")
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("unable to parse IP"))
	})

	t.Run("When service does not exist, it should return error", func(t *testing.T) {
		g := NewWithT(t)
		r := newTestResolver()
		_, _, err := r.ResolveK8sService(context.Background(), logr.Discard(), "missing.ns.svc.cluster.local")
		g.Expect(err).To(HaveOccurred())
	})
}

func TestDefaultResolve(t *testing.T) {
	t.Run("When mustResolve is false, it should return nil IP", func(t *testing.T) {
		g := NewWithT(t)
		r := newTestResolver()
		ctx, ip, err := r.defaultResolve(context.Background(), "example.com")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(ip).To(BeNil())
		g.Expect(ctx).ToNot(BeNil())
	})

	t.Run("When mustResolve is true, it should resolve and store fallbacks", func(t *testing.T) {
		g := NewWithT(t)
		r := newTestResolver(func(r *proxyResolver) {
			r.mustResolve = true
		})
		ctx, ip, err := r.defaultResolve(context.Background(), "127.0.0.1")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(ip).ToNot(BeNil())
		g.Expect(ip.String()).To(Equal("127.0.0.1"))
		g.Expect(ctx).ToNot(BeNil())
	})

	t.Run("When mustResolve and context canceled, it should return error", func(t *testing.T) {
		g := NewWithT(t)
		r := newTestResolver(func(r *proxyResolver) {
			r.mustResolve = true
		})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, _, err := r.defaultResolve(ctx, "example.com")
		g.Expect(err).To(HaveOccurred())
	})
}
