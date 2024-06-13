package none

import (
	"context"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/spf13/cobra"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/hypershift/cmd/cluster/core"
)

type CreateOptions struct {
	APIServerAddress          string
	ExposeThroughLoadBalancer bool
}

func (o *CreateOptions) Validate(ctx context.Context, opts *core.CreateOptions) error {
	return nil
}

func (o *CreateOptions) Complete(ctx context.Context, opts *core.CreateOptions) error {
	var err error
	if o.APIServerAddress == "" && !o.ExposeThroughLoadBalancer {
		o.APIServerAddress, err = core.GetAPIServerAddressByNode(ctx, opts.Log)
	}
	return err
}

func (o *CreateOptions) ApplyPlatformSpecifics(cluster *hyperv1.HostedCluster) error {
	if cluster.Spec.DNS.BaseDomain == "" {
		cluster.Spec.DNS.BaseDomain = "example.com"
	}
	cluster.Spec.Platform = hyperv1.PlatformSpec{
		Type: hyperv1.NonePlatform,
	}
	if o.APIServerAddress != "" {
		cluster.Spec.Services = core.GetServicePublishingStrategyMappingByAPIServerAddress(o.APIServerAddress, cluster.Spec.Networking.NetworkType)
	} else {
		cluster.Spec.Services = core.GetIngressServicePublishingStrategyMapping(cluster.Spec.Networking.NetworkType, false)
	}
	return nil
}

func (o *CreateOptions) GenerateNodePools(constructor core.DefaultNodePoolConstructor) []*hyperv1.NodePool {
	nodePool := constructor(hyperv1.NonePlatform, "")
	if nodePool.Spec.Management.UpgradeType == "" {
		nodePool.Spec.Management.UpgradeType = hyperv1.UpgradeTypeInPlace
	}
	return []*hyperv1.NodePool{nodePool}
}

func (o *CreateOptions) GenerateResources() ([]client.Object, error) {
	return nil, nil
}

var _ core.Platform = (*CreateOptions)(nil)

func NewCreateCommand(opts *core.CreateOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "none",
		Short:        "Creates basic functional HostedCluster resources on None",
		SilenceUsage: true,
	}

	noneOpts := &CreateOptions{}

	cmd.Flags().StringVar(&noneOpts.APIServerAddress, "external-api-server-address", noneOpts.APIServerAddress, "The external API Server Address when using platform none")
	cmd.Flags().BoolVar(&noneOpts.ExposeThroughLoadBalancer, "expose-through-load-balancer", noneOpts.ExposeThroughLoadBalancer, "If the services should be exposed through LoadBalancer. If not set, nodeports will be used instead")

	cmd.MarkPersistentFlagRequired("pull-secret")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		if opts.Timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
			defer cancel()
		}

		if err := CreateCluster(ctx, opts, noneOpts); err != nil {
			opts.Log.Error(err, "Failed to create cluster")
			return err
		}
		return nil
	}

	return cmd
}

func CreateCluster(ctx context.Context, opts *core.CreateOptions, noneOpts *CreateOptions) error {
	return core.CreateCluster(ctx, opts, noneOpts)
}
