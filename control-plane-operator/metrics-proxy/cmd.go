package metricsproxy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/openshift/hypershift/support/metrics"
	"github.com/openshift/hypershift/support/supportedversion"

	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/spf13/cobra"
	"go.uber.org/zap/zapcore"
)

func NewStartCommand() *cobra.Command {
	log.SetLogger(zap.New(zap.JSONEncoder(func(o *zapcore.EncoderConfig) {
		o.EncodeTime = zapcore.RFC3339TimeEncoder
	})))
	l := log.Log.WithName("metrics-proxy")

	cmd := &cobra.Command{
		Use:   "metrics-proxy",
		Short: "Runs the metrics proxy for hosted control plane components.",
	}

	var (
		servingPort  uint32
		certFile     string
		keyFile      string
		clientCAFile string
		metricsSet   string
		configFile   string
	)

	cmd.Flags().Uint32Var(&servingPort, "serving-port", 9443, "The port to serve metrics on.")
	cmd.Flags().StringVar(&certFile, "tls-cert-file", "/etc/metrics-proxy/serving-cert/tls.crt", "Path to TLS certificate file.")
	cmd.Flags().StringVar(&keyFile, "tls-key-file", "/etc/metrics-proxy/serving-cert/tls.key", "Path to TLS private key file.")
	cmd.Flags().StringVar(&clientCAFile, "client-ca-file", "/etc/metrics-proxy/client-ca/ca.crt", "Path to CA certificate file for verifying client certificates (mTLS).")
	cmd.Flags().StringVar(&metricsSet, "metrics-set", "All", "The metrics set to use for filtering (e.g. All, Telemetry, SRE).")
	cmd.Flags().StringVar(&configFile, "config-file", "", "Path to scrape config file (required). Component discovery and endpoint resolution use file-based config and the endpoint-resolver service.")

	cmd.Run = func(cmd *cobra.Command, args []string) {
		l.Info("Starting metrics-proxy", "version", supportedversion.String())

		if configFile == "" {
			fmt.Fprintf(os.Stderr, "Error: --config-file is required\n")
			os.Exit(1)
		}

		ctx, cancel := context.WithCancel(cmd.Context())
		defer cancel()

		namespace := os.Getenv("MY_NAMESPACE")
		if namespace == "" {
			fmt.Fprintln(os.Stderr, "Error: MY_NAMESPACE environment variable is not set")
			os.Exit(1)
		}

		// Load the client CA for mTLS verification.
		clientCACert, err := os.ReadFile(clientCAFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading client CA file: %v\n", err)
			os.Exit(1)
		}
		clientCAPool := x509.NewCertPool()
		if !clientCAPool.AppendCertsFromPEM(clientCACert) {
			fmt.Fprintf(os.Stderr, "Error: failed to parse client CA certificate from %s\n", clientCAFile)
			os.Exit(1)
		}

		configReader := NewConfigFileReader(configFile, l)
		if err := configReader.Load(); err != nil {
			fmt.Fprintf(os.Stderr, "Error loading config file: %v\n", err)
			os.Exit(1)
		}
		l.Info("Config loaded", "components", configReader.GetComponentNames())

		resolverURL := configReader.EndpointResolverURL()
		resolverCAFile := configReader.EndpointResolverCAFile()
		resolverClient, err := NewEndpointResolverClient(resolverURL, resolverCAFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating endpoint-resolver client: %v\n", err)
			os.Exit(1)
		}

		scraper := NewScraper()
		filter := NewFilter(metrics.MetricsSet(metricsSet))
		labeler := NewLabeler(namespace)

		handler := NewProxyHandler(l, configReader, resolverClient, scraper, filter, labeler)
		mux := http.NewServeMux()
		mux.Handle("/", handler)
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})

		tlsCert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading TLS certificate: %v\n", err)
			os.Exit(1)
		}

		server := &http.Server{
			Addr:    fmt.Sprintf(":%d", servingPort),
			Handler: mux,
			TLSConfig: &tls.Config{
				Certificates: []tls.Certificate{tlsCert},
				ClientAuth:   tls.RequireAndVerifyClientCert,
				ClientCAs:    clientCAPool,
				MinVersion:   tls.VersionTLS12,
			},
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      60 * time.Second,
			IdleTimeout:       120 * time.Second,
		}

		// Handle graceful shutdown on SIGTERM/SIGINT.
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)
		go func() {
			sig := <-sigChan
			l.Info("Received shutdown signal", "signal", sig)
			cancel()
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer shutdownCancel()
			if err := server.Shutdown(shutdownCtx); err != nil {
				l.Error(err, "Error during server shutdown")
			}
		}()

		l.Info("Serving metrics-proxy", "port", servingPort)
		if err := server.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "Error serving: %v\n", err)
			os.Exit(1)
		}
		_ = ctx
		l.Info("Server stopped")
	}

	return cmd
}
