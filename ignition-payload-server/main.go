package ignitionpayloadserver

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperapi "github.com/openshift/hypershift/support/api"
	payloadstore "github.com/openshift/hypershift/support/ignitionpayload"

	librarycrypto "github.com/openshift/library-go/pkg/crypto"

	corev1 "k8s.io/api/core/v1"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/certwatcher"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/spf13/cobra"
)

const namespaceEnvVariableName = "MY_NAMESPACE"

// serverShutdownTimeout bounds graceful HTTP shutdown so a stuck connection cannot block process exit.
const serverShutdownTimeout = 10 * time.Second

var setupLog = ctrl.Log.WithName("setup")

// Options configures the ignition-payload-server.
type Options struct {
	Addr                     string
	CertFile                 string
	KeyFile                  string
	TokenVerificationKeyFile string
	TLSMinVersion            string
	TLSCipherSuites          []string
}

// NewStartCommand returns the cobra command that runs the stateless (leaderless) ignition payload
// serving tier. It is registered as a subcommand of the HyperShift Operator binary.
func NewStartCommand() *cobra.Command {
	opts := Options{
		Addr:                     "0.0.0.0:9090",
		CertFile:                 "/var/run/secrets/ignition/serving-cert/tls.crt",
		KeyFile:                  "/var/run/secrets/ignition/serving-cert/tls.key",
		TokenVerificationKeyFile: "/var/run/secrets/ignition/token-verification-key/ec-public.pem",
	}

	cmd := &cobra.Command{
		Use:   "ignition-payload-server",
		Short: "Stateless server that serves ignition payloads from the PayloadStore",
		Run: func(cmd *cobra.Command, args []string) {
			if err := run(ctrl.SetupSignalHandler(), opts); err != nil {
				setupLog.Error(err, "unable to start server")
				os.Exit(1)
			}
		},
	}

	cmd.Flags().StringVar(&opts.Addr, "addr", opts.Addr, "Listen address")
	cmd.Flags().StringVar(&opts.CertFile, "cert-file", opts.CertFile, "Path to the serving cert")
	cmd.Flags().StringVar(&opts.KeyFile, "key-file", opts.KeyFile, "Path to the serving key")
	cmd.Flags().StringVar(&opts.TokenVerificationKeyFile, "token-verification-key-file", opts.TokenVerificationKeyFile, "Path to the PEM ECDSA public key used to verify node bearer tokens")
	cmd.Flags().StringVar(&opts.TLSMinVersion, "tls-min-version", "", "Minimum TLS version (e.g., VersionTLS12, VersionTLS13)")
	cmd.Flags().StringSliceVar(&opts.TLSCipherSuites, "tls-cipher-suites", nil, "TLS cipher suites (comma-separated)")

	return cmd
}

func run(ctx context.Context, opts Options) error {
	namespace := os.Getenv(namespaceEnvVariableName)
	if namespace == "" {
		return fmt.Errorf("environment variable %s is empty, this is not supported", namespaceEnvVariableName)
	}

	setupLog.Info("Starting ignition-payload-server", "namespace", namespace, "addr", opts.Addr)

	// The serving tier holds only the public verification key; without it cannot authenticate any
	// node, so a missing/unparsable key is a hard startup failure.
	verifier, err := loadTokenVerifier(opts.TokenVerificationKeyFile)
	if err != nil {
		return err
	}

	restConfig := ctrl.GetConfigOrDie()
	restConfig.UserAgent = "ignition-payload-server"

	// Stateless serving tier: NO leader election (all replicas serve). Payload Secrets are always read
	// through the APIReader, never cached: an in-place refresh or a deletion must take effect on the
	// very next serve. The only informer is the namespace-scoped IgnitionPayload one that backs
	// SetIgnitionReached's fast path. Secrets are excluded from the cached client so a stray Secret Get
	// through mgr.GetClient() reads live instead of lazily starting a namespace-wide Secret informer.
	mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
		Scheme: hyperapi.Scheme,
		Cache: cache.Options{
			DefaultNamespaces: map[string]cache.Config{namespace: {}},
		},
		Client: client.Options{
			Cache: &client.CacheOptions{DisableFor: []client.Object{&corev1.Secret{}}},
		},
	})
	if err != nil {
		return fmt.Errorf("unable to start manager: %w", err)
	}

	certWatcher, err := certwatcher.New(opts.CertFile, opts.KeyFile)
	if err != nil {
		return fmt.Errorf("failed to load serving cert: %w", err)
	}
	if err := mgr.Add(certWatcher); err != nil {
		return fmt.Errorf("failed to add certWatcher to manager: %w", err)
	}

	// IgnitionReached is recorded off the request path: the handler enqueues each served
	// (payload, generation) and a single worker performs the de-duplicated status writes.
	reached := NewReachedRecorder(func(ctx context.Context, owner payloadstore.OwnerRef, rolloutGeneration int64) error {
		return SetIgnitionReached(ctx, mgr.GetClient(), mgr.GetAPIReader(), owner, rolloutGeneration)
	})

	if err := mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
		reached.Run(ctx)
		return nil
	})); err != nil {
		return fmt.Errorf("failed to add IgnitionReached recorder to manager: %w", err)
	}

	srv := &Server{
		Namespace: namespace,
		Verifier:  verifier,
		Payloads:  payloadstore.NewSecretBackedReader(mgr.GetAPIReader(), namespace),
		APIReader: mgr.GetAPIReader(),
		OnServed:  reached.Enqueue,
	}

	// Register the IgnitionPayload informer SetIgnitionReached's fast path reads before starting the
	// manager, so the cache-sync barrier below actually blocks on it: WaitForCacheSync only waits for
	// already-registered informers, so without this the barrier is a no-op and the first served request
	// would synchronously create and sync the informer under request load.
	if _, err := mgr.GetCache().GetInformer(ctx, &hyperv1.IgnitionPayload{}, cache.BlockUntilSynced(false)); err != nil {
		return fmt.Errorf("failed to register IgnitionPayload informer: %w", err)
	}

	// Tie the serving path's lifetime to the manager: if the manager exits (e.g. the metrics server
	// fails to bind, or the cert watcher errors), cancel a child context with the cause so the cache
	// wait and the server both unwind instead of leaving the process alive but never serving.
	runCtx, cancelRun := context.WithCancelCause(ctx)
	defer cancelRun(nil)

	go func() {
		if err := mgr.Start(runCtx); err != nil {
			setupLog.Error(err, "manager exited")
			cancelRun(err)
		}
	}()

	if !mgr.GetCache().WaitForCacheSync(runCtx) {
		if cause := context.Cause(runCtx); ctx.Err() == nil && cause != nil {
			return fmt.Errorf("manager exited before caches synced: %w", cause)
		}
		return fmt.Errorf("failed to wait for caches to sync")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", srv.HandleIgnition)

	server := &http.Server{
		Addr:         opts.Addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		TLSConfig:    buildTLSConfig(certWatcher, opts),
		TLSNextProto: make(map[string]func(*http.Server, *tls.Conn, http.Handler)),
	}

	// Drain the server when the parent context is canceled or the manager fails; signal completion so
	// run does not return (and let the process exit) while responses are still in flight.
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-runCtx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), serverShutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			setupLog.Error(err, "error shutting down server")
		}
	}()

	setupLog.Info("listening", "addr", opts.Addr)
	if err := server.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	// Wait for the drain to finish before returning so active responses are not cut off at exit.
	<-shutdownDone
	if cause := context.Cause(runCtx); ctx.Err() == nil && cause != nil {
		return fmt.Errorf("manager exited: %w", cause)
	}
	return nil
}

// loadTokenVerifier reads the PEM ECDSA public key the serving tier verifies node bearer tokens
// against and builds a verifier from it.
func loadTokenVerifier(path string) (*payloadstore.TokenVerifier, error) {
	pemBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("unable to read token-verification key %q: %w", path, err)
	}
	pub, err := payloadstore.LoadECDSAPublicKey(pemBytes)
	if err != nil {
		return nil, err
	}
	return payloadstore.NewTokenVerifier(pub), nil
}

func buildTLSConfig(certWatcher *certwatcher.CertWatcher, opts Options) *tls.Config {
	cfg := &tls.Config{
		GetCertificate: certWatcher.GetCertificate,
	}
	if opts.TLSMinVersion != "" {
		minVersion, err := librarycrypto.TLSVersion(opts.TLSMinVersion)
		if err != nil {
			log.Fatalf("invalid TLS min version: %v", err)
		}
		cfg.MinVersion = minVersion
	}
	if len(opts.TLSCipherSuites) > 0 {
		cfg.CipherSuites = librarycrypto.CipherSuitesOrDie(opts.TLSCipherSuites)
	}
	return librarycrypto.SecureTLSConfig(cfg)
}
