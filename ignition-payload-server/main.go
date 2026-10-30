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

	hyperapi "github.com/openshift/hypershift/support/api"
	payloadstore "github.com/openshift/hypershift/support/ignitionpayload"

	librarycrypto "github.com/openshift/library-go/pkg/crypto"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/certwatcher"

	"github.com/spf13/cobra"
)

const namespaceEnvVariableName = "MY_NAMESPACE"

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

	// Stateless serving tier: NO leader election (all replicas serve). The namespace-scoped cache
	// is the per-pod informer that hydrates the fast path; the APIReader is the read-through on miss.
	mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
		Scheme: hyperapi.Scheme,
		Cache: cache.Options{
			DefaultNamespaces: map[string]cache.Config{namespace: {}},
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

	srv := &Server{
		Namespace: namespace,
		Verifier:  verifier,
		Cached:    mgr.GetClient(),
		Uncached:  mgr.GetAPIReader(),
		OnServed: func(ctx context.Context, owner payloadstore.OwnerRef, rolloutGeneration int64) {
			if err := SetIgnitionReached(ctx, mgr.GetClient(), owner, rolloutGeneration); err != nil {
				setupLog.Error(err, "failed to record IgnitionReached", "owner", owner.Namespace+"/"+owner.Name, "rolloutGeneration", rolloutGeneration)
			}
		},
	}

	go func() {
		if err := mgr.Start(ctx); err != nil {
			setupLog.Error(err, "manager exited")
		}
	}()

	// Wait for the informer cache before serving so the cached fast path is usable; until then a
	// miss would otherwise fall through to the APIReader read-through regardless.
	if !mgr.GetCache().WaitForCacheSync(ctx) {
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

	go func() {
		<-ctx.Done()
		if err := server.Shutdown(context.Background()); err != nil {
			setupLog.Error(err, "error shutting down server")
		}
	}()

	setupLog.Info("listening", "addr", opts.Addr)
	if err := server.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
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
