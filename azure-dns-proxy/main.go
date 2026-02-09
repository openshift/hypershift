package azurednsproxy

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/go-logr/logr"
	"github.com/openshift/hypershift/support/supportedversion"
	"github.com/spf13/cobra"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"go.uber.org/zap/zapcore"
)

const (
	azureDNSServer = "168.63.129.16:53"
)

func NewStartCommand() *cobra.Command {
	l := log.Log.WithName("azure-dns-proxy")
	log.SetLogger(zap.New(zap.JSONEncoder(func(o *zapcore.EncoderConfig) {
		o.EncodeTime = zapcore.RFC3339TimeEncoder
	})))

	cmd := &cobra.Command{
		Use:   "azure-dns-proxy",
		Short: "Runs the Azure DNS HTTP CONNECT proxy server.",
	}

	var listenAddr string
	var requestTimeout time.Duration
	var connectTimeout time.Duration
	var tunnelIdleTimeout time.Duration

	cmd.Flags().StringVar(&listenAddr, "listen-addr", "0.0.0.0:8888", "Address to listen on for HTTP CONNECT requests")
	cmd.Flags().DurationVar(&requestTimeout, "request-timeout", 30*time.Second, "Timeout for proxy request handling")
	cmd.Flags().DurationVar(&connectTimeout, "connect-timeout", 10*time.Second, "Timeout for establishing connections to target hosts")
	cmd.Flags().DurationVar(&tunnelIdleTimeout, "tunnel-idle-timeout", 5*time.Minute, "Idle timeout for connection tunnels")

	cmd.Run = func(cmd *cobra.Command, args []string) {
		l.Info("Starting Azure DNS HTTP CONNECT proxy", "version", supportedversion.String())

		azureDomains := []string{
			".vault.azure.net",
			".vaultcore.azure.net",
			".privatelink.vaultcore.azure.net",
		}

		proxy := &AzureDNSProxy{
			log:               l,
			azureDNSServer:    azureDNSServer,
			azureDomains:      azureDomains,
			connectTimeout:    connectTimeout,
			tunnelIdleTimeout: tunnelIdleTimeout,
		}

		server := &http.Server{
			Addr:         listenAddr,
			Handler:      proxy,
			ReadTimeout:  requestTimeout,
			WriteTimeout: requestTimeout,
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

		go func() {
			<-sigChan
			l.Info("Received shutdown signal")
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer shutdownCancel()
			if err := server.Shutdown(shutdownCtx); err != nil {
				l.Error(err, "Error during shutdown")
			}
			cancel()
		}()

		l.Info("HTTP CONNECT proxy listening", "addr", listenAddr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			l.Error(err, "Server failed")
			os.Exit(1)
		}

		<-ctx.Done()
		l.Info("Azure DNS proxy stopped")
	}

	return cmd
}

type AzureDNSProxy struct {
	log               logr.Logger
	azureDNSServer    string
	azureDomains      []string
	connectTimeout    time.Duration
	tunnelIdleTimeout time.Duration
}

func (p *AzureDNSProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodConnect {
		http.Error(w, "Method not allowed. Only CONNECT is supported.", http.StatusMethodNotAllowed)
		return
	}

	p.log.V(1).Info("Received CONNECT request", "host", r.Host)

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "Hijacking not supported", http.StatusInternalServerError)
		return
	}

	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		p.log.Error(err, "Failed to hijack connection")
		return
	}
	defer clientConn.Close()

	host, port, err := net.SplitHostPort(r.Host)
	if err != nil {
		p.log.Error(err, "Invalid host:port")
		clientConn.Write([]byte("HTTP/1.1 400 Bad Request\r\n\r\n"))
		return
	}

	targetAddr, err := p.resolveHost(host, port)
	if err != nil {
		p.log.Error(err, "Failed to resolve host")
		clientConn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		return
	}

	dialer := &net.Dialer{Timeout: p.connectTimeout}
	targetConn, err := dialer.Dial("tcp", targetAddr)
	if err != nil {
		p.log.Error(err, "Failed to connect to target")
		clientConn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		return
	}
	defer targetConn.Close()

	_, err = clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
	if err != nil {
		p.log.Error(err, "Failed to write success response")
		return
	}

	p.log.Info("Established tunnel", "host", host, "target", targetAddr)
	p.tunnel(clientConn, targetConn)
}

func (p *AzureDNSProxy) resolveHost(host, port string) (string, error) {
	useAzureDNS := false
	for _, domain := range p.azureDomains {
		if strings.HasSuffix(host, domain) {
			useAzureDNS = true
			break
		}
	}

	if useAzureDNS {
		p.log.V(1).Info("Resolving via Azure DNS", "host", host)
		return p.resolveViaAzureDNS(host, port)
	}

	return net.JoinHostPort(host, port), nil
}

func (p *AzureDNSProxy) resolveViaAzureDNS(host, port string) (string, error) {
	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{Timeout: 5 * time.Second}
			return d.DialContext(ctx, network, p.azureDNSServer)
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ips, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return "", fmt.Errorf("DNS resolution failed for %s: %w", host, err)
	}

	if len(ips) == 0 {
		return "", fmt.Errorf("no IP addresses found for %s", host)
	}

	resolvedIP := ips[0].IP.String()
	p.log.Info("Resolved via Azure DNS", "host", host, "ip", resolvedIP)

	return net.JoinHostPort(resolvedIP, port), nil
}

func (p *AzureDNSProxy) tunnel(client, target net.Conn) {
	deadline := time.Now().Add(p.tunnelIdleTimeout)
	client.SetDeadline(deadline)
	target.SetDeadline(deadline)

	done := make(chan struct{}, 2)

	go func() {
		defer func() { done <- struct{}{} }()
		io.Copy(target, client)
	}()

	go func() {
		defer func() { done <- struct{}{} }()
		io.Copy(client, target)
	}()

	<-done
	client.Close()
	target.Close()
	<-done
}
