package konnectivityproxy

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/util/sets"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestValidate(t *testing.T) {

	tests := []struct {
		name        string
		o           Options
		expectValid bool
	}{
		{
			name: "valid options",
			o: Options{
				CAFile:           "test-ca",
				ClientCertBytes:  []byte("test-cert"),
				ClientKeyFile:    "test-key-name",
				KonnectivityHost: "example.org",
				KonnectivityPort: 123,
				Client:           fake.NewFakeClient(),
			},
			expectValid: true,
		},
		{
			name: "missing CA",
			o: Options{
				ClientCertBytes:  []byte("test-cert"),
				ClientKeyFile:    "test-key-name",
				KonnectivityHost: "example.org",
				KonnectivityPort: 123,
				Client:           fake.NewFakeClient(),
			},
			expectValid: false,
		},
		{
			name: "missing KonnectivityPort",
			o: Options{
				CABytes:          []byte("test-ca"),
				ClientCertBytes:  []byte("test-cert"),
				ClientKeyFile:    "test-key-name",
				KonnectivityHost: "example.org",
				Client:           fake.NewFakeClient(),
			},
			expectValid: false,
		},
		{
			name: "client cert file and bytes",
			o: Options{
				CAFile:           "test-ca",
				ClientCertFile:   "test-cert-file",
				ClientCertBytes:  []byte("test-cert"),
				ClientKeyFile:    "test-key-name",
				KonnectivityHost: "example.org",
				KonnectivityPort: 123,
				Client:           fake.NewFakeClient(),
			},
			expectValid: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.o.Validate()
			if test.expectValid && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if !test.expectValid && err == nil {
				t.Errorf("did not get expected error")
			}
		})
	}
}

func TestKonnectivityHealth(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(*konnectivityHealth)
		action   func(*konnectivityHealth) bool
		expected bool
	}{
		{
			name:     "When healthy it should allow retry",
			setup:    func(kh *konnectivityHealth) {},
			action:   func(kh *konnectivityHealth) bool { return kh.beginRetry() },
			expected: true,
		},
		{
			name: "When in fallback and too soon it should not retry",
			setup: func(kh *konnectivityHealth) {
				kh.markFailure()
			},
			action:   func(kh *konnectivityHealth) bool { return kh.beginRetry() },
			expected: false,
		},
		{
			name: "When in fallback and enough time passed it should retry",
			setup: func(kh *konnectivityHealth) {
				kh.markFailure()
				// Set lastRetryTime to past
				kh.lastRetryTime = time.Now().Add(-31 * time.Second)
			},
			action:   func(kh *konnectivityHealth) bool { return kh.beginRetry() },
			expected: true,
		},
		{
			name: "When another retry is active it should not retry",
			setup: func(kh *konnectivityHealth) {
				kh.markFailure()
				kh.lastRetryTime = time.Now().Add(-31 * time.Second)
				kh.beginRetry() // Start first retry
			},
			action:   func(kh *konnectivityHealth) bool { return kh.beginRetry() },
			expected: false,
		},
		{
			name: "After success it should be healthy",
			setup: func(kh *konnectivityHealth) {
				kh.markFailure()
				kh.markSuccess()
			},
			action:   func(kh *konnectivityHealth) bool { return kh.isHealthy() },
			expected: true,
		},
		{
			name: "After failure it should be unhealthy",
			setup: func(kh *konnectivityHealth) {
				kh.markFailure()
			},
			action:   func(kh *konnectivityHealth) bool { return kh.isHealthy() },
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kh := newKonnectivityHealth()
			tt.setup(kh)
			got := tt.action(kh)
			if got != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, got)
			}
		})
	}
}

func TestKonnectivityHealthRecovery(t *testing.T) {
	kh := newKonnectivityHealth()

	// Initially healthy
	if !kh.isHealthy() {
		t.Error("expected konnectivityHealth to be initially healthy")
	}

	// First failure triggers fallback
	kh.markFailure()
	if kh.isHealthy() {
		t.Error("expected konnectivityHealth to be unhealthy after failure")
	}

	// Immediate retry should be blocked
	if kh.beginRetry() {
		t.Error("expected immediate retry to be blocked")
	}

	// Fast-forward time
	kh.lastRetryTime = time.Now().Add(-31 * time.Second)

	// Now retry should be allowed
	if !kh.beginRetry() {
		t.Error("expected retry to be allowed after interval")
	}

	// Multiple simultaneous retries should be blocked
	if kh.beginRetry() {
		t.Error("expected simultaneous retry to be blocked")
	}

	// Successful resolution should restore health
	kh.markSuccess()
	if !kh.isHealthy() {
		t.Error("expected konnectivityHealth to be healthy after success")
	}

	// Should allow immediate retry when healthy
	if !kh.beginRetry() {
		t.Error("expected retry to be allowed when healthy")
	}
}

func TestKonnectivityHealthEndRetry(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(*konnectivityHealth)
		expectRetry bool
	}{
		{
			name: "When endRetry is called it should clear activeRetry flag",
			setup: func(kh *konnectivityHealth) {
				kh.markFailure()
				kh.lastRetryTime = time.Now().Add(-31 * time.Second)
				kh.beginRetry() // Set activeRetry to true
				kh.endRetry()   // Clear activeRetry
				// Reset time to allow another retry (since beginRetry updates lastRetryTime)
				kh.lastRetryTime = time.Now().Add(-31 * time.Second)
			},
			expectRetry: true, // Should allow retry since activeRetry was cleared
		},
		{
			name: "When endRetry is called after beginRetry it should allow subsequent retries",
			setup: func(kh *konnectivityHealth) {
				kh.markFailure()
				kh.lastRetryTime = time.Now().Add(-31 * time.Second)
				kh.beginRetry() // Start retry
				kh.endRetry()   // End retry
				// Reset time to allow another retry
				kh.lastRetryTime = time.Now().Add(-31 * time.Second)
			},
			expectRetry: true, // Should allow new retry after endRetry was called
		},
		{
			name: "When multiple endRetry calls it should remain safe",
			setup: func(kh *konnectivityHealth) {
				kh.markFailure()
				kh.lastRetryTime = time.Now().Add(-31 * time.Second)
				kh.beginRetry()
				kh.endRetry() // First call
				kh.endRetry() // Second call - should be safe
				kh.lastRetryTime = time.Now().Add(-31 * time.Second)
			},
			expectRetry: true, // Should still work after multiple endRetry calls
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kh := newKonnectivityHealth()
			tt.setup(kh)
			got := kh.beginRetry()
			if got != tt.expectRetry {
				t.Errorf("expected beginRetry() = %v, got %v", tt.expectRetry, got)
			}
		})
	}
}

func TestKonnectivityHealthEndRetryPreventsStubbornFlag(t *testing.T) {
	kh := newKonnectivityHealth()

	// Simulate the original bug scenario that was fixed
	kh.markFailure()
	kh.lastRetryTime = time.Now().Add(-31 * time.Second)

	if !kh.beginRetry() {
		t.Fatal("expected beginRetry to succeed")
	}

	// Before the fix, if guest DNS and management DNS both failed,
	// activeRetry would remain true, blocking future retries.
	// With the fix, endRetry() ensures activeRetry is cleared.
	kh.endRetry()

	// Fast-forward time and ensure a new retry can begin
	kh.lastRetryTime = time.Now().Add(-31 * time.Second)
	if !kh.beginRetry() {
		t.Error("expected retry to be allowed after endRetry and interval")
	}
}

// startTCPEchoServer starts a TCP server that echoes back anything it receives.
// All accepted connections inherit a deadline so io.Copy never blocks indefinitely.
func startTCPEchoServer(t *testing.T) net.Listener {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start echo server: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
					return
				}
				if _, err := io.Copy(conn, conn); err != nil {
					return
				}
			}()
		}
	}()
	return ln
}

// startConnectProxy starts an HTTP CONNECT proxy that increments connectCount
// for every successful tunnel. Relay goroutines are bounded by per-connection
// deadlines so they cannot outlive the test.
func startConnectProxy(t *testing.T, connectCount *atomic.Int32) net.Listener {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start proxy server: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodConnect {
				http.Error(w, "only CONNECT supported", http.StatusMethodNotAllowed)
				return
			}
			connectCount.Add(1)

			target, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(r.Context(), "tcp", r.Host)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			defer target.Close()
			if err := target.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			w.WriteHeader(http.StatusOK)
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				http.Error(w, "hijack not supported", http.StatusInternalServerError)
				return
			}
			client, _, err := hijacker.Hijack()
			if err != nil {
				return
			}
			defer client.Close()
			if err := client.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
				return
			}

			done := make(chan struct{}, 2)
			relay := func(dst, src net.Conn) {
				io.Copy(dst, src) //nolint:errcheck // relay best-effort; deadline bounds lifetime
				done <- struct{}{}
			}
			go relay(target, client)
			go relay(client, target)
			<-done
		}),
	}
	t.Cleanup(func() { srv.Close() })
	go func() { _ = srv.Serve(ln) }()
	return ln
}

func TestDialDirectWithProxy(t *testing.T) {
	const testTimeout = 5 * time.Second

	t.Run("When HTTPS_PROXY is set it should route through the proxy", func(t *testing.T) {
		g := NewGomegaWithT(t)
		t.Setenv("HTTP_PROXY", "")
		t.Setenv("HTTPS_PROXY", "")
		t.Setenv("NO_PROXY", "")
		echo := startTCPEchoServer(t)
		var connectCount atomic.Int32
		proxyLn := startConnectProxy(t, &connectCount)
		t.Setenv("HTTPS_PROXY", fmt.Sprintf("http://%s", proxyLn.Addr().String()))

		p := &konnectivityProxy{}
		conn, err := p.dialDirectWithProxy("tcp", echo.Addr().String())
		g.Expect(err).NotTo(HaveOccurred(), "dialDirectWithProxy should succeed")
		defer conn.Close()
		g.Expect(conn.SetDeadline(time.Now().Add(testTimeout))).To(Succeed())

		msg := []byte("hello")
		_, err = conn.Write(msg)
		g.Expect(err).NotTo(HaveOccurred(), "write should succeed")
		buf := make([]byte, len(msg))
		_, err = io.ReadFull(conn, buf)
		g.Expect(err).NotTo(HaveOccurred(), "read should succeed")
		g.Expect(string(buf)).To(Equal(string(msg)))
		g.Expect(connectCount.Load()).To(Equal(int32(1)), "proxy should receive 1 CONNECT request")
	})

	t.Run("When HTTPS_PROXY is not set it should connect directly", func(t *testing.T) {
		g := NewGomegaWithT(t)
		echo := startTCPEchoServer(t)
		var connectCount atomic.Int32
		startConnectProxy(t, &connectCount)
		t.Setenv("HTTPS_PROXY", "")
		t.Setenv("HTTP_PROXY", "")
		t.Setenv("NO_PROXY", "")

		p := &konnectivityProxy{}
		conn, err := p.dialDirectWithProxy("tcp", echo.Addr().String())
		g.Expect(err).NotTo(HaveOccurred(), "dialDirectWithProxy should succeed")
		defer conn.Close()
		g.Expect(conn.SetDeadline(time.Now().Add(testTimeout))).To(Succeed())

		msg := []byte("hello")
		_, err = conn.Write(msg)
		g.Expect(err).NotTo(HaveOccurred(), "write should succeed")
		buf := make([]byte, len(msg))
		_, err = io.ReadFull(conn, buf)
		g.Expect(err).NotTo(HaveOccurred(), "read should succeed")
		g.Expect(string(buf)).To(Equal(string(msg)))
		g.Expect(connectCount.Load()).To(Equal(int32(0)), "proxy should receive 0 CONNECT requests")
	})
}

func TestIsCloudAPI(t *testing.T) {
	tests := []struct {
		name        string
		host        string
		expected    bool
		description string
	}{
		// Valid cloud API hosts
		{
			name:        "When host is valid AWS API it should return true",
			host:        "ec2.amazonaws.com",
			expected:    true,
			description: "AWS API endpoints should be detected",
		},
		{
			name:        "When host is valid Azure API it should return true",
			host:        "management.azure.com",
			expected:    true,
			description: "Azure API endpoints should be detected",
		},
		{
			name:        "When host is valid Microsoft API it should return true",
			host:        "login.microsoftonline.com",
			expected:    true,
			description: "Microsoft API endpoints should be detected",
		},
		{
			name:        "When host is valid IBM API it should return true",
			host:        "iam.cloud.ibm.com",
			expected:    true,
			description: "IBM Cloud API endpoints should be detected",
		},

		// Valid AWS ISO cloud API hosts
		{
			name:        "When host is valid AWS ISO C2S API it should return true",
			host:        "s3.c2s.ic.gov",
			expected:    true,
			description: "AWS ISO C2S endpoints should be detected",
		},
		{
			name:        "When host is valid AWS ISO HCI API it should return true",
			host:        "iam.hci.ic.gov",
			expected:    true,
			description: "AWS ISO HCI endpoints should be detected",
		},
		{
			name:        "When host is valid AWS ISO-B SC2S API it should return true",
			host:        "s3.sc2s.sgov.gov",
			expected:    true,
			description: "AWS ISO-B SC2S endpoints should be detected",
		},

		// False positive scenarios that were fixed
		{
			name:        "When host contains azure.com but is not azure.com it should return false",
			host:        "notazure.com",
			expected:    false,
			description: "False positive: hosts ending with azure.com but not actually Azure",
		},
		{
			name:        "When host contains cloud.ibm.com but is not IBM it should return false",
			host:        "fakecloud.ibm.com",
			expected:    false,
			description: "False positive: hosts ending with cloud.ibm.com but not actually IBM",
		},
		{
			name:        "When host is malicious azure lookalike it should return false",
			host:        "evilazure.com",
			expected:    false,
			description: "Malicious hosts trying to mimic Azure should not be detected as cloud API",
		},
		{
			name:        "When host is malicious IBM lookalike it should return false",
			host:        "badcloud.ibm.com",
			expected:    false,
			description: "Malicious hosts trying to mimic IBM should not be detected as cloud API",
		},

		// Edge cases
		{
			name:        "When host is exactly azure.com it should return false",
			host:        "azure.com",
			expected:    false,
			description: "Bare azure.com without subdomain should not be cloud API",
		},
		{
			name:        "When host is exactly cloud.ibm.com it should return false",
			host:        "cloud.ibm.com",
			expected:    false,
			description: "Bare cloud.ibm.com without subdomain should not be cloud API",
		},

		// Non-cloud hosts
		{
			name:        "When host is not cloud API it should return false",
			host:        "example.com",
			expected:    false,
			description: "Regular hosts should not be detected as cloud API",
		},
		{
			name:        "When host is empty it should return false",
			host:        "",
			expected:    false,
			description: "Empty host should not be detected as cloud API",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a proxy with minimal config for testing
			proxy := &konnectivityProxy{
				connectDirectlyToCloudAPIs: true, // Enable cloud API detection
				excludeCloudHosts:          sets.New[string](),
			}

			got := proxy.IsCloudAPI(tt.host)
			if got != tt.expected {
				t.Errorf("IsCloudAPI(%q) = %v, expected %v - %s", tt.host, got, tt.expected, tt.description)
			}
		})
	}
}
