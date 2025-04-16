package syncfgconfigmap

// sync-fg-configmap applies a generated feature gate configmap from a file into a control plane configmap

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	cmdutil "github.com/openshift/hypershift/cmd/util"

	corev1 "k8s.io/api/core/v1"

	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/spf13/cobra"
)

type syncFGConfigMapOptions struct {
	File           string
	Namespace      string
	Name           string
	PayloadVersion string
}

func NewRunCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync-fg-configmap",
		Short: "Creates or updates feature-gate configmap",
		Long:  `Creates or updates a feature-gate configmap that contains a feature-gate YAML and is annotated with the corresponding release version`,
	}

	opts := syncFGConfigMapOptions{
		File:           "/manifests/99_feature-gate.yaml",
		Namespace:      os.Getenv("NAMESPACE"),
		Name:           "feature-gate",
		PayloadVersion: os.Getenv("PAYLOAD_VERSION"),
	}
	cmd.Flags().StringVar(&opts.File, "file", opts.File, "The path path to the file that contains the feature gate YAML to apply.")
	cmd.Flags().StringVar(&opts.Namespace, "namespace", opts.Namespace, "The control plane namespace for the feature gate configmap.")
	cmd.Flags().StringVar(&opts.Name, "name", opts.Name, "The name of the feature gate configmap.")
	cmd.Flags().StringVar(&opts.PayloadVersion, "payload-version", opts.PayloadVersion, "The payload version of the control plane.")
	cmd.Run = func(cmd *cobra.Command, args []string) {
		ctx, cancel := context.WithCancel(context.Background())
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT)
		go func() {
			<-sigs
			cancel()
		}()

		if err := opts.run(ctx); err != nil {
			log.Fatal(err)
			os.Exit(1)
		}
	}

	return cmd
}

func (o *syncFGConfigMapOptions) run(ctx context.Context) error {
	content, err := os.ReadFile(o.File)
	if err != nil {
		return fmt.Errorf("failed to read input file %s: %w", o.File, err)
	}

	c, err := cmdutil.GetClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}

	cm := &corev1.ConfigMap{}
	cm.Name = o.Name
	cm.Namespace = o.Namespace

	result, err := controllerutil.CreateOrUpdate(ctx, c, cm, func() error {
		if cm.Annotations == nil {
			cm.Annotations = map[string]string{}
		}
		cm.Annotations["hypershift.openshift.io/payload-version"] = o.PayloadVersion
		cm.Data = map[string]string{
			"feature-gate.yaml": string(content),
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to create or update configmap: %w", err)
	}
	log.Printf("configmap %s/%s applied: %v", cm.Namespace, cm.Name, result)
	return nil
}
