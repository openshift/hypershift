package cmd

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"os"

	"sigs.k8s.io/yaml"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
	"go.uber.org/zap/zapcore"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/openshift/hypershift/contrib/gomaxprocs-webhook/internal/config"
	intscheme "github.com/openshift/hypershift/contrib/gomaxprocs-webhook/internal/scheme"
	"github.com/openshift/hypershift/contrib/gomaxprocs-webhook/internal/webhook/pod"
)

const (
	// maxConfigFileSize limits configuration file size to prevent memory exhaustion attacks.
	// This should match the limit in config/loader.go
	maxConfigFileSize = 1 * 1024 * 1024 // 1MB
)

func newServeCmd() *cobra.Command {
	var (
		metricsAddr  string
		probeAddr    string
		certDir      string
		port         int
		configPath   string
		defaultValue string
		logDev       bool
		logLevel     int
	)

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the webhook server",
		RunE: func(c *cobra.Command, args []string) error {
			return runServer(c.Context(), metricsAddr, probeAddr, certDir, port, configPath, defaultValue, logDev, logLevel)
		},
	}

	cmd.Flags().StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	cmd.Flags().StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	cmd.Flags().StringVar(&certDir, "cert-dir", "/var/run/secrets/serving-cert", "Directory with TLS cert/key for the webhook server.")
	cmd.Flags().IntVar(&port, "port", 9443, "Port for the webhook server.")
	cmd.Flags().StringVar(&configPath, "config-path", "/etc/config/config.yaml", "Path to the configuration file.")
	cmd.Flags().StringVar(&defaultValue, "default", "", "Default GOMAXPROCS value if not specified in the config.")
	cmd.Flags().BoolVar(&logDev, "log-dev", false, "Enable development logging (human-friendly).")
	cmd.Flags().IntVar(&logLevel, "log-level", 0, "Log verbosity level (0=info only, 1=verbose, 2=debug).")

	return cmd
}

func runServer(ctx context.Context, metricsAddr, probeAddr, certDir string, port int, configPath, defaultVal string, dev bool, logLevel int) error {
	logger := zap.New(zap.UseDevMode(dev), zap.Level(zapcore.Level(-1*logLevel)))
	ctrl.SetLogger(logger)

	scheme := intscheme.New()

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		WebhookServer: webhook.NewServer(webhook.Options{
			Port:    port,
			CertDir: certDir,
			TLSOpts: []func(*tls.Config){
				func(cfg *tls.Config) {
					cfg.MinVersion = tls.VersionTLS12
				},
			},
		}),
	})
	if err != nil {
		return fmt.Errorf("unable to start manager: %w", err)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		return fmt.Errorf("unable to set up health check: %w", err)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		return fmt.Errorf("unable to set up ready check: %w", err)
	}

	// Config loader
	cfgLoader := config.NewConfigLoader(configPath, defaultVal, logger)

	// Validate configuration is available at startup
	logger.V(1).Info("Validating webhook configuration", "configPath", configPath)
	if err := validateConfigurationFile(configPath, logger); err != nil {
		return fmt.Errorf("configuration validation failed: %w", err)
	}
	logger.Info("Webhook configuration validation successful", "configPath", configPath)

	// Webhook registration
	hookServer := mgr.GetWebhookServer()
	hookServer.Register("/mutate-pod", &webhook.Admission{Handler: pod.NewHandler(logger, mgr.GetClient(), admission.NewDecoder(mgr.GetScheme()), cfgLoader)})

	return mgr.Start(ctx)
}

// validateConfigurationFile ensures the configuration file exists and is valid at startup
func validateConfigurationFile(configPath string, logger logr.Logger) error {
	if configPath == "" {
		logger.V(1).Info("No config file specified, webhook will use defaults only")
		return nil
	}

	// Check if file exists
	if _, err := os.Stat(configPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("required configuration file %s not found - webhook cannot function without configuration", configPath)
		}
		return fmt.Errorf("failed to access configuration file %s: %w", configPath, err)
	}

	// Read the file with size limit
	configYAML, err := readFileWithLimit(configPath, maxConfigFileSize)
	if err != nil {
		return fmt.Errorf("failed to read configuration file %s: %w", configPath, err)
	}

	if len(configYAML) == 0 {
		logger.V(1).Info("Configuration file is empty, webhook will use defaults only", "configPath", configPath)
		return nil
	}

	// Validate the YAML can be parsed
	var testConfig config.Config
	if err := yaml.Unmarshal(configYAML, &testConfig); err != nil {
		return fmt.Errorf("configuration file %s contains invalid YAML: %w", configPath, err)
	}

	logger.V(1).Info("Configuration file validation successful", "configPath", configPath, "default", testConfig.Default,
		"overrides", testConfig.Overrides.String(),
		"exclusions", testConfig.Exclusions.String())
	return nil
}

// readFileWithLimit reads a file with a size limit to prevent memory exhaustion attacks.
// It uses io.LimitReader to prevent reading beyond the specified limit.
func readFileWithLimit(filename string, limit int64) ([]byte, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	// Use LimitReader to prevent reading beyond the limit
	limitedReader := io.LimitReader(file, limit+1) // +1 to detect if file exceeds limit
	data, err := io.ReadAll(limitedReader)
	if err != nil {
		return nil, err
	}

	// Check if we read exactly limit+1 bytes, which means the file is too large
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("config file %s is too large (exceeds %d bytes), maximum allowed is %d bytes", filename, limit, limit)
	}

	return data, nil
}
