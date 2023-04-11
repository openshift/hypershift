package cmd

import (
	"context"
	"crypto/tls"
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
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/ignition-server/controllers"
	"github.com/openshift/hypershift/pkg/version"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/spf13/cobra"
	"go.uber.org/zap/zapcore"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/certwatcher"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

const namespaceEnvVariableName = "MY_NAMESPACE"

// We only match /ignition
var ignPathPattern = regexp.MustCompile("^/ignition[^/ ]*$")
var payloadStore = controllers.NewPayloadStore()

type Options struct {
	Addr              string
	CertFile          string
	KeyFile           string
	RegistryOverrides map[string]string
	Platform          string
	WorkDir           string
	MetricsAddr       string
}

// This is an https server that enable us to satisfy
// 1 - 1 relation between clusters and ign endpoints.
// It runs a token Secret controller.
// The token Secret controller uses an IgnitionProvider provider implementation
// (e.g machineConfigServerIgnitionProvider) to keep up to date a payload store in memory.
// The payload store has the structure "NodePool token": "payload".
// A token represents a given cluster version (and in the future also a machine Config) at any given point in time.
// For a request to succeed a token needs to be passed in the Header.
func NewStartCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ignition-server",
		Short: "Starts the ignition server",
	}

	opts := Options{
		Addr:              "0.0.0.0:9090",
		MetricsAddr:       "0.0.0.0:8080",
		CertFile:          "/var/run/secrets/ignition/serving-cert/tls.crt",
		KeyFile:           "/var/run/secrets/ignition/serving-cert/tls.key",
		WorkDir:           "/payloads",
		RegistryOverrides: map[string]string{},
	}

	cmd.Flags().StringVar(&opts.Addr, "addr", opts.Addr, "Listen address")
	cmd.Flags().StringVar(&opts.CertFile, "cert-file", opts.CertFile, "Path to the serving cert")
	cmd.Flags().StringVar(&opts.KeyFile, "key-file", opts.KeyFile, "Path to the serving key")
	cmd.Flags().StringToStringVar(&opts.RegistryOverrides, "registry-overrides", map[string]string{}, "registry-overrides contains the source registry string as a key and the destination registry string as value. Images before being applied are scanned for the source registry string and if found the string is replaced with the destination registry string. Format is: sr1=dr1,sr2=dr2")
	cmd.Flags().StringVar(&opts.Platform, "platform", "", "The cloud provider platform name")
	cmd.Flags().StringVar(&opts.WorkDir, "work-dir", opts.WorkDir, "Directory in which to store transient working data")
	cmd.Flags().StringVar(&opts.MetricsAddr, "metrics-addr", opts.MetricsAddr, "The address the metric endpoint binds to.")

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
func setUpPayloadStoreReconciler(ctx context.Context, registryOverrides map[string]string, cloudProvider hyperv1.PlatformType, cacheDir string, metricsAddr string) (ctrl.Manager, error) {
	if os.Getenv(namespaceEnvVariableName) == "" {
		return nil, fmt.Errorf("environment variable %s is empty, this is not supported", namespaceEnvVariableName)
	}

	restConfig := ctrl.GetConfigOrDie()
	restConfig.UserAgent = "ignition-server-manager"
	mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
		Scheme:             hyperapi.Scheme,
		Port:               9443,
		MetricsBindAddress: metricsAddr,
		// LeaderElection:     opts.EnableLeaderElection,
		Namespace: os.Getenv(namespaceEnvVariableName),
	})
	if err != nil {
		return nil, fmt.Errorf("unable to start manager: %w", err)
	}
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		return nil, fmt.Errorf("unable to set up health check: %w", err)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		return nil, fmt.Errorf("unable to set up ready check: %w", err)
	}

	imageFileCache, err := controllers.NewImageFileCache(cacheDir)
	if err != nil {
		return nil, fmt.Errorf("unable to create image file cache: %w", err)
	}

	if err = (&controllers.TokenSecretReconciler{
		Client:       mgr.GetClient(),
		PayloadStore: payloadStore,
		IgnitionProvider: &controllers.LocalIgnitionProvider{
			ReleaseProvider: &releaseinfo.RegistryMirrorProviderDecorator{
				Delegate: &releaseinfo.CachedProvider{
					Inner: &releaseinfo.RegistryClientProvider{},
					Cache: map[string]*releaseinfo.ReleaseImage{},
				},
				RegistryOverrides: registryOverrides,
			},
			Client:         mgr.GetClient(),
			Namespace:      os.Getenv(namespaceEnvVariableName),
			CloudProvider:  cloudProvider,
			WorkDir:        cacheDir,
			ImageFileCache: imageFileCache,
		},
	}).SetupWithManager(ctx, mgr); err != nil {
		return nil, fmt.Errorf("unable to create controller: %w", err)
	}

	return mgr, nil
}

func run(ctx context.Context, opts Options) error {
	logger := zap.New(zap.UseDevMode(true), zap.JSONEncoder(func(o *zapcore.EncoderConfig) {
		o.EncodeTime = zapcore.RFC3339TimeEncoder
	}))
	ctrl.SetLogger(logger)
	logger.Info("Starting ignition-server", "version", version.String())

	certWatcher, err := certwatcher.New(opts.CertFile, opts.KeyFile)
	if err != nil {
		return fmt.Errorf("failed to load serving cert: %w", err)
	}

	mgr, err := setUpPayloadStoreReconciler(ctx, opts.RegistryOverrides, hyperv1.PlatformType(opts.Platform), opts.WorkDir, opts.MetricsAddr)
	if err != nil {
		return fmt.Errorf("error setting up manager: %w", err)
	}
	if err := mgr.Add(certWatcher); err != nil {
		return fmt.Errorf("failed to add certWatcher to manager: %w", err)
	}
	go mgr.Start(ctx)

	mgr.GetLogger().Info("Using opts", "opts", fmt.Sprintf("%+v", opts))

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
			// We return a 5xx here to give ignition the chance to backoff and retry if the machine request happens
			// before the content is cached for this token.
			// https://coreos.github.io/ignition/operator-notes/#http-backoff-and-retry
			log.Printf("Token not found")
			http.Error(w, "Token not found", http.StatusNetworkAuthenticationRequired)
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Write(value.Payload)
	})
	mux.HandleFunc("/healthz", func(http.ResponseWriter, *http.Request) {})

	server := http.Server{
		Addr:         opts.Addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		TLSConfig: &tls.Config{GetCertificate: certWatcher.GetCertificate,
			MinVersion: tls.VersionTLS12,
			CipherSuites: []uint16{
				//TLS 1.3 ciphers from openshift tls modern profile
				tls.TLS_AES_128_GCM_SHA256,
				tls.TLS_AES_256_GCM_SHA384,
				tls.TLS_CHACHA20_POLY1305_SHA256,
				//TLS 1.2 subset from openshift intermediate tls profile
				tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
				tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
				tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
				tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
				tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
				tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
			},
		},
	}

	go func() {
		<-ctx.Done()
		if err := server.Shutdown(ctx); err != nil {
			log.Printf("error shutting down server: %s", err)
		}
	}()

	log.Printf("Listening on %s", opts.Addr)
	if err := server.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
