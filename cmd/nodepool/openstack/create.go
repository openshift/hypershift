package openstack

import (
	"context"
	"fmt"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/kubevirt/params"
	"github.com/openshift/hypershift/cmd/nodepool/core"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func DefaultOptions() *RawOpenStackPlatformCreateOptions {
	return &RawOpenStackPlatformCreateOptions{OpenStackPlatformOptions: &OpenStackPlatformOptions{}}
}

type PortOpts struct {
	NetworkID           string `param:"network-id"`
	VNICType            string `param:"vnic-type"`
	DisablePortSecurity string `param:"disable-port-security"`
	AddressPairs        string `param:"address-pairs"`
}

type OpenStackPlatformOptions struct {
	Flavor    string
	ImageName string
}

// completedCreateOptions is a private wrapper that enforces a call of Complete() before nodepool creation can be invoked.
type completedOpenStackPlatformCreateOptions struct {
	*OpenStackPlatformOptions
	AdditionalPorts []hyperv1.PortOpts
}

type OpenStackPlatformCreateOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*completedOpenStackPlatformCreateOptions
}

type RawOpenStackPlatformCreateOptions struct {
	*OpenStackPlatformOptions
	AdditionalPorts []string
}

type validatedOpenStackPlatformCreateOptions struct {
	*RawOpenStackPlatformCreateOptions
}

type ValidatedOpenStackPlatformCreateOptions struct {
	*validatedOpenStackPlatformCreateOptions
}

func (o *ValidatedOpenStackPlatformCreateOptions) Complete() (*OpenStackPlatformCreateOptions, error) {
	additionalPorts := []hyperv1.PortOpts{}
	for _, additionalPortOptsRaw := range o.AdditionalPorts {
		additionalPortOpts := PortOpts{}
		if err := params.Map("openstack-node-additional-port", additionalPortOptsRaw, &additionalPortOpts); err != nil {
			return nil, err
		}
		additionalPorts = append(additionalPorts, hyperv1.PortOpts{
			Network: &hyperv1.NetworkParam{
				ID: &additionalPortOpts.NetworkID,
			},
			ResolvedPortSpecFields: hyperv1.ResolvedPortSpecFields{
				AllowedAddressPairs: getAddressPairs(additionalPortOpts.AddressPairs),
				DisablePortSecurity: strToBool(additionalPortOpts.DisablePortSecurity),
				VNICType:            additionalPortOpts.VNICType,
			},
		})
	}

	return &OpenStackPlatformCreateOptions{
		completedOpenStackPlatformCreateOptions: &completedOpenStackPlatformCreateOptions{
			OpenStackPlatformOptions: o.OpenStackPlatformOptions,
			AdditionalPorts:          additionalPorts,
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

	if err := validateAdditionalPorts(o.AdditionalPorts); err != nil {
		return nil, err
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
	flags.StringArrayVar(&opts.AdditionalPorts, "openstack-node-additional-port", opts.AdditionalPorts, fmt.Sprintf(`Specify additional port that should be attached to the nodes, the "network-id" field should point to an existing neutron network ID and the "vnic-type" is the type of the port to create, it can be specified multiple times to attach to multiple ports. Supported parameters: %s, example: "network-id:40a355cb-596d-495c-8766-419d98cadd57,vnic-type:direct"`, params.Supported(PortOpts{})))

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
	nodePool := &hyperv1.OpenStackNodePoolPlatform{
		Flavor:          o.Flavor,
		ImageName:       o.ImageName,
		AdditionalPorts: o.AdditionalPorts,
	}

	return nodePool
}

func validateAdditionalPorts(additionalPorts []string) error {
	for _, additionalPortOptsRaw := range additionalPorts {
		additionalPortOpts := PortOpts{}
		if err := params.Map("openstack-node-additional-port", additionalPortOptsRaw, &additionalPortOpts); err != nil {
			return err
		}
		if additionalPortOpts.NetworkID == "" {
			return fmt.Errorf("--openstack-node-additional-port requires network-id to be set")
		}
	}
	return nil
}

func strToBool(s string) bool {
	return s == "true"
}

func getAddressPairs(addressPairs string) []hyperv1.AddressPair {
	if addressPairs == "" {
		return []hyperv1.AddressPair{}
	}
	separetedAddressPairs := strings.Split(addressPairs, "-")
	resolvedAddressPairs := []hyperv1.AddressPair{}
	for _, addressPair := range separetedAddressPairs {
		resolvedAddressPairs = append(resolvedAddressPairs, hyperv1.AddressPair{IPAddress: addressPair})
	}
	return resolvedAddressPairs
}
