package konnectivitysocks5proxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/openshift/hypershift/support/konnectivityproxy"

	"k8s.io/apimachinery/pkg/util/wait"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/armon/go-socks5"
	"github.com/go-logr/logr"
)

const (
	defaultKonnectivityHost = "konnectivity-server-local"
	defaultKonnectivityPort = 8090
	konnectivityDialTimeout = 5 * time.Second
)

// coreGuardBackoffConfig holds exponential backoff parameters for konnectivity bootstrap and serve retries.
type coreGuardBackoffConfig struct {
	initialDelay time.Duration
	factor       float64
	jitter       float64
	steps        int
	cap          time.Duration
}

func (c coreGuardBackoffConfig) waitBackoff() wait.Backoff {
	return wait.Backoff{
		Duration: c.initialDelay,
		Factor:   c.factor,
		Jitter:   c.jitter,
		Steps:    c.steps,
		Cap:      c.cap,
	}
}

var (
	coreGuardBackoff = coreGuardBackoffConfig{
		initialDelay: 1 * time.Second,
		factor:       2.0,
		jitter:       0.1,
		steps:        5,
		cap:          30 * time.Second,
	}
	dialKonnectivityServer     = dialKonnectivityServerTCP
	bootstrapKonnectivityFn    = bootstrapKonnectivity
	tryBootstrapKonnectivityFn = tryBootstrapKonnectivity
	getConfigFn                = ctrl.GetConfig
	newClientFn                = client.New
)

// runWithCoreGuard keeps the process alive while konnectivity infrastructure becomes
// reachable, retrying transient failures with exponential backoff instead of exiting.
func runWithCoreGuard(ctx context.Context, log logr.Logger, opts konnectivityproxy.Options, servingPort uint32, serve func(dialer konnectivityproxy.ProxyDialer) error) error {
	for {
		delay := coreGuardBackoff.waitBackoff().DelayFunc()
		if err := ctx.Err(); err != nil {
			return err
		}

		dialer, err := bootstrapKonnectivityFn(ctx, log, opts)
		if err != nil {
			return err
		}

		log.Info("Konnectivity socks5 proxy bootstrap complete, starting server", "port", servingPort)
		if err := serve(dialer); err != nil {
			if !isTransientKonnectivityError(err) {
				return fmt.Errorf("socks5 server exited: %w", err)
			}
			log.Error(err, "socks5 server stopped due to transient error, retrying")
			sleepDuration := delay()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(sleepDuration):
				continue
			}
		}
		return nil
	}
}

// serveWithGracefulShutdown creates and runs a socks5 server with proper graceful shutdown handling.
// It returns nil on clean shutdown (context cancellation), or an error if the server fails.
func serveWithGracefulShutdown(ctx context.Context, dialer konnectivityproxy.ProxyDialer, servingPort uint32, log logr.Logger) error {
	conf := &socks5.Config{
		Dial:     dialer.DialContext,
		Resolver: dialer,
	}
	server, err := socks5.New(conf)
	if err != nil {
		return fmt.Errorf("cannot create socks5 server: %w", err)
	}

	// Create listener explicitly for graceful shutdown
	lc := &net.ListenConfig{}
	listener, err := lc.Listen(ctx, "tcp", fmt.Sprintf(":%d", servingPort))
	if err != nil {
		return fmt.Errorf("cannot create listener: %w", err)
	}

	// Run server in goroutine
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- server.Serve(listener)
	}()

	// Wait for either context cancellation or serve error
	select {
	case <-ctx.Done():
		// Graceful shutdown: close listener to stop accepting new connections
		if err := listener.Close(); err != nil {
			log.Error(err, "failed to close listener during shutdown")
		}
		// Wait for Serve to finish
		<-serveDone
		// Treat context cancellation as clean shutdown
		return nil
	case err := <-serveDone:
		// Server stopped, return the error
		return err
	}
}

func bootstrapKonnectivity(ctx context.Context, log logr.Logger, opts konnectivityproxy.Options) (konnectivityproxy.ProxyDialer, error) {
	// Create kube client once - client creation failures are permanent (missing files, bad config)
	// and should not be retried. Only konnectivity server availability is retried.
	cfg, err := getConfigFn()
	if err != nil {
		return nil, fmt.Errorf("cannot get client config: %w", err)
	}

	kubeClient, err := newClientFn(cfg, client.Options{})
	if err != nil {
		return nil, fmt.Errorf("cannot get client: %w", err)
	}

	opts.Client = kubeClient

	delay := coreGuardBackoff.waitBackoff().DelayFunc()
	attempt := 0

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		attempt++

		d, err := tryBootstrapKonnectivityFn(ctx, opts)
		if err == nil {
			return d, nil
		}
		if !isTransientKonnectivityError(err) {
			return nil, fmt.Errorf("konnectivity bootstrap failed: %w", err)
		}

		log.Error(err, "transient konnectivity bootstrap failure, retrying", "attempt", attempt)
		sleepDuration := delay()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(sleepDuration):
			// Continue retry loop
		}
	}
}

func tryBootstrapKonnectivity(ctx context.Context, opts konnectivityproxy.Options) (konnectivityproxy.ProxyDialer, error) {
	dialer, err := konnectivityproxy.NewKonnectivityDialer(opts)
	if err != nil {
		return nil, fmt.Errorf("cannot initialize konnectivity dialer: %w", err)
	}

	host := opts.KonnectivityHost
	if host == "" {
		host = defaultKonnectivityHost
	}
	port := opts.KonnectivityPort
	if port == 0 {
		port = defaultKonnectivityPort
	}

	if err := dialKonnectivityServer(ctx, host, port); err != nil {
		return nil, fmt.Errorf("cannot dial konnectivity server at %s:%d: %w", host, port, err)
	}

	return dialer, nil
}

func dialKonnectivityServerTCP(ctx context.Context, host string, port uint32) error {
	address := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	conn, err := (&net.Dialer{Timeout: konnectivityDialTimeout}).DialContext(ctx, "tcp", address)
	if err != nil {
		return err
	}
	_ = conn.Close()
	return nil
}

func isTransientKonnectivityError(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}

	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if isTransientKonnectivityError(opErr.Err) {
			return true
		}
	}

	var syscallErr syscall.Errno
	if errors.As(err, &syscallErr) {
		switch syscallErr {
		case syscall.ECONNREFUSED, syscall.ECONNRESET, syscall.ETIMEDOUT,
			syscall.EHOSTUNREACH, syscall.ENETUNREACH, syscall.EPIPE:
			return true
		}
	}

	// Configuration errors such as missing certificate files should fail fast, not retry.
	if errors.Is(err, os.ErrNotExist) {
		return false
	}

	msg := err.Error()
	transientFragments := []string{
		"connection refused",
		"connection reset",
		"i/o timeout",
		"context deadline exceeded",
		"no route to host",
		"network is unreachable",
		"operation timed out",
		"TLS handshake timeout",
	}
	for _, fragment := range transientFragments {
		if strings.Contains(msg, fragment) {
			return true
		}
	}

	return false
}
