package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	hyperapi "github.com/openshift/hypershift/api"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
)

func main() {
	cmd := &cobra.Command{
		Use: "webhook",
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
	Namespace string
	CertDir   string
}

func NewStartCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Starts the Hypershift webhook",
	}

	opts := Options{
		Namespace: "hypershift",
		CertDir:   "/var/run/secrets/serving-cert",
	}

	cmd.Flags().StringVar(&opts.CertDir, "cert-dir", opts.CertDir, "Path to the serving key and cert")

	cmd.Run = func(cmd *cobra.Command, args []string) {
		ctx, cancel := context.WithCancel(context.Background())
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT)
		go func() {
			<-sigs
			cancel()
		}()

		if err := run(ctx, opts); err != nil {
			log.Fatal(err)
		}
	}

	return cmd
}

func run(ctx context.Context, opts Options) error {

	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Port:      6443,
		CertDir:   opts.CertDir,
		Scheme:    hyperapi.Scheme,
		Namespace: "hypershift",
	})
	if err != nil {
		return fmt.Errorf("unable to start manager: %w", err)
	}

	if err = (&hyperv1.HostedCluster{}).SetupWebhookWithManager(mgr); err != nil {
		return fmt.Errorf("unable to create controller: %w", err)
	}

	return mgr.Start(ctx)
}
