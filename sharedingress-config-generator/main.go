package sharedingressconfiggenerator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	hyperapi "github.com/openshift/hypershift/support/api"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/spf13/cobra"
)

var (
	setupLog = ctrl.Log.WithName("setup")
)

type Options struct {
	ConfigPath        string
	HAProxySocketPath string
}

func NewStartCommand() *cobra.Command {
	opts := Options{
		ConfigPath:        "/usr/local/etc/haproxy/haproxy.cfg",
		HAProxySocketPath: "/var/run/haproxy/admin.sock",
	}

	cmd := &cobra.Command{
		Use:   "sharedingress-config-generator",
		Short: "Shared Ingress Config Generator is a Kubernetes operator for generating shared ingress configurations for hosted clusters",
		Run: func(cmd *cobra.Command, args []string) {
			if err := run(ctrl.SetupSignalHandler(), opts); err != nil {
				setupLog.Error(err, "unable to start manager")
				os.Exit(1)
			}
		},
	}

	cmd.Flags().StringVar(&opts.ConfigPath, "config-path", opts.ConfigPath, "Path where the configuration file for shared ingress HAProxy will be stored")
	cmd.Flags().StringVar(&opts.HAProxySocketPath, "haproxy-socket-path", opts.HAProxySocketPath, "Path to the HAProxy runtime socket file")

	return cmd
}

func run(ctx context.Context, opts Options) error {
	setupLog.Info("Starting sharedingress-config-generator", "ConfigPath", opts.ConfigPath, "HAProxyRuntimeSocketPath", opts.HAProxySocketPath)

	restConfig := ctrl.GetConfigOrDie()
	restConfig.UserAgent = "sharedingress-config-generator"

	mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
		Scheme:         hyperapi.Scheme,
		LeaderElection: false,
	})
	if err != nil {
		return fmt.Errorf("unable to start manager: %w", err)
	}

	// If the config path is a directory, append the default filename
	if configInfo, err := os.Stat(opts.ConfigPath); err == nil && configInfo.IsDir() {
		opts.ConfigPath = filepath.Join(opts.ConfigPath, "haproxy.cfg")
	}

	controller := SharedIngressConfigReconciler{
		configPath:               opts.ConfigPath,
		haProxyRuntimeSocketPath: opts.HAProxySocketPath,
	}
	if err := controller.SetupWithManager(mgr); err != nil {
		return fmt.Errorf("failed to setup sharedingress config controller: %w", err)
	}

	return mgr.Start(ctx)
}
