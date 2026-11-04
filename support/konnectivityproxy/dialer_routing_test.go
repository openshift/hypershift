package konnectivityproxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/armon/go-socks5"
	"golang.org/x/net/proxy"
)

type routingDNS struct {
	addresses []net.IP
	fail      atomic.Bool
	queries   atomic.Int32
	before    func()
}

func (server *routingDNS) serve(t *testing.T, connection net.Conn) {
	t.Helper()
	for {
		var length uint16
		if err := binary.Read(connection, binary.BigEndian, &length); err != nil {
			return
		}
		packet := make([]byte, length)
		if _, err := io.ReadFull(connection, packet); err != nil {
			return
		}
		if len(packet) < 17 || binary.BigEndian.Uint16(packet[4:6]) != 1 {
			t.Error("expected a DNS query with one question")
			return
		}
		questionEnd := 12
		for questionEnd < len(packet) && packet[questionEnd] != 0 {
			questionEnd += int(packet[questionEnd]) + 1
		}
		questionEnd++
		if questionEnd+4 > len(packet) {
			t.Error("truncated DNS question")
			return
		}
		queryType := binary.BigEndian.Uint16(packet[questionEnd:])
		if queryType == 1 {
			server.queries.Add(1)
		}
		if server.before != nil {
			server.before()
		}
		packet = packet[:questionEnd+4]
		binary.BigEndian.PutUint16(packet[2:4], 0x8180)
		clear(packet[6:12])
		if server.fail.Load() {
			packet[3] = 0x83
		} else {
			var answers uint16
			for _, address := range server.addresses {
				addressBytes := address.To4()
				if queryType == 28 && addressBytes == nil {
					addressBytes = address.To16()
				} else if queryType != 1 {
					continue
				}
				if addressBytes == nil {
					continue
				}
				packet = append(packet, 0xc0, 0x0c)
				packet = binary.BigEndian.AppendUint16(packet, queryType)
				packet = binary.BigEndian.AppendUint16(packet, 1)
				packet = binary.BigEndian.AppendUint32(packet, 0)
				packet = binary.BigEndian.AppendUint16(packet, uint16(len(addressBytes)))
				packet = append(packet, addressBytes...)
				answers++
			}
			binary.BigEndian.PutUint16(packet[6:8], answers)
		}
		if err := binary.Write(connection, binary.BigEndian, uint16(len(packet))); err != nil {
			return
		}
		if _, err := connection.Write(packet); err != nil {
			return
		}
	}
}

func routingListener(t *testing.T, configuration *tls.Config, serve func(net.Conn)) net.Listener {
	t.Helper()
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if configuration != nil {
		listener = tls.NewListener(listener, configuration)
	}
	var workers sync.WaitGroup
	workers.Add(1)
	go func() {
		defer workers.Done()
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer connection.Close()
				stop := context.AfterFunc(t.Context(), func() { connection.Close() })
				defer stop()
				if err := connection.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
					if t.Context().Err() == nil {
						t.Errorf("set fixture deadline: %v", err)
					}
					return
				}
				serve(connection)
			}()
		}
	}()
	t.Cleanup(func() {
		listener.Close()
		workers.Wait()
	})
	return listener
}

type routingFixture struct {
	proxy       *konnectivityProxy
	guest       *routingDNS
	management  *routingDNS
	direct      atomic.Int32
	dnsConnects atomic.Int32
	mutex       sync.Mutex
	targets     []string
	hosts       []string
	port        string
	reject      bool
	stall       bool
	onConnect   func()
}

func newRoutingFixture(t *testing.T) *routingFixture {
	t.Helper()
	fixture := &routingFixture{
		guest:      &routingDNS{addresses: []net.IP{net.ParseIP("192.0.2.10")}},
		management: &routingDNS{addresses: []net.IP{net.ParseIP("127.0.0.1")}},
	}
	direct := routingListener(t, nil, func(connection net.Conn) {
		fixture.direct.Add(1)
		if _, err := io.ReadFull(connection, make([]byte, 1)); err != nil {
			return
		}
		_, _ = io.WriteString(connection, "direct\n")
	})
	_, fixture.port, _ = net.SplitHostPort(direct.Addr().String())
	managementDNS := routingListener(t, nil, func(connection net.Conn) { fixture.management.serve(t, connection) })
	guestDNS := routingListener(t, nil, func(connection net.Conn) { fixture.guest.serve(t, connection) })
	previousResolver := net.DefaultResolver
	net.DefaultResolver = routingResolver(managementDNS.Addr().String())
	t.Cleanup(func() { net.DefaultResolver = previousResolver })
	certificateServer := httptest.NewTLSServer(nil)
	certificate := certificateServer.TLS.Certificates[0]
	clientConfiguration := certificateServer.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	certificateServer.Close()
	tunnel := routingListener(t, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}, func(connection net.Conn) {
		reader := bufio.NewReader(connection)
		requestLine, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		parts := strings.Fields(requestLine)
		if len(parts) != 3 || parts[0] != "CONNECT" {
			t.Errorf("unexpected CONNECT request: %q", requestLine)
			return
		}
		var host string
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			if line == "\r\n" {
				break
			}
			if strings.HasPrefix(line, "Host: ") {
				host = strings.TrimSpace(strings.TrimPrefix(line, "Host: "))
			}
		}
		if parts[1] == "10.0.0.53:53" {
			fixture.dnsConnects.Add(1)
			dnsConnection, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", guestDNS.Addr().String())
			if err != nil {
				t.Errorf("dial fixture DNS: %v", err)
				return
			}
			defer dnsConnection.Close()
			if _, err := io.WriteString(connection, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
				return
			}
			var copies sync.WaitGroup
			copies.Add(1)
			go func() {
				defer copies.Done()
				_, _ = io.Copy(dnsConnection, reader)
				dnsConnection.Close()
			}()
			_, _ = io.Copy(connection, dnsConnection)
			connection.Close()
			copies.Wait()
			return
		}
		fixture.mutex.Lock()
		fixture.targets = append(fixture.targets, parts[1])
		fixture.hosts = append(fixture.hosts, host)
		fixture.mutex.Unlock()
		if fixture.stall {
			if fixture.onConnect != nil {
				fixture.onConnect()
			}
			_, _ = io.Copy(io.Discard, reader)
			return
		}
		if fixture.reject || strings.HasPrefix(parts[1], "192.0.2.1:") {
			_, _ = io.WriteString(connection, "HTTP/1.1 502 Bad Gateway\r\n\r\n")
			return
		}
		_, _ = io.WriteString(connection, "HTTP/1.1 200 Connection Established\r\n\r\n")
		if _, err := io.ReadFull(reader, make([]byte, 1)); err != nil {
			return
		}
		_, _ = io.WriteString(connection, "tunnel\n")
	})
	proxyHost, proxyPort, _ := net.SplitHostPort(tunnel.Addr().String())
	portNumber, err := strconv.ParseUint(proxyPort, 10, 32)
	if err != nil {
		t.Fatal(err)
	}
	resolver := newTestResolver(func(resolver *proxyResolver) {
		resolver.mustResolve = true
		resolver.resolveFromGuestCluster = true
		resolver.resolveFromManagementCluster = true
	})
	fixture.proxy = &konnectivityProxy{
		konnectivityHost: proxyHost, konnectivityPort: uint32(portNumber),
		resolveBeforeDial: true, resolveFromManagementClusterDNS: true,
		proxyResolver: resolver,
	}
	fixture.proxy.tlsConfigOnce.Do(func() { fixture.proxy.tlsConfig = clientConfiguration })
	fixture.proxy.guestClusterResolver = &guestClusterResolver{
		log: resolver.log, resolver: routingResolver(guestDNS.Addr().String()),
	}
	return fixture
}

func routingResolver(address string) *net.Resolver {
	return &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", address)
	}}
}

func (fixture *routingFixture) fallback(expired bool) {
	health := fixture.proxy.konnectivityHealth
	health.markFailure()
	if expired {
		health.mutex.Lock()
		health.lastRetryTime = time.Now().Add(-2 * health.retryInterval)
		health.mutex.Unlock()
	}
}

func (fixture *routingFixture) dial(t *testing.T, expected string) {
	t.Helper()
	connection, err := fixture.proxy.DialContext(t.Context(), "tcp", net.JoinHostPort("idp.example.test", fixture.port))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	routingResponse(t, connection, expected)
}

func routingResponse(t *testing.T, connection net.Conn, expected string) {
	t.Helper()
	if _, err := connection.Write([]byte("!")); err != nil {
		t.Fatal(err)
	}
	if err := connection.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	response, err := bufio.NewReader(connection).ReadString('\n')
	if err != nil || response != expected+"\n" {
		t.Fatalf("expected %s route, got %q, error %v", expected, response, err)
	}
}

func routingSOCKSDialer(t *testing.T, dialer *konnectivityProxy) proxy.ContextDialer {
	t.Helper()
	dialer.resolveBeforeDial = false
	dialer.mustResolve = false
	server, err := socks5.New(&socks5.Config{Dial: dialer.DialContext, Resolver: dialer})
	if err != nil {
		t.Fatal(err)
	}
	listener := routingListener(t, nil, func(connection net.Conn) { _ = server.ServeConn(connection) })
	socksDialer, err := proxy.SOCKS5("tcp", listener.Addr().String(), nil, proxy.Direct)
	if err != nil {
		t.Fatal(err)
	}
	return socksDialer.(proxy.ContextDialer)
}

func (fixture *routingFixture) assertAttempts(t *testing.T, host string, targets ...string) {
	t.Helper()
	fixture.mutex.Lock()
	defer fixture.mutex.Unlock()
	if strings.Join(fixture.targets, ",") != strings.Join(targets, ",") {
		t.Fatalf("CONNECT targets: got %v, want %v", fixture.targets, targets)
	}
	for _, actualHost := range fixture.hosts {
		if actualHost != host {
			t.Errorf("CONNECT Host: got %q, want %q", actualHost, host)
		}
	}
}

func TestKonnectivityProxy_DialContext(t *testing.T) {
	testDialContextRecovery(t)
	testDialContextRecoveryFailures(t)
	testDialContextDNSRouting(t)
	testDialContextResolutionModes(t)
	testDialContextCloudAndCancellation(t)
	testDialContextSOCKS5(t)
}

func testDialContextRecovery(t *testing.T) {
	t.Helper()
	t.Run("When an expired fallback recovers, it should tunnel the next hostname request", func(t *testing.T) {
		fixture := newRoutingFixture(t)
		fixture.fallback(true)
		fixture.dial(t, "tunnel")
		if !fixture.proxy.konnectivityHealth.isHealthy() || fixture.guest.queries.Load() != 1 || fixture.direct.Load() != 0 {
			t.Fatalf("recovery: healthy=%v guest=%d direct=%d", fixture.proxy.konnectivityHealth.isHealthy(), fixture.guest.queries.Load(), fixture.direct.Load())
		}
		fixture.assertAttempts(t, "idp.example.test", net.JoinHostPort("192.0.2.10", fixture.port))
	})
	t.Run("When guest DNS first fails, it should directly dial the same request", func(t *testing.T) {
		fixture := newRoutingFixture(t)
		fixture.guest.fail.Store(true)
		fixture.dial(t, "direct")
		if fixture.proxy.konnectivityHealth.isHealthy() || fixture.guest.queries.Load() == 0 || fixture.direct.Load() != 1 {
			t.Fatalf("fallback: healthy=%v guest=%d direct=%d", fixture.proxy.konnectivityHealth.isHealthy(), fixture.guest.queries.Load(), fixture.direct.Load())
		}
		fixture.assertAttempts(t, "")
	})
	t.Run("When the retry interval has not expired, it should suppress guest probes", func(t *testing.T) {
		fixture := newRoutingFixture(t)
		fixture.fallback(false)
		previousRetry := fixture.proxy.konnectivityHealth.lastRetryTime
		fixture.dial(t, "direct")
		fixture.dial(t, "direct")
		if fixture.guest.queries.Load() != 0 || fixture.proxy.konnectivityHealth.lastRetryTime != previousRetry {
			t.Fatal("suppressed requests probed guest DNS or changed retry timestamp")
		}
		fixture.assertAttempts(t, "")
	})
	t.Run("When a recovery retry is active, it should suppress concurrent guest probes", func(t *testing.T) {
		fixture := newRoutingFixture(t)
		fixture.fallback(true)
		entered, release := make(chan struct{}), make(chan struct{})
		var enteredOnce sync.Once
		fixture.guest.before = func() {
			enteredOnce.Do(func() { close(entered) })
			select {
			case <-release:
			case <-t.Context().Done():
			}
		}
		result := make(chan error, 1)
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		go func() {
			connection, err := fixture.proxy.DialContext(ctx, "tcp", net.JoinHostPort("idp.example.test", fixture.port))
			if connection != nil {
				connection.Close()
			}
			result <- err
		}()
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal("recovery did not enter guest DNS")
		}
		fixture.dial(t, "direct")
		close(release)
		if err := <-result; err != nil {
			t.Fatal(err)
		}
		if fixture.guest.queries.Load() != 1 || fixture.proxy.konnectivityHealth.activeRetry || !fixture.proxy.konnectivityHealth.isHealthy() {
			t.Fatal("recovery was duplicated or not cleaned up")
		}
	})
}

func testDialContextRecoveryFailures(t *testing.T) {
	t.Helper()
	t.Run("When management DNS has multiple addresses, it should keep the original hostname for direct dialing", func(t *testing.T) {
		fixture := newRoutingFixture(t)
		fixture.guest.fail.Store(true)
		fixture.management.addresses = []net.IP{net.ParseIP("127.0.0.2"), net.ParseIP("127.0.0.1")}
		_, primary, err := fixture.proxy.defaultResolve(t.Context(), "idp.example.test")
		if err != nil || !primary.Equal(net.ParseIP("127.0.0.2")) {
			t.Fatalf("fixture primary: %v, %v", primary, err)
		}
		fixture.dial(t, "direct")
		fixture.assertAttempts(t, "")
	})
	for _, scenario := range []struct {
		name   string
		cancel bool
	}{
		{name: "When a recovery probe fails, it should release the active retry"},
		{name: "When a recovery probe is canceled, it should release the active retry", cancel: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			fixture := newRoutingFixture(t)
			fixture.fallback(true)
			fixture.guest.fail.Store(true)
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			fixture.guest.before = func() {
				once.Do(func() { close(entered) })
				select {
				case <-release:
				case <-t.Context().Done():
				}
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			result := make(chan error, 1)
			go func() {
				connection, err := fixture.proxy.DialContext(ctx, "tcp", net.JoinHostPort("idp.example.test", fixture.port))
				if connection != nil {
					connection.Close()
				}
				result <- err
			}()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("recovery probe never started")
			}
			if scenario.cancel {
				cancel()
			}
			close(release)
			err := <-result
			if scenario.cancel && !errors.Is(err, context.Canceled) {
				t.Fatalf("expected canceled recovery, got %v", err)
			}
			if !scenario.cancel && err != nil {
				t.Fatal(err)
			}
			if fixture.proxy.konnectivityHealth.activeRetry || fixture.proxy.konnectivityHealth.isHealthy() {
				t.Fatal("failed recovery did not retain fallback and clear active retry")
			}
		})
	}
}

func testDialContextDNSRouting(t *testing.T) {
	t.Helper()
	t.Run("When guest DNS returns alternatives, it should retain context and original CONNECT Host", func(t *testing.T) {
		fixture := newRoutingFixture(t)
		fixture.guest.addresses = []net.IP{net.ParseIP("192.0.2.1"), net.ParseIP("192.0.2.2")}
		fixture.dial(t, "tunnel")
		fixture.assertAttempts(t, "idp.example.test", net.JoinHostPort("192.0.2.1", fixture.port), net.JoinHostPort("192.0.2.2", fixture.port))
	})
	t.Run("When all tunnel addresses fail, it should attempt at most three alternatives", func(t *testing.T) {
		fixture := newRoutingFixture(t)
		fixture.guest.addresses = []net.IP{net.ParseIP("192.0.2.1"), net.ParseIP("192.0.2.2"), net.ParseIP("192.0.2.3"), net.ParseIP("192.0.2.4"), net.ParseIP("192.0.2.5")}
		fixture.reject = true
		connection, err := fixture.proxy.DialContext(t.Context(), "tcp", "idp.example.test:443")
		if connection != nil || err == nil {
			t.Fatalf("expected failed tunnel: %v, %v", connection, err)
		}
		fixture.assertAttempts(t, "idp.example.test", "192.0.2.1:443", "192.0.2.2:443", "192.0.2.3:443", "192.0.2.4:443")
	})
	for _, scenario := range []struct {
		name       string
		management bool
	}{
		{name: "When fallback is disabled, it should return the guest DNS error"},
		{name: "When both DNS sources fail, it should not newly mark the tunnel unhealthy", management: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			fixture := newRoutingFixture(t)
			fixture.guest.fail.Store(true)
			fixture.management.fail.Store(true)
			fixture.proxy.resolveFromManagementCluster = scenario.management
			fixture.proxy.resolveFromManagementClusterDNS = scenario.management
			connection, err := fixture.proxy.DialContext(t.Context(), "tcp", "idp.example.test:443")
			if connection != nil || err == nil || !fixture.proxy.konnectivityHealth.isHealthy() || fixture.proxy.konnectivityHealth.activeRetry {
				t.Fatalf("DNS error contract: conn=%v err=%v healthy=%v", connection, err, fixture.proxy.konnectivityHealth.isHealthy())
			}
			fixture.assertAttempts(t, "")
		})
	}
	t.Run("When recovery uses the guest DNS service, it should tunnel port 53 without recursion", func(t *testing.T) {
		fixture := newRoutingFixture(t)
		fixture.fallback(true)
		service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "dns-default", Namespace: "openshift-dns"}, Spec: corev1.ServiceSpec{ClusterIP: "10.0.0.53"}}
		if err := fixture.proxy.client.Create(t.Context(), service); err != nil {
			t.Fatal(err)
		}
		fixture.proxy.guestClusterResolver = &guestClusterResolver{client: fixture.proxy.client, log: fixture.proxy.log, konnectivityDialFunc: fixture.proxy.DialContext}
		fixture.dial(t, "tunnel")
		if fixture.dnsConnects.Load() == 0 || fixture.direct.Load() != 0 || !fixture.proxy.konnectivityHealth.isHealthy() {
			t.Fatal("guest DNS did not recover through the tunnel")
		}
	})
}

func testDialContextResolutionModes(t *testing.T) {
	t.Helper()
	for _, scenario := range []struct {
		name      string
		unhealthy bool
	}{
		{name: "When a Service exists with a healthy tunnel, it should precede guest DNS", unhealthy: false},
		{name: "When a Service exists during management fallback, it should precede guest DNS", unhealthy: true},
	} {
		unhealthy := scenario.unhealthy
		t.Run(scenario.name, func(t *testing.T) {
			fixture := newRoutingFixture(t)
			if unhealthy {
				fixture.fallback(true)
			}
			service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "idp", Namespace: "example"}, Spec: corev1.ServiceSpec{ClusterIP: "192.0.2.20"}}
			if err := fixture.proxy.client.Create(t.Context(), service); err != nil {
				t.Fatal(err)
			}
			if unhealthy {
				fixture.dial(t, "direct")
				fixture.assertAttempts(t, "")
			} else {
				fixture.dial(t, "tunnel")
				fixture.assertAttempts(t, "idp.example.test", net.JoinHostPort("192.0.2.20", fixture.port))
			}
			if fixture.guest.queries.Load() != 0 || fixture.proxy.konnectivityHealth.isHealthy() == unhealthy {
				t.Fatal("Service resolution probed guest DNS or changed health")
			}
		})
	}
	for _, scenario := range []struct {
		name    string
		address string
	}{
		{name: "When the destination is an IPv4 literal, it should skip resolution", address: "127.0.0.1:443"},
		{name: "When the destination is an IPv6 literal, it should skip resolution", address: "[::1]:443"},
	} {
		address := scenario.address
		t.Run(scenario.name, func(t *testing.T) {
			fixture := newRoutingFixture(t)
			connection, err := fixture.proxy.DialContext(t.Context(), "tcp", address)
			if err != nil {
				t.Fatal(err)
			}
			connection.Close()
			host, _, _ := net.SplitHostPort(address)
			fixture.assertAttempts(t, host, address)
			if fixture.guest.queries.Load() != 0 || fixture.management.queries.Load() != 0 {
				t.Fatal("IP literal triggered DNS")
			}
		})
	}
	t.Run("When resolution is disabled, it should preserve hostname tunneling", func(t *testing.T) {
		fixture := newRoutingFixture(t)
		fixture.proxy.disableResolver = true
		fixture.dial(t, "tunnel")
		fixture.assertAttempts(t, "idp.example.test", net.JoinHostPort("idp.example.test", fixture.port))
		if fixture.guest.queries.Load() != 0 || fixture.management.queries.Load() != 0 {
			t.Fatal("disabled resolver triggered DNS")
		}
	})
	t.Run("When resolution is disabled during fallback, it should directly dial without guest probes", func(t *testing.T) {
		fixture := newRoutingFixture(t)
		fixture.fallback(true)
		fixture.proxy.disableResolver = true
		fixture.dial(t, "direct")
		if fixture.guest.queries.Load() != 0 || fixture.proxy.konnectivityHealth.isHealthy() {
			t.Fatal("disabled resolver attempted recovery")
		}
		fixture.assertAttempts(t, "")
	})
	t.Run("When a DNS hostname request selects fallback, it should still tunnel port 53", func(t *testing.T) {
		fixture := newRoutingFixture(t)
		fixture.guest.fail.Store(true)
		connection, err := fixture.proxy.DialContext(t.Context(), "tcp", "idp.example.test:53")
		if err != nil {
			t.Fatal(err)
		}
		connection.Close()
		fixture.assertAttempts(t, "idp.example.test", "127.0.0.1:53")
		if fixture.guest.queries.Load() == 0 || fixture.direct.Load() != 0 || fixture.proxy.konnectivityHealth.isHealthy() {
			t.Fatal("hostname port 53 changed resolution or bypassed tunnel")
		}
	})
	t.Run("When an IP request is canceled during fallback, it should retain direct dial cancellation", func(t *testing.T) {
		fixture := newRoutingFixture(t)
		fixture.fallback(true)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		connection, err := fixture.proxy.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", fixture.port))
		if connection != nil || !errors.Is(err, context.Canceled) || fixture.guest.queries.Load() != 0 {
			t.Fatalf("canceled direct dial: conn=%v err=%v", connection, err)
		}
		fixture.assertAttempts(t, "")
	})
	t.Run("When guest resolution is disabled, it should resolve using management DNS before tunneling", func(t *testing.T) {
		fixture := newRoutingFixture(t)
		fixture.proxy.resolveFromGuestCluster = false
		fixture.dial(t, "tunnel")
		fixture.assertAttempts(t, "idp.example.test", net.JoinHostPort("127.0.0.1", fixture.port))
		if fixture.guest.queries.Load() != 0 || fixture.management.queries.Load() == 0 {
			t.Fatal("wrong DNS source")
		}
	})
}

func testDialContextCloudAndCancellation(t *testing.T) {
	t.Helper()
	t.Run("When the hostname is a cloud API, it should bypass recovery and use the management proxy", func(t *testing.T) {
		fixture := newRoutingFixture(t)
		fixture.fallback(true)
		fixture.proxy.connectDirectlyToCloudAPIs = true
		var connects atomic.Int32
		managementProxy := startConnectProxy(t, &connects)
		t.Setenv("HTTPS_PROXY", "http://"+managementProxy.Addr().String())
		connection, err := fixture.proxy.DialContext(t.Context(), "tcp", net.JoinHostPort("ec2.amazonaws.com", fixture.port))
		if err != nil {
			t.Fatal(err)
		}
		routingResponse(t, connection, "direct")
		connection.Close()
		if connects.Load() != 1 || fixture.guest.queries.Load() != 0 || fixture.proxy.konnectivityHealth.isHealthy() {
			t.Fatal("cloud bypass attempted recovery or ignored management proxy")
		}
		fixture.assertAttempts(t, "")
	})
	t.Run("When a cloud hostname is excluded, it should follow ordinary recovery", func(t *testing.T) {
		fixture := newRoutingFixture(t)
		fixture.fallback(true)
		fixture.proxy.connectDirectlyToCloudAPIs = true
		fixture.proxy.excludeCloudHosts = sets.New("ec2.amazonaws.com")
		connection, err := fixture.proxy.DialContext(t.Context(), "tcp", "ec2.amazonaws.com:443")
		if err != nil {
			t.Fatal(err)
		}
		connection.Close()
		fixture.assertAttempts(t, "ec2.amazonaws.com", "192.0.2.10:443")
		if fixture.guest.queries.Load() != 1 || !fixture.proxy.konnectivityHealth.isHealthy() {
			t.Fatal("excluded host bypassed recovery")
		}
	})
	t.Run("When the caller is already canceled, it should return cancellation without dialing", func(t *testing.T) {
		fixture := newRoutingFixture(t)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		connection, err := fixture.proxy.DialContext(ctx, "tcp", "idp.example.test:443")
		if connection != nil || !errors.Is(err, context.Canceled) || fixture.proxy.konnectivityHealth.activeRetry {
			t.Fatalf("canceled resolution: conn=%v err=%v", connection, err)
		}
		fixture.assertAttempts(t, "")
	})
	t.Run("When a CONNECT handshake stalls, it should honor the caller deadline", func(t *testing.T) {
		fixture := newRoutingFixture(t)
		fixture.guest.addresses = []net.IP{net.ParseIP("192.0.2.2"), net.ParseIP("192.0.2.3")}
		fixture.stall = true
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		connection, err := fixture.proxy.DialContext(ctx, "tcp", "idp.example.test:443")
		var timeout net.Error
		if connection != nil || !errors.As(err, &timeout) || !timeout.Timeout() {
			t.Fatalf("expected CONNECT timeout, got conn=%v err=%v", connection, err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
			t.Fatal("caller deadline did not expire")
		}
		if ctx.Err() != context.DeadlineExceeded {
			t.Fatalf("deadline: conn=%v err=%v context=%v", connection, err, ctx.Err())
		}
	})
	t.Run("When cancellation occurs during CONNECT, it should stop alternatives at the caller deadline", func(t *testing.T) {
		fixture := newRoutingFixture(t)
		fixture.guest.addresses = []net.IP{net.ParseIP("192.0.2.2"), net.ParseIP("192.0.2.3")}
		fixture.stall = true
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		fixture.onConnect = cancel
		connection, err := fixture.proxy.DialContext(ctx, "tcp", "idp.example.test:443")
		var timeout net.Error
		if connection != nil || !errors.As(err, &timeout) || !timeout.Timeout() || ctx.Err() != context.Canceled {
			t.Fatalf("canceled CONNECT: conn=%v err=%v context=%v", connection, err, ctx.Err())
		}
		fixture.assertAttempts(t, "idp.example.test", "192.0.2.2:443")
	})
}

func testDialContextSOCKS5(t *testing.T) {
	t.Helper()
	t.Run("When SOCKS5 resolves before dialing, it should retain recovery and context alternatives", func(t *testing.T) {
		fixture := newRoutingFixture(t)
		fixture.fallback(true)
		fixture.guest.addresses = []net.IP{net.ParseIP("192.0.2.1"), net.ParseIP("192.0.2.2")}
		dialer := routingSOCKSDialer(t, fixture.proxy)
		connection, err := dialer.DialContext(t.Context(), "tcp", "idp.example.test:443")
		if err != nil {
			t.Fatal(err)
		}
		routingResponse(t, connection, "tunnel")
		connection.Close()
		fixture.assertAttempts(t, "192.0.2.1", "192.0.2.1:443", "192.0.2.2:443")
		if fixture.guest.queries.Load() != 1 || !fixture.proxy.konnectivityHealth.isHealthy() {
			t.Fatal("SOCKS5 recovery performed duplicate resolution or failed")
		}
	})
	t.Run("When SOCKS5 guest DNS fails, it should directly dial and recover on a later request", func(t *testing.T) {
		fixture := newRoutingFixture(t)
		fixture.guest.fail.Store(true)
		dialer := routingSOCKSDialer(t, fixture.proxy)
		connection, err := dialer.DialContext(t.Context(), "tcp", net.JoinHostPort("idp.example.test", fixture.port))
		if err != nil {
			t.Fatal(err)
		}
		routingResponse(t, connection, "direct")
		connection.Close()
		if fixture.direct.Load() != 1 || fixture.proxy.konnectivityHealth.isHealthy() || fixture.proxy.konnectivityHealth.activeRetry {
			t.Fatal("SOCKS5 did not preserve management fallback")
		}
		fixture.assertAttempts(t, "")
		previousQueries := fixture.guest.queries.Load()
		fixture.guest.fail.Store(false)
		fixture.fallback(true)
		connection, err = dialer.DialContext(t.Context(), "tcp", net.JoinHostPort("idp.example.test", fixture.port))
		if err != nil {
			t.Fatal(err)
		}
		routingResponse(t, connection, "tunnel")
		connection.Close()
		if fixture.direct.Load() != 1 || !fixture.proxy.konnectivityHealth.isHealthy() || fixture.guest.queries.Load() != previousQueries+1 {
			t.Fatal("SOCKS5 did not recover on the next eligible request")
		}
		fixture.assertAttempts(t, "192.0.2.10", net.JoinHostPort("192.0.2.10", fixture.port))
	})
	t.Run("When both DNS sources fail for SOCKS5, it should retain hostname tunneling without marking failure", func(t *testing.T) {
		fixture := newRoutingFixture(t)
		fixture.guest.fail.Store(true)
		fixture.management.fail.Store(true)
		dialer := routingSOCKSDialer(t, fixture.proxy)
		connection, err := dialer.DialContext(t.Context(), "tcp", "idp.example.test:443")
		if err != nil {
			t.Fatal(err)
		}
		defer connection.Close()
		routingResponse(t, connection, "tunnel")
		fixture.assertAttempts(t, "idp.example.test", "idp.example.test:443")
		if !fixture.proxy.konnectivityHealth.isHealthy() || fixture.proxy.konnectivityHealth.activeRetry {
			t.Fatal("SOCKS5 both-DNS-fail changed health or leaked active retry")
		}
	})
}
