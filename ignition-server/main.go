package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"syscall"
	"time"

	hyperapi "github.com/openshift/hypershift/api"
	"github.com/openshift/hypershift/control-plane-operator/releaseinfo"
	"github.com/openshift/hypershift/ignition-server/controllers"
	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

const namespaceEnvVariableName = "MY_NAMESPACE"

// We only match /ignition
var ignPathPattern = regexp.MustCompile("^/ignition[^/ ]*$")
var payloadStore = controllers.NewPayloadStore()

// This is an https server that enable us to satisfy
// 1 - 1 relation between clusters and ign endpoints.
// It runs a token Secret controller.
// The token Secret controller uses an IgnitionProvider provider implementation
// (e.g machineConfigServerIgnitionProvider) to keep up to date a payload store in memory.
// The payload store has the structure "NodePool token": "payload".
// A token represents a given cluster version (and in the future also a machine Config) at any given point in time.
// For a request to succeed a token needs to be passed in the Header.
// TODO (alberto): Metrics.
func main() {
	cmd := &cobra.Command{
		Use: "ignition-server",
		Run: func(cmd *cobra.Command, args []string) {
			cmd.Help()
			os.Exit(1)
		},
	}
	cmd.AddCommand(NewStartCommand())

	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

type Options struct {
	Addr     string
	CertFile string
	KeyFile  string
}

func NewStartCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Starts the ignition server",
	}

	opts := Options{
		Addr:     "0.0.0.0:9090",
		CertFile: "/var/run/secrets/ignition/tls.crt",
		KeyFile:  "/var/run/secrets/ignition/tls.key",
	}

	cmd.Flags().StringVar(&opts.Addr, "addr", opts.Addr, "Listen address")
	cmd.Flags().StringVar(&opts.CertFile, "cert-file", opts.CertFile, "Path to the serving cert")
	cmd.Flags().StringVar(&opts.KeyFile, "key-file", opts.KeyFile, "Path to the serving key")

	cmd.Run = func(cmd *cobra.Command, args []string) {
		ctx, cancel := context.WithCancel(context.Background())
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT)
		go func() {
			<-sigs
			cancel()
		}()

		// TODO: Add an fsnotify watcher to cancel the context and trigger a restart
		// if any of the secret data has changed.
		if err := run(ctx, opts); err != nil {
			log.Fatal(err)
		}
	}

	return cmd
}

// payloadStoreReconciler runs a TokenSecretReconciler controller
// to keep the PayloadStore up to date.
func payloadStoreReconciler(ctx context.Context) error {
	if os.Getenv(namespaceEnvVariableName) == "" {
		return fmt.Errorf("environment variable %s is empty, this is not supported", namespaceEnvVariableName)
	}

	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: hyperapi.Scheme,
		Port:   9443,
		// TODO (alberto): expose this flags?
		// MetricsBindAddress: opts.MetricsAddr,
		// LeaderElection:     opts.EnableLeaderElection,
		Namespace: os.Getenv(namespaceEnvVariableName),
	})
	if err != nil {
		return fmt.Errorf("unable to start manager: %w", err)
	}

	kubeClient, err := kubernetes.NewForConfig(mgr.GetConfig())
	if err != nil {
		return fmt.Errorf("unable to create kube client: %w", err)
	}

	if err = (&controllers.TokenSecretReconciler{
		Client:       mgr.GetClient(),
		PayloadStore: payloadStore,
		IgnitionProvider: &controllers.MCSIgnitionProvider{
			ReleaseProvider: &releaseinfo.CachedProvider{
				Inner: &releaseinfo.PodProvider{
					Pods: kubeClient.CoreV1().Pods(os.Getenv(namespaceEnvVariableName)),
				},
				Cache: map[string]*releaseinfo.ReleaseImage{},
			},
			Client:    mgr.GetClient(),
			Namespace: os.Getenv(namespaceEnvVariableName),
		},
	}).SetupWithManager(ctx, mgr); err != nil {
		return fmt.Errorf("unable to create controller: %w", err)
	}

	return mgr.Start(ctx)
}

func run(ctx context.Context, opts Options) error {
	// Run the payloadReconciler to watch token Secrets in a particular Namespace
	if os.Getenv(namespaceEnvVariableName) == "" {
		return fmt.Errorf("environment variable %s is empty, this is not supported", namespaceEnvVariableName)
	}
	go payloadStoreReconciler(ctx)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("User Agent: %s. Requested: %s", r.Header.Get("User-Agent"), r.URL.Path)

		if !ignPathPattern.MatchString(r.URL.Path) {
			// No pattern matched; send 404 response.
			log.Printf("Path not found: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}

		// Authorize the request against the token
		const bearerPrefix = "Bearer "
		auth := r.Header.Get("Authorization")
		n := len(bearerPrefix)
		if len(auth) < n || auth[:n] != bearerPrefix {
			log.Printf("Invalid Authorization header value prefix")
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		encodedToken := auth[n:]
		decodedToken, err := base64.StdEncoding.DecodeString(encodedToken)
		if err != nil {
			log.Printf("Invalid token value")
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		payload, ok := payloadStore.Get(string(decodedToken))
		if !ok {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Write(payload)
		return
	})

	server := http.Server{
		Addr:         opts.Addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		if err := server.Shutdown(ctx); err != nil {
			log.Printf("error shutting down server: %s", err)
		}
	}()

	log.Printf("Listening on %s", opts.Addr)
	if err := server.ListenAndServeTLS(opts.CertFile, opts.KeyFile); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
