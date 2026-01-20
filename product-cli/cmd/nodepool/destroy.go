package nodepool

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/log"
	"github.com/openshift/hypershift/cmd/util"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/spf13/cobra"
)

type DestroyNodePoolOptions struct {
	Name      string
	Namespace string
}

func NewDestroyCommand() *cobra.Command {
	opts := &DestroyNodePoolOptions{
		Namespace: "clusters",
	}

	cmd := &cobra.Command{
		Use:          "nodepool",
		Short:        "Destroys a NodePool",
		SilenceUsage: true,
	}

	cmd.Flags().StringVar(&opts.Name, "name", opts.Name, "The name of the NodePool to destroy (required)")
	cmd.Flags().StringVar(&opts.Namespace, "namespace", opts.Namespace, "The namespace of the NodePool")

	_ = cmd.MarkFlagRequired("name")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return opts.Run(cmd.Context())
	}

	return cmd
}

func (o *DestroyNodePoolOptions) Run(ctx context.Context) error {
	client, err := util.GetClient()
	if err != nil {
		return err
	}

	nodePool := &hyperv1.NodePool{}
	nodePool.Name = o.Name
	nodePool.Namespace = o.Namespace

	if err := client.Delete(ctx, nodePool); err != nil {
		if apierrors.IsNotFound(err) {
			log.Log.Info("NodePool already deleted or not found", "name", o.Name, "namespace", o.Namespace)
			return nil
		}
		return fmt.Errorf("failed to delete NodePool %s/%s: %w", o.Namespace, o.Name, err)
	}

	log.Log.Info("NodePool deleted successfully", "name", o.Name, "namespace", o.Namespace)
	return nil
}
