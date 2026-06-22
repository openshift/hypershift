package endpointresolver

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/openshift/hypershift/support/supportedversion"

	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/spf13/cobra"
	"go.uber.org/zap/zapcore"
)

func NewStartCommand() *cobra.Command {
	l := log.Log.WithName("endpoint-resolver")
	log.SetLogger(zap.New(zap.JSONEncoder(func(o *zapcore.EncoderConfig) {
		o.EncodeTime = zapcore.RFC3339TimeEncoder
	})))

	cmd := &cobra.Command{
		Use:   "endpoint-resolver",
		Short: "Runs the endpoint resolver for hosted control plane pod endpoint discovery.",
	}

	var (
		servingPort uint32
		certFile    string
		keyFile     string
	)

	cmd.Flags().Uint32Var(&servingPort, "serving-port", 9444, "The port to serve on.")
	cmd.Flags().StringVar(&certFile, "tls-cert-file", "/etc/endpoint-resolver/serving-cert/tls.crt", "Path to TLS certificate file.")
	cmd.Flags().StringVar(&keyFile, "tls-key-file", "/etc/endpoint-resolver/serving-cert/tls.key", "Path to TLS private key file.")

	cmd.Run = func(cmd *cobra.Command, args []string) {
		l.Info("Starting endpoint-resolver", "version", supportedversion.String())

		ctx, cancel := context.WithCancel(cmd.Context())
		defer cancel()

		namespace := os.Getenv("MY_NAMESPACE")
		if namespace == "" {
			fmt.Fprintln(os.Stderr, "MY_NAMESPACE environment variable is required")
			os.Exit(1)
		}

		inClusterConfig, err := rest.InClusterConfig()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error getting in-cluster config: %v\n", err)
			os.Exit(1)
		}
		k8sClient, err := kubernetes.NewForConfig(inClusterConfig)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating kubernetes client: %v\n", err)
			os.Exit(1)
		}

		factory := informers.NewSharedInformerFactoryWithOptions(
			k8sClient,
			10*time.Minute,
			informers.WithNamespace(namespace),
		)
		podLister := factory.Core().V1().Pods().Lister().Pods(namespace)

		factory.Start(ctx.Done())
		factory.WaitForCacheSync(ctx.Done())

		handler := newResolverHandler(podLister)
		mux := http.NewServeMux()
		mux.Handle(resolvePath, handler)
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
				MinVersion:   tls.VersionTLS12,
			},
		}

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

		l.Info("Serving endpoint-resolver", "port", servingPort, "namespace", namespace)
		if err := server.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "Error serving: %v\n", err)
			os.Exit(1)
		}
		_ = ctx
		l.Info("Server stopped")
	}

	return cmd
}
