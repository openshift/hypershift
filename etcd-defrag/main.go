package etcddefrag

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/openshift/hypershift/pkg/version"
	hyperapi "github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/upsert"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/spf13/cobra"
	"go.uber.org/zap/zapcore"
)

type Options struct {
	Namespace string
}

func NewStartCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "etcd-defrag-controller",
		Short: "Starts the etcd defrag controller",
	}

	opts := Options{
		Namespace: "",
	}

	cmd.Flags().StringVar(&opts.Namespace, "namespace", os.Getenv("MY_NAMESPACE"), "The namespace this operator lives in (required)")

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
			os.Exit(1)
		}
	}

	return cmd
}

func run(ctx context.Context, opts Options) error {
	logger := zap.New(zap.UseDevMode(true), zap.JSONEncoder(func(o *zapcore.EncoderConfig) {
		o.EncodeTime = zapcore.RFC3339TimeEncoder
	}))
	ctrl.SetLogger(logger)
	logger.Info("Starting etcd-defrag-controller", "version", version.String(), "namespace", opts.Namespace)
	leaseDuration := time.Minute * 5
	renewDeadline := time.Minute * 4
	retryPeriod := time.Second * 30

	restConfig := ctrl.GetConfigOrDie()
	restConfig.UserAgent = "etcd-defrag-controller"
	mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
		Scheme:                        hyperapi.Scheme,
		LeaderElection:                true,
		LeaderElectionID:              "etcd-defrag-controller-leader-elect",
		LeaderElectionResourceLock:    "leases",
		LeaderElectionNamespace:       opts.Namespace,
		LeaderElectionReleaseOnCancel: true,
		LeaseDuration:                 &leaseDuration,
		RenewDeadline:                 &renewDeadline,
		RetryPeriod:                   &retryPeriod,
		Cache: cache.Options{
			DefaultNamespaces: map[string]cache.Config{opts.Namespace: {}},
		},
	})
	if err != nil {
		return fmt.Errorf("unable to start manager: %w", err)
	}

	controllerName := "EtcdDefragController"
	if err := (&DefragController{
		Client:                 mgr.GetClient(),
		log:                    logger,
		ControllerName:         controllerName,
		CreateOrUpdateProvider: upsert.New(false),
	}).SetupWithManager(ctx, mgr); err != nil {
		return fmt.Errorf("unable to create controller: %s: %w", controllerName, err)
	}

	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		return fmt.Errorf("problem running manager: %w", err)
	}
	return nil
}
