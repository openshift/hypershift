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
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/ignition-server/controllers"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/spf13/cobra"
	"go.uber.org/zap/zapcore"
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
	Addr              string
	CertFile          string
	KeyFile           string
	RegistryOverrides map[string]string
	Platform          string
}

func NewStartCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Starts the ignition server",
	}

	opts := Options{
		Addr:              "0.0.0.0:9090",
		CertFile:          "/var/run/secrets/ignition/serving-cert/tls.crt",
		KeyFile:           "/var/run/secrets/ignition/serving-cert/tls.key",
		RegistryOverrides: map[string]string{},
	}

	cmd.Flags().StringVar(&opts.Addr, "addr", opts.Addr, "Listen address")
	cmd.Flags().StringVar(&opts.CertFile, "cert-file", opts.CertFile, "Path to the serving cert")
	cmd.Flags().StringVar(&opts.KeyFile, "key-file", opts.KeyFile, "Path to the serving key")
	cmd.Flags().StringToStringVar(&opts.RegistryOverrides, "registry-overrides", map[string]string{}, "registry-overrides contains the source registry string as a key and the destination registry string as value. Images before being applied are scanned for the source registry string and if found the string is replaced with the destination registry string. Format is: sr1=dr1,sr2=dr2")
	cmd.Flags().StringVar(&opts.Platform, "platform", "", "The cloud provider platform name")

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

// setUpPayloadStoreReconciler sets up manager with a TokenSecretReconciler controller
// to keep the PayloadStore up to date.
func setUpPayloadStoreReconciler(ctx context.Context, registryOverrides map[string]string, cloudProvider hyperv1.PlatformType) (ctrl.Manager, error) {
	if os.Getenv(namespaceEnvVariableName) == "" {
		return nil, fmt.Errorf("environment variable %s is empty, this is not supported", namespaceEnvVariableName)
	}

	ctrl.SetLogger(zap.New(zap.UseDevMode(true), zap.JSONEncoder(func(o *zapcore.EncoderConfig) {
		o.EncodeTime = zapcore.RFC3339TimeEncoder
	})))
	restConfig := ctrl.GetConfigOrDie()
	restConfig.UserAgent = "ignition-server-manager"
	mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
		Scheme: hyperapi.Scheme,
		Port:   9443,
		// TODO (alberto): expose this flags?
		// MetricsBindAddress: opts.MetricsAddr,
		// LeaderElection:     opts.EnableLeaderElection,
		Namespace: os.Getenv(namespaceEnvVariableName),
	})
	if err != nil {
		return nil, fmt.Errorf("unable to start manager: %w", err)
	}
	if err = (&controllers.TokenSecretReconciler{
		Client:       mgr.GetClient(),
		PayloadStore: payloadStore,
		IgnitionProvider: &controllers.MCSIgnitionProvider{
			ReleaseProvider: &releaseinfo.RegistryMirrorProviderDecorator{
				Delegate: &releaseinfo.CachedProvider{
					Inner: &releaseinfo.RegistryClientProvider{},
					Cache: map[string]*releaseinfo.ReleaseImage{},
				},
				RegistryOverrides: registryOverrides,
			},
			Client:        mgr.GetClient(),
			Namespace:     os.Getenv(namespaceEnvVariableName),
			CloudProvider: cloudProvider,
		},
	}).SetupWithManager(ctx, mgr); err != nil {
		return nil, fmt.Errorf("unable to create controller: %w", err)
	}

	return mgr, nil
}

func run(ctx context.Context, opts Options) error {
	mgr, err := setUpPayloadStoreReconciler(ctx, opts.RegistryOverrides, hyperv1.PlatformType(opts.Platform))
	if err != nil {
		return fmt.Errorf("error setting up manager: %w", err)
	}
	go mgr.Start(ctx)

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

		value, ok := payloadStore.Get(string(decodedToken))
		if !ok {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Write(value.Payload)
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
