package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	hyperapi "github.com/openshift/hypershift/api"
	"github.com/openshift/hypershift/ignition-server/controllers"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/util"
	"github.com/spf13/cobra"
	"go.uber.org/zap/zapcore"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

type RunLocalIgnitionProviderOptions struct {
	Namespace           string
	Image               string
	TokenSecret         string
	WorkDir             string
	FeatureGateManifest string
}

func NewRunLocalIgnitionProviderCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run-local-ign-provider",
		Short: "Executes payload generation once for debugging and exits",
	}

	opts := RunLocalIgnitionProviderOptions{}

	cmd.Flags().StringVar(&opts.Namespace, "namespace", opts.Namespace, "Namespace")
	cmd.Flags().StringVar(&opts.Image, "image", opts.Image, "Release image")
	cmd.Flags().StringVar(&opts.TokenSecret, "token-secret", opts.TokenSecret, "Token secret name")
	cmd.Flags().StringVar(&opts.WorkDir, "dir", opts.WorkDir, "Working directory (default: temporary dir)")
	cmd.Flags().StringVar(&opts.FeatureGateManifest, "feature-gate-manifest", opts.FeatureGateManifest, "Path to a rendered featuregates.config.openshift.io/v1 manifest")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		ctx, cancel := context.WithCancel(context.Background())
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT)
		go func() {
			<-sigs
			cancel()
		}()
		ctrl.SetLogger(zap.New(zap.UseDevMode(true), zap.JSONEncoder(func(o *zapcore.EncoderConfig) {
			o.EncodeTime = zapcore.RFC3339TimeEncoder
		})))
		return opts.Run(ctx)
	}

	return cmd
}

func (o *RunLocalIgnitionProviderOptions) Run(ctx context.Context) error {
	start := time.Now()
	log := ctrl.Log.WithName("local-ign-provider")
	cfg, err := ctrl.GetConfig()
	if err != nil {
		return err
	}
	cfg.QPS = 100
	cfg.Burst = 100
	cl, err := client.New(cfg, client.Options{Scheme: hyperapi.Scheme})
	if err != nil {
		return fmt.Errorf("unable to get kubernetes client: %w", err)
	}
	if err != nil {
		return err
	}
	token := &corev1.Secret{}
	if err := cl.Get(ctx, client.ObjectKey{Namespace: o.Namespace, Name: o.TokenSecret}, token); err != nil {
		return err
	}
	compressedConfig := token.Data[controllers.TokenSecretConfigKey]
	config, err := util.DecodeAndDecompress(compressedConfig)
	if err != nil {
		return nil
	}
	// Set up the cache directory
	cacheDir, err := os.MkdirTemp("", "cache")
	if err != nil {
		return fmt.Errorf("failed to create cache directory: %w", err)
	}
	log.Info("Using cache directory", "directory", cacheDir)
	imageFileCache, err := controllers.NewImageFileCache(cacheDir)
	if err != nil {
		return fmt.Errorf("unable to create image file cache: %w", err)
	}

	p := &controllers.LocalIgnitionProvider{
		Client:              cl,
		ReleaseProvider:     &releaseinfo.ProviderWithOpenShiftImageRegistryOverridesDecorator{},
		CloudProvider:       "",
		Namespace:           o.Namespace,
		WorkDir:             o.WorkDir,
		PreserveOutput:      true,
		ImageFileCache:      imageFileCache,
		FeatureGateManifest: o.FeatureGateManifest,
	}

	payload, err := p.GetPayload(ctx, o.Image, config.String(), "", "")
	if err != nil {
		return err
	}

	payloadFile, err := os.CreateTemp(o.WorkDir, "payload-")
	if err != nil {
		return err
	}
	defer payloadFile.Close()
	if err := os.WriteFile(payloadFile.Name(), payload, 0644); err != nil {
		return fmt.Errorf("failed to write payload file: %w", err)
	}

	log.Info("Wrote payload", "duration", time.Since(start).Round(time.Second).String(), "output", payloadFile.Name())
	return nil
}
