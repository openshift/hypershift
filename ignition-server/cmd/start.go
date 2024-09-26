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

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/nodepool"
	"github.com/openshift/hypershift/ignition-server/controllers"
	"github.com/openshift/hypershift/pkg/version"
	hyperapi "github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/util"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/spf13/cobra"
	"go.uber.org/zap/zapcore"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/certwatcher"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

const namespaceEnvVariableName = "MY_NAMESPACE"

var (
	// We only match /ignition
	ignPathPattern                       = regexp.MustCompile("^/ignition[^/ ]*$")
	payloadStore                         = controllers.NewPayloadStore()
	getRequestsPerNodePool               = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ign_server_get_request"}, []string{"nodePool"})
	TokenSecretIgnitionReachedAnnotation = "hypershift.openshift.io/ignition-reached"
)

func init() {
	metrics.Registry.MustRegister(
		getRequestsPerNodePool,
	)
}

type Options struct {
	Addr                string
	CertFile            string
	KeyFile             string
	RegistryOverrides   map[string]string
	Platform            string
	WorkDir             string
	MetricsAddr         string
	FeatureGateManifest string
}

// This is a https server that enable us to satisfy
// 1 - 1 relation between clusters and ign endpoints.
// It runs a token Secret controller.
// The token Secret controller uses an IgnitionProvider provider implementation
// (e.g. machineConfigServerIgnitionProvider) to keep up to date a payload store in memory.
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
	cmd.Flags().StringVar(&opts.FeatureGateManifest, "feature-gate-manifest", opts.FeatureGateManifest, "Path to a rendered featuregates.config.openshift.io/v1 file")

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
func setUpPayloadStoreReconciler(ctx context.Context, registryOverrides map[string]string, cloudProvider hyperv1.PlatformType, cacheDir string, metricsAddr string, featureGateManifest string) (ctrl.Manager, error) {
	if os.Getenv(namespaceEnvVariableName) == "" {
		return nil, fmt.Errorf("environment variable %s is empty, this is not supported", namespaceEnvVariableName)
	}

	restConfig := ctrl.GetConfigOrDie()
	restConfig.UserAgent = "ignition-server-manager"
	mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
		Scheme: hyperapi.Scheme,
		WebhookServer: webhook.NewServer(webhook.Options{
			Port: 9443,
		}),
		Metrics: metricsserver.Options{
			BindAddress: metricsAddr,
		},
		Cache: cache.Options{
			DefaultNamespaces: map[string]cache.Config{os.Getenv(namespaceEnvVariableName): {}},
		},
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
			ReleaseProvider: &releaseinfo.ProviderWithOpenShiftImageRegistryOverridesDecorator{
				Delegate: &releaseinfo.RegistryMirrorProviderDecorator{
					Delegate: &releaseinfo.CachedProvider{
						Inner: &releaseinfo.RegistryClientProvider{},
						Cache: map[string]*releaseinfo.ReleaseImage{},
					},
					RegistryOverrides: registryOverrides,
				},
				OpenShiftImageRegistryOverrides: util.ConvertImageRegistryOverrideStringToMap(os.Getenv("OPENSHIFT_IMG_OVERRIDES")),
			},
			Client:              mgr.GetClient(),
			Namespace:           os.Getenv(namespaceEnvVariableName),
			CloudProvider:       cloudProvider,
			WorkDir:             cacheDir,
			ImageFileCache:      imageFileCache,
			FeatureGateManifest: featureGateManifest,
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

	mgr, err := setUpPayloadStoreReconciler(ctx, opts.RegistryOverrides, hyperv1.PlatformType(opts.Platform), opts.WorkDir, opts.MetricsAddr, opts.FeatureGateManifest)
	if err != nil {
		return fmt.Errorf("error setting up manager: %w", err)
	}
	if err = mgr.Add(certWatcher); err != nil {
		return fmt.Errorf("failed to add certWatcher to manager: %w", err)
	}
	go func() {
		err = mgr.Start(ctx)
		if err != nil {
			logger.Error(err, "failed to start manager")
		}
	}()

	mgr.GetLogger().Info("Using opts", "opts", fmt.Sprintf("%+v", opts))
	eventRecorder := mgr.GetEventRecorderFor("ignition-server")

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("User Agent: %s. Requested: %s", r.Header.Get("User-Agent"), r.URL.Path)

		tokenSecret := nodepool.TokenSecret(os.Getenv(namespaceEnvVariableName),
			util.ParseNamespacedName(r.Header.Get("NodePool")).Name,
			r.Header.Get("TargetConfigVersionHash"))

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
			eventRecorder.Event(tokenSecret, corev1.EventTypeWarning, "GetPayloadFailed", "Bad header")
			return
		}
		encodedToken := auth[n:]
		decodedToken, err := base64.StdEncoding.DecodeString(encodedToken)
		if err != nil {
			log.Printf("Invalid token value")
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			eventRecorder.Event(tokenSecret, corev1.EventTypeWarning, "GetPayloadFailed", "Token invalid")
			return
		}

		value, ok := payloadStore.Get(string(decodedToken))
		if !ok {
			// We return a 5xx here to give ignition the chance to backoff and retry if the machine request happens
			// before the content is cached for this token.
			// https://coreos.github.io/ignition/operator-notes/#http-backoff-and-retry
			log.Printf("Token not found")
			http.Error(w, "Token not found", http.StatusNetworkAuthenticationRequired)
			eventRecorder.Event(tokenSecret, corev1.EventTypeWarning, "GetPayloadFailed", "Token not found in cache")
			return
		}

		if err := util.SanitizeIgnitionPayload(value.Payload); err != nil {
			log.Printf("Invalid ignition payload: %s", err)
			http.Error(w, "Invalid ignition payload", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, err = w.Write(value.Payload)
		if err != nil {
			logger.Error(err, "failed to write response")
		}

		eventRecorder.Event(tokenSecret, corev1.EventTypeNormal, "GetPayload", "")
		getRequestsPerNodePool.WithLabelValues(r.Header.Get("NodePool")).Inc()

		// Annotate tokenSecret so NodePool controller can set a conditions based on it.
		if err := mgr.GetClient().Get(ctx, client.ObjectKeyFromObject(tokenSecret), tokenSecret); err != nil {
			log.Printf("Failed to get tokenSecret resource: %q: %s", client.ObjectKeyFromObject(tokenSecret).String(), err)
		} else {
			tokenSecret.Annotations[TokenSecretIgnitionReachedAnnotation] = "True"
			if err := mgr.GetClient().Update(ctx, tokenSecret); err != nil {
				log.Printf("Failed to update tokenSecret: %q: %s", tokenSecret.Name, err)
			}
		}
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
		TLSNextProto: make(map[string]func(*http.Server, *tls.Conn, http.Handler)),
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
