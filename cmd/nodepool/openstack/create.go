package openstack

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/nodepool/core"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func DefaultOptions() *RawOpenStackPlatformCreateOptions {
	return &RawOpenStackPlatformCreateOptions{OpenStackPlatformOptions: &OpenStackPlatformOptions{}}
}

type OpenStackPlatformOptions struct {
	Flavor         string
	ImageName      string
	AvailabityZone string
}

// completedCreateOptions is a private wrapper that enforces a call of Complete() before nodepool creation can be invoked.
type completedOpenStackPlatformCreateOptions struct {
	*OpenStackPlatformOptions
}

type OpenStackPlatformCreateOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*completedOpenStackPlatformCreateOptions
}

type RawOpenStackPlatformCreateOptions struct {
	*OpenStackPlatformOptions
}

type validatedOpenStackPlatformCreateOptions struct {
	*RawOpenStackPlatformCreateOptions
}

type ValidatedOpenStackPlatformCreateOptions struct {
	*validatedOpenStackPlatformCreateOptions
}

func (o *ValidatedOpenStackPlatformCreateOptions) Complete() (*OpenStackPlatformCreateOptions, error) {
	return &OpenStackPlatformCreateOptions{
		completedOpenStackPlatformCreateOptions: &completedOpenStackPlatformCreateOptions{
			OpenStackPlatformOptions: o.OpenStackPlatformOptions,
		},
	}, nil
}

func (o *RawOpenStackPlatformCreateOptions) Validate() (*ValidatedOpenStackPlatformCreateOptions, error) {
	if o.Flavor == "" {
		return nil, fmt.Errorf("flavor is required")
	}

	// TODO(emilien): Remove that validation once we support using the image from the release payload.
	// This will be possible when CAPO supports managing images in the OpenStack cluster:
	// https://github.com/kubernetes-sigs/cluster-api-provider-openstack/pull/2130
	// For 4.17 we might leave this as is and let the user provide the image name as
	// we plan to deliver the OpenStack provider as a dev preview.
	if o.ImageName == "" {
		return nil, fmt.Errorf("image name is required")
	}

	return &ValidatedOpenStackPlatformCreateOptions{
		validatedOpenStackPlatformCreateOptions: &validatedOpenStackPlatformCreateOptions{
			RawOpenStackPlatformCreateOptions: o,
		},
	}, nil
}

func BindOptions(opts *RawOpenStackPlatformCreateOptions, flags *pflag.FlagSet) {
	bindCoreOptions(opts, flags)
}

func bindCoreOptions(opts *RawOpenStackPlatformCreateOptions, flags *pflag.FlagSet) {
	flags.StringVar(&opts.Flavor, "openstack-node-flavor", opts.Flavor, "The flavor to use for the nodepool (required)")
	flags.StringVar(&opts.ImageName, "openstack-node-image-name", opts.ImageName, "The image name to use for the nodepool (required)")
	flags.StringVar(&opts.AvailabityZone, "openstack-node-availability-zone", opts.AvailabityZone, "The availability zone to use for the nodepool (optional)")
}

func NewCreateCommand(coreOpts *core.CreateNodePoolOptions) *cobra.Command {
	platformOpts := DefaultOptions()

	cmd := &cobra.Command{
		Use:          "openstack",
		Short:        "Creates basic functional NodePool resources for OpenStack platform",
		SilenceUsage: true,
	}
	BindOptions(platformOpts, cmd.Flags())
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		validOpts, err := platformOpts.Validate()
		if err != nil {
			return err
		}

		opts, err := validOpts.Complete()
		if err != nil {
			return err
		}
		return coreOpts.CreateRunFunc(opts)(cmd, args)
	}

	return cmd
}

func (o *OpenStackPlatformCreateOptions) UpdateNodePool(_ context.Context, nodePool *hyperv1.NodePool, _ *hyperv1.HostedCluster, _ crclient.Client) error {
	nodePool.Spec.Platform.Type = o.Type()
	nodePool.Spec.Platform.OpenStack = o.NodePoolPlatform()
	return nil
}

func (o *OpenStackPlatformCreateOptions) Type() hyperv1.PlatformType {
	return hyperv1.OpenStackPlatform
}

func (o *OpenStackPlatformCreateOptions) NodePoolPlatform() *hyperv1.OpenStackNodePoolPlatform {
	return &hyperv1.OpenStackNodePoolPlatform{
		Flavor:           o.Flavor,
		ImageName:        o.ImageName,
		AvailabilityZone: o.AvailabityZone,
	}
}
