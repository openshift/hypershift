package konnectivitysocks5proxy

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	"github.com/go-logr/logr"
)

func TestNewStartCommand(t *testing.T) {
	t.Run("When creating start command, it should have correct structure", func(t *testing.T) {
		g := NewGomegaWithT(t)

		cmd := NewStartCommand()

		g.Expect(cmd).ToNot(BeNil())
		g.Expect(cmd.Use).To(Equal("konnectivity-socks5-proxy"))
		g.Expect(cmd.Short).To(ContainSubstring("konnectivity socks5 proxy"))
		g.Expect(cmd.Run).ToNot(BeNil())
	})

	t.Run("When command has flags, it should have all required flags", func(t *testing.T) {
		g := NewGomegaWithT(t)

		cmd := NewStartCommand()

		// Check that all expected flags are present
		flags := []string{
			"konnectivity-hostname",
			"konnectivity-port",
			"serving-port",
			"connect-directly-to-cloud-apis",
			"resolve-from-guest-cluster-dns",
			"resolve-from-management-cluster-dns",
			"disable-resolver",
			"ca-cert-path",
			"tls-cert-path",
			"tls-key-path",
		}

		for _, flagName := range flags {
			flag := cmd.Flags().Lookup(flagName)
			g.Expect(flag).ToNot(BeNil(), "Flag %s should exist", flagName)
		}
	})

	t.Run("When checking flag defaults, they should be correct", func(t *testing.T) {
		g := NewGomegaWithT(t)

		cmd := NewStartCommand()

		// Check default values
		hostnameFlag := cmd.Flags().Lookup("konnectivity-hostname")
		g.Expect(hostnameFlag.DefValue).To(Equal("konnectivity-server-local"))

		konnectivityPortFlag := cmd.Flags().Lookup("konnectivity-port")
		g.Expect(konnectivityPortFlag.DefValue).To(Equal("8090"))

		servingPortFlag := cmd.Flags().Lookup("serving-port")
		g.Expect(servingPortFlag.DefValue).To(Equal("8090"))

		caPathFlag := cmd.Flags().Lookup("ca-cert-path")
		g.Expect(caPathFlag.DefValue).To(Equal("/etc/konnectivity/proxy-ca/ca.crt"))

		tlsCertFlag := cmd.Flags().Lookup("tls-cert-path")
		g.Expect(tlsCertFlag.DefValue).To(Equal("/etc/konnectivity/proxy-client/tls.crt"))

		tlsKeyFlag := cmd.Flags().Lookup("tls-key-path")
		g.Expect(tlsKeyFlag.DefValue).To(Equal("/etc/konnectivity/proxy-client/tls.key"))
	})

	t.Run("When checking boolean flag defaults, they should be false", func(t *testing.T) {
		g := NewGomegaWithT(t)

		cmd := NewStartCommand()

		boolFlags := []string{
			"connect-directly-to-cloud-apis",
			"resolve-from-guest-cluster-dns",
			"resolve-from-management-cluster-dns",
			"disable-resolver",
		}

		for _, flagName := range boolFlags {
			flag := cmd.Flags().Lookup(flagName)
			g.Expect(flag.DefValue).To(Equal("false"), "Flag %s should default to false", flagName)
		}
	})

	t.Run("When validating graceful shutdown behavior in serve function", func(t *testing.T) {
		g := NewGomegaWithT(t)

		// This test verifies that serveWithGracefulShutdown respects context cancellation
		// by checking that it returns nil (clean shutdown) when context is canceled

		// Create a mock dialer (minimal implementation)
		mockDialer := &mockProxyDialer{}

		// Test context cancellation leads to clean shutdown
		ctx, cancel := context.WithCancel(context.Background())

		// Start the server in a goroutine
		errChan := make(chan error, 1)
		go func() {
			errChan <- serveWithGracefulShutdown(ctx, mockDialer, 0, logr.Discard()) // Port 0 = random available port
		}()

		// Give server time to start
		time.Sleep(50 * time.Millisecond)

		// Cancel context (simulating SIGTERM)
		cancel()

		// Server should shut down cleanly (return nil)
		select {
		case err := <-errChan:
			g.Expect(err).To(BeNil(), "Context cancellation should result in clean shutdown (nil error)")
		case <-time.After(1 * time.Second):
			g.Fail("Server did not shut down within timeout")
		}
	})
}
