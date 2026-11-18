package gcpnodeterminationhandler

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/openshift/hypershift/support/supportedversion"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/spf13/cobra"
	"go.uber.org/zap/zapcore"
)

const defaultMetadataURL = "http://metadata.google.internal/computeMetadata/v1/instance/preempted"
const defaultMetadataTimeout = time.Second

func NewStartCommand() *cobra.Command {
	log.SetLogger(zap.New(zap.JSONEncoder(func(o *zapcore.EncoderConfig) {
		o.EncodeTime = zapcore.RFC3339TimeEncoder
	})))
	l := log.Log.WithName(componentName)

	cmd := &cobra.Command{
		Use:   componentName,
		Short: "Runs the GCP node termination handler for hosted cluster worker nodes.",
	}

	var (
		nodeName     string
		metadataURL  string
		pollInterval time.Duration
		drainTimeout time.Duration
	)

	cmd.Flags().StringVar(&nodeName, "node-name", os.Getenv("NODE_NAME"), "Name of the node this handler is running on.")
	cmd.Flags().StringVar(&metadataURL, "metadata-url", defaultMetadataURL, "GCP metadata server URL to query for preemption status.")
	cmd.Flags().DurationVar(&pollInterval, "poll-interval", 2*time.Second, "Interval between metadata preemption checks.")
	cmd.Flags().DurationVar(&drainTimeout, "drain-timeout", 20*time.Second, "Maximum time to spend draining the node after preemption is detected.")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		l.Info("Starting GCP node termination handler", "version", supportedversion.String())
		if nodeName == "" {
			return fmt.Errorf("--node-name or NODE_NAME is required")
		}

		cfg, err := rest.InClusterConfig()
		if err != nil {
			return fmt.Errorf("failed to get in-cluster config: %w", err)
		}
		kubeClient, err := kubernetes.NewForConfig(cfg)
		if err != nil {
			return fmt.Errorf("failed to create kubernetes client: %w", err)
		}

		ctx, cancel := context.WithCancel(cmd.Context())
		defer cancel()

		signalCh := make(chan os.Signal, 1)
		signal.Notify(signalCh, syscall.SIGTERM, syscall.SIGINT)
		defer signal.Stop(signalCh)
		go func() {
			select {
			case sig := <-signalCh:
				l.Info("Received shutdown signal", "signal", sig)
				cancel()
			case <-ctx.Done():
			}
		}()

		h := &handler{
			nodeName:     nodeName,
			metadataURL:  metadataURL,
			pollInterval: pollInterval,
			drainTimeout: drainTimeout,
			kubeClient:   kubeClient,
			httpClient:   &http.Client{Timeout: defaultMetadataTimeout},
			log:          l,
		}
		h.drainer = &kubectlDrainer{client: kubeClient, log: l}
		return h.run(ctx)
	}

	return cmd
}
