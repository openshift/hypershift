package konnectivityproxy

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/go-logr/logr"
)

func TestKonnectivityProxy_DialContext(t *testing.T) {
	// No test in this package runs in parallel. Restore the process resolver after
	// the test; all DNS responses here come from an in-memory connection.
	previousResolver := net.DefaultResolver
	net.DefaultResolver = recoveryDNSResolver(nil, nil)
	t.Cleanup(func() { net.DefaultResolver = previousResolver })

	t.Run("When guest DNS is available, it should resolve and connect through the tunnel", func(t *testing.T) {
		f := newRecoveryFixture(t)
		f.guestAvailable.Store(true)
		f.expectDial(net.JoinHostPort("idp.test", f.targetPort), true)
		if f.guestAttempts.Load() == 0 || !f.p.konnectivityHealth.isHealthy() {
			t.Error("expected successful guest DNS resolution and healthy tunnel")
		}
	})

	t.Run("When guest DNS fails then recovers, it should enter fallback immediately and recover after the retry interval", func(t *testing.T) {
		f := newRecoveryFixture(t)
		address := net.JoinHostPort("idp.test", f.targetPort)
		f.expectDial(address, false)
		if f.p.konnectivityHealth.isHealthy() {
			t.Error("guest DNS failure with successful management DNS must enter fallback")
		}
		guestAttempts := f.guestAttempts.Load()
		f.guestAvailable.Store(true)
		f.expectDial(address, false)
		if f.guestAttempts.Load() != guestAttempts {
			t.Error("guest DNS must not be retried before the interval")
		}
		f.allowRetry()
		f.expectDial(address, true)
		if !f.p.konnectivityHealth.isHealthy() {
			t.Error("successful guest DNS retry must clear fallback")
		}
		if f.guestAttempts.Load() <= guestAttempts {
			t.Error("expected a guest DNS recovery attempt")
		}
		f.expectDial(address, true)
	})

	t.Run("When a literal IP is dialed in fallback, it should debounce failed retries and recover through the tunnel", func(t *testing.T) {
		f := newRecoveryFixture(t)
		f.enterFallback()
		f.tunnelAvailable.Store(false)
		f.expectDial(f.targetAddress, false)
		if f.tunnelAttempts.Load() != 0 {
			t.Error("must not probe the tunnel before retry is due")
		}
		f.allowRetry()
		f.expectDial(f.targetAddress, false)
		if f.tunnelAttempts.Load() != 1 {
			t.Error("expected one failed tunnel retry")
		}
		if f.p.konnectivityHealth.isHealthy() {
			t.Error("failed tunnel retry must preserve fallback")
		}
		attempts := f.tunnelAttempts.Load()
		f.expectDial(f.targetAddress, false)
		if f.tunnelAttempts.Load() != attempts {
			t.Error("failed retry must be debounced")
		}
		f.tunnelAvailable.Store(true)
		f.allowRetry()
		f.expectDial(f.targetAddress, true)
		if !f.p.konnectivityHealth.isHealthy() {
			t.Error("successful tunnel retry must clear fallback")
		}
		f.expectDial(f.targetAddress, true)
	})

	t.Run("When an IP addressed upstream proxy is configured, it should recover without resolving the destination locally", runUpstreamProxyRecovery)

	t.Run("When a recovery CONNECT stalls, it should fall back after the five second recovery timeout", func(t *testing.T) {
		f := newRecoveryFixture(t)
		f.enterFallback()
		f.allowRetry()
		f.stallTunnel.Store(true)
		ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
		defer cancel()
		started := time.Now()
		conn, err := f.p.DialContext(ctx, "tcp", f.targetAddress)
		elapsed := time.Since(started)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if _, tunneled := conn.(*tls.Conn); tunneled {
			t.Error("stalled recovery must return a management connection")
		}
		if f.tunnelAttempts.Load() != 1 {
			t.Error("expected one recovery attempt")
		}
		if elapsed < 4*time.Second || elapsed > 7*time.Second {
			t.Errorf("recovery took %v, expected approximately five seconds", elapsed)
		}
		if f.p.konnectivityHealth.isHealthy() {
			t.Error("recovery timeout must preserve fallback")
		}
	})

	t.Run("When TLS setup panics during recovery, it should release the retry gate", runTLSSetupPanicRecovery)

	t.Run("When another recovery attempt is active, it should continue management fallback", func(t *testing.T) {
		f := newRecoveryFixture(t)
		f.enterFallback()
		f.allowRetry()
		if !f.p.konnectivityHealth.beginRetry() {
			t.Fatal("could not reserve recovery attempt")
		}
		defer f.p.konnectivityHealth.endRetry()
		f.expectDial(f.targetAddress, false)
		if f.tunnelAttempts.Load() != 0 {
			t.Error("must not start a concurrent tunnel recovery attempt")
		}
	})

	t.Run("When guest DNS is dialed during fallback, it should always use the tunnel", func(t *testing.T) {
		f := newRecoveryFixture(t)
		f.enterFallback()
		f.expectDial("192.0.2.53:53", true)
		if f.tunnelAttempts.Load() != 1 {
			t.Error("guest DNS connection must use the tunnel even before retry is due")
		}
	})

	t.Run("When a cloud API is dialed during fallback, it should retain management proxy bypass", runCloudAPIBypass)
}

func runUpstreamProxyRecovery(t *testing.T) {
	f := newRecoveryFixture(t)
	f.enterFallback()
	f.tunnelAvailable.Store(false)
	var proxyRequests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyRequests.Add(1)
		if r.URL.Host != "idp.test" {
			t.Errorf("unexpected upstream destination: %s", r.URL)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)
	proxyURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL), DialContext: f.p.DialContext, DisableKeepAlives: true}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	request := func() {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://idp.test/resource", nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusNoContent {
			t.Errorf("unexpected status: %s", response.Status)
		}
	}
	request()
	if f.tunnelAttempts.Load() != 0 {
		t.Error("must initially use management fallback")
	}
	f.allowRetry()
	request()
	if f.tunnelAttempts.Load() != 1 {
		t.Error("expected a failed tunnel retry to the upstream proxy")
	}
	request()
	if f.tunnelAttempts.Load() != 1 {
		t.Error("must debounce the next upstream proxy retry")
	}
	f.tunnelAvailable.Store(true)
	f.allowRetry()
	request()
	if !f.p.konnectivityHealth.isHealthy() {
		t.Error("upstream proxy tunnel success must clear fallback")
	}
	select {
	case address := <-f.tunnelAddresses:
		if address != proxyURL.Host {
			t.Errorf("tunneled to %s, expected upstream proxy %s", address, proxyURL.Host)
		}
	default:
		t.Error("expected a connection through the tunnel to the upstream proxy")
	}
	if proxyRequests.Load() != 4 {
		t.Errorf("upstream received %d requests, expected 4", proxyRequests.Load())
	}
}

func runTLSSetupPanicRecovery(t *testing.T) {
	f := newRecoveryFixture(t)
	f.enterFallback()
	f.allowRetry()
	// Use a fresh TLS configuration so invalid CA data exercises the real
	// setup panic without changing the fixture's initialized sync.Once.
	p := &konnectivityProxy{
		proxyResolver:                   f.p.proxyResolver,
		resolveFromManagementClusterDNS: true,
		ca:                              []byte("invalid CA"),
	}
	func() {
		defer func() {
			if got := recover(); got != "cannot load client CA" {
				t.Errorf("expected TLS setup panic, got %v", got)
			}
		}()
		conn, _ := p.DialContext(t.Context(), "tcp", f.targetAddress)
		if conn != nil {
			conn.Close()
		}
	}()
	if p.konnectivityHealth.isHealthy() {
		t.Error("TLS setup panic must preserve fallback")
	}
	f.allowRetry()
	if !p.konnectivityHealth.beginRetry() {
		t.Fatal("TLS setup panic left the retry gate blocked")
	}
	p.konnectivityHealth.endRetry()
}

func runCloudAPIBypass(t *testing.T) {
	f := newRecoveryFixture(t)
	f.enterFallback()
	f.allowRetry()
	f.p.connectDirectlyToCloudAPIs = true
	var cloudRequests atomic.Int32
	managementProxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cloudRequests.Add(1)
		if r.Method != http.MethodConnect || r.Host != "ec2.amazonaws.com:443" {
			t.Errorf("unexpected cloud CONNECT: %s %s", r.Method, r.Host)
		}
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_, _ = rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		_ = rw.Flush()
	}))
	t.Cleanup(managementProxy.Close)
	t.Setenv("HTTPS_PROXY", managementProxy.URL)
	guestAttempts := f.guestAttempts.Load()
	f.expectDial("ec2.amazonaws.com:443", false)
	if cloudRequests.Load() != 1 {
		t.Error("cloud API must use the management HTTPS proxy")
	}
	if f.tunnelAttempts.Load() != 0 || f.guestAttempts.Load() != guestAttempts {
		t.Error("cloud bypass must not probe the tunnel or guest DNS")
	}
	if f.p.konnectivityHealth.isHealthy() {
		t.Error("cloud bypass must not clear tunnel fallback")
	}
}

type recoveryFixture struct {
	t               *testing.T
	p               *konnectivityProxy
	guestAvailable  atomic.Bool
	guestAttempts   atomic.Int32
	stallTunnel     atomic.Bool
	tunnelAvailable atomic.Bool
	tunnelAttempts  atomic.Int32
	tunnelAddresses chan string
	targetAddress   string
	targetPort      string
}

func newRecoveryFixture(t *testing.T) *recoveryFixture {
	t.Helper()
	f := &recoveryFixture{t: t, tunnelAddresses: make(chan string, 16)}
	f.tunnelAvailable.Store(true)
	target := startTCPEchoServer(t)
	f.targetAddress = target.Addr().String()
	_, f.targetPort, _ = net.SplitHostPort(f.targetAddress)
	tunnel := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.tunnelAttempts.Add(1)
		if r.Method != http.MethodConnect {
			http.Error(w, "CONNECT required", http.StatusMethodNotAllowed)
			return
		}
		if !f.tunnelAvailable.Load() {
			http.Error(w, "workers unavailable", http.StatusServiceUnavailable)
			return
		}
		if f.stallTunnel.Load() {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
			_, _ = io.Copy(io.Discard, conn)
			return
		}
		_, port, err := net.SplitHostPort(r.Host)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// Port 53 is only used to verify routing. Never dial an actual DNS service.
		var targetConn net.Conn
		if port != "53" {
			targetConn, err = (&net.Dialer{Timeout: time.Second}).DialContext(r.Context(), "tcp", r.Host)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			defer targetConn.Close()
		}
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		if _, err := rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			return
		}
		f.tunnelAddresses <- r.Host
		if err := rw.Flush(); err != nil {
			return
		}
		if targetConn == nil {
			_, _ = io.Copy(io.Discard, rw)
			return
		}
		done := make(chan struct{})
		go func() { _, _ = io.Copy(targetConn, rw); _ = targetConn.Close(); close(done) }()
		_, _ = io.Copy(conn, targetConn)
		_ = conn.Close()
		<-done
	}))
	t.Cleanup(tunnel.Close)
	host, port, err := net.SplitHostPort(tunnel.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	portNumber, err := strconv.ParseUint(port, 10, 32)
	if err != nil {
		t.Fatal(err)
	}
	p := &konnectivityProxy{konnectivityHost: host, konnectivityPort: uint32(portNumber), resolveBeforeDial: true, resolveFromManagementClusterDNS: true}
	p.proxyResolver = proxyResolver{
		client: fake.NewClientBuilder().Build(), resolveFromGuestCluster: true,
		resolveFromManagementCluster: true, mustResolve: true,
		konnectivityHealth: newKonnectivityHealth(), log: logr.Discard(), isCloudAPI: p.IsCloudAPI,
		guestClusterResolver: &guestClusterResolver{resolver: recoveryDNSResolver(&f.guestAvailable, &f.guestAttempts)},
	}
	// Use the local test server's trusted certificate without requiring certificate files.
	p.tlsConfigOnce.Do(func() { p.tlsConfig = tunnel.Client().Transport.(*http.Transport).TLSClientConfig.Clone() })
	f.p = p
	return f
}

func (f *recoveryFixture) enterFallback() {
	f.t.Helper()
	_, _, err := f.p.Resolve(f.t.Context(), "idp.test")
	if err != nil {
		f.t.Fatal(err)
	}
	if f.p.konnectivityHealth.isHealthy() {
		f.t.Fatal("expected guest DNS failure and successful management DNS to enter fallback")
	}
}

func (f *recoveryFixture) allowRetry() {
	f.p.konnectivityHealth.mutex.Lock()
	f.p.konnectivityHealth.lastRetryTime = time.Now().Add(-time.Minute)
	f.p.konnectivityHealth.mutex.Unlock()
}

func (f *recoveryFixture) expectDial(address string, wantTunnel bool) {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(f.t.Context(), 3*time.Second)
	defer cancel()
	conn, err := f.p.DialContext(ctx, "tcp", address)
	if err != nil {
		f.t.Errorf("DialContext(%s): %v", address, err)
		return
	}
	defer conn.Close()
	_, throughTunnel := conn.(*tls.Conn)
	if throughTunnel != wantTunnel {
		f.t.Errorf("DialContext(%s) used tunnel=%v, expected %v", address, throughTunnel, wantTunnel)
	}
}

// recoveryDNSResolver answers A queries with loopback and AAAA queries with no
// records. net.Pipe uses DNS-over-TCP framing, with no sockets or external DNS.
func recoveryDNSResolver(available *atomic.Bool, attempts *atomic.Int32) *net.Resolver {
	return &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		if attempts != nil {
			attempts.Add(1)
		}
		if available != nil && !available.Load() {
			return nil, fmt.Errorf("guest DNS unavailable")
		}
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			_ = server.SetDeadline(time.Now().Add(3 * time.Second))
			var length [2]byte
			if _, err := io.ReadFull(server, length[:]); err != nil {
				return
			}
			query := make([]byte, int(binary.BigEndian.Uint16(length[:])))
			if _, err := io.ReadFull(server, query); err != nil || len(query) < 16 {
				return
			}
			response := append([]byte(nil), query...)
			binary.BigEndian.PutUint16(response[2:4], 0x8180)
			binary.BigEndian.PutUint16(response[6:8], 0)
			binary.BigEndian.PutUint16(response[8:10], 0)
			binary.BigEndian.PutUint16(response[10:12], 0)
			// Skip the question name to find QTYPE. Ignore any EDNS additional record.
			end := 12
			for end < len(query) && query[end] != 0 {
				end += 1 + int(query[end])
			}
			end++
			if end+4 > len(query) {
				return
			}
			response = response[:end+4]
			if binary.BigEndian.Uint16(query[end:end+2]) == 1 {
				binary.BigEndian.PutUint16(response[6:8], 1)
				response = append(response, 0xc0, 0x0c, 0, 1, 0, 1, 0, 0, 0, 0, 0, 4, 127, 0, 0, 1)
			}
			binary.BigEndian.PutUint16(length[:], uint16(len(response)))
			_, _ = server.Write(append(length[:], response...))
		}()
		return client, nil
	}}
}
