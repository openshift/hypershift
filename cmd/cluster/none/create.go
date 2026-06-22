package none

import (
	"context"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/core"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type RawCreateOptions struct {
	APIServerAddress          string
	ExposeThroughLoadBalancer bool
}

// validatedCreateOptions is a private wrapper that enforces a call of Validate() before Complete() can be invoked.
type validatedCreateOptions struct {
	*RawCreateOptions
}

type ValidatedCreateOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*validatedCreateOptions
}

func (o *RawCreateOptions) Validate(ctx context.Context, opts *core.CreateOptions) (core.PlatformCompleter, error) {
	return &ValidatedCreateOptions{
		validatedCreateOptions: &validatedCreateOptions{
			RawCreateOptions: o,
		},
	}, nil
}

// completedCreateOptions is a private wrapper that enforces a call of Complete() before cluster creation can be invoked.
type completedCreateOptions struct {
	*ValidatedCreateOptions
}

type CreateOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*completedCreateOptions
}

func (o *ValidatedCreateOptions) Complete(ctx context.Context, opts *core.CreateOptions) (core.Platform, error) {
	var err error
	if o.APIServerAddress == "" && !o.ExposeThroughLoadBalancer {
		o.APIServerAddress, err = core.GetAPIServerAddressByNode(ctx, opts.Log)
	}
	return &CreateOptions{
		completedCreateOptions: &completedCreateOptions{
			ValidatedCreateOptions: o,
		},
	}, err
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

func DefaultOptions() *RawCreateOptions {
	return &RawCreateOptions{}
}

func BindOptions(opts *RawCreateOptions, flags *pflag.FlagSet) {
	flags.StringVar(&opts.APIServerAddress, "external-api-server-address", opts.APIServerAddress, "The external API Server Address when using platform none")
	flags.BoolVar(&opts.ExposeThroughLoadBalancer, "expose-through-load-balancer", opts.ExposeThroughLoadBalancer, "If the services should be exposed through LoadBalancer. If not set, nodeports will be used instead")
}

func NewCreateCommand(opts *core.RawCreateOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "none",
		Short:        "Creates basic functional HostedCluster resources on None",
		SilenceUsage: true,
	}

	noneOpts := DefaultOptions()
	BindOptions(noneOpts, cmd.Flags())
	_ = cmd.MarkPersistentFlagRequired("pull-secret")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		if opts.Timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
			defer cancel()
		}

		if err := core.CreateCluster(ctx, opts, noneOpts); err != nil {
			opts.Log.Error(err, "Failed to create cluster")
			return err
		}
		return nil
	}

	return cmd
}
