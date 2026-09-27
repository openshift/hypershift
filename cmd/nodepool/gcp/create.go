package gcp

import (
	"context"
	"fmt"
	"slices"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/nodepool/core"
	"github.com/openshift/hypershift/cmd/util"

	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type GCPNodePoolCreateOptions struct {
	MachineType           string
	Zone                  string
	Subnet                string
	BootDiskSize          int32
	BootDiskType          string
	BootDiskEncryptionKey string
	ServiceAccountEmail   string
	ProvisioningModel     string
	NetworkTags           []string
	ResourceLabels        map[string]string
	Image                 string
}

type RawGCPNodePoolCreateOptions struct {
	*GCPNodePoolCreateOptions
}

func DefaultOptions() *RawGCPNodePoolCreateOptions {
	return &RawGCPNodePoolCreateOptions{
		GCPNodePoolCreateOptions: &GCPNodePoolCreateOptions{
			ProvisioningModel: "Standard",
			ResourceLabels:    make(map[string]string),
			NetworkTags:       []string{},
		},
	}
}

type validatedGCPNodePoolCreateOptions struct {
	*RawGCPNodePoolCreateOptions
}

type ValidatedGCPNodePoolCreateOptions struct {
	*validatedGCPNodePoolCreateOptions
}

type completedGCPNodePoolCreateOptions struct {
	*GCPNodePoolCreateOptions
}

type CompletedGCPNodePoolCreateOptions struct {
	*completedGCPNodePoolCreateOptions
}

func BindDeveloperOptions(opts *RawGCPNodePoolCreateOptions, flags *pflag.FlagSet) {
	flags.StringVar(&opts.MachineType, "machine-type", opts.MachineType, util.GCPMachineTypeHelp)
	flags.StringVar(&opts.Zone, "zone", opts.Zone, "The GCP zone for node instances (e.g. us-central1-a)")
	flags.StringVar(&opts.Subnet, "subnet", opts.Subnet, "The subnet name for node instances")
	flags.Int32Var(&opts.BootDiskSize, "boot-disk-size", opts.BootDiskSize, "The size of the boot disk in GB (minimum 20)")
	flags.StringVar(&opts.BootDiskType, "boot-disk-type", opts.BootDiskType, "The type of the boot disk (e.g. pd-standard, pd-ssd)")
	flags.StringVar(&opts.BootDiskEncryptionKey, "boot-disk-encryption-key", opts.BootDiskEncryptionKey, "The GCP KMS key for boot disk encryption")
	flags.StringVar(&opts.ServiceAccountEmail, "service-account-email", opts.ServiceAccountEmail, "The Google Service Account email for node instances")
	flags.StringVar(&opts.ProvisioningModel, "provisioning-model", opts.ProvisioningModel, "The provisioning model for node instances (Standard, Spot, Preemptible)")
	flags.StringSliceVar(&opts.NetworkTags, "network-tags", opts.NetworkTags, "Network tags to apply to node instances (comma-separated)")
	flags.StringToStringVar(&opts.ResourceLabels, "resource-labels", opts.ResourceLabels, "Resource labels to apply to node instances (key=value pairs)")
	flags.StringVar(&opts.Image, "image", opts.Image, "The GCP boot image for node instances")
}

func (o *RawGCPNodePoolCreateOptions) Validate(_ context.Context, _ *core.CreateNodePoolOptions) (core.NodePoolPlatformCompleter, error) {
	// Reject negative boot disk size (nonsensical input)
	// 0 means "not set" and will use API default (64GB)
	// 1-19 will be rejected by API validation (minimum 20GB)
	if o.BootDiskSize < 0 {
		return nil, fmt.Errorf("boot disk size cannot be negative: %d", o.BootDiskSize)
	}

	return &ValidatedGCPNodePoolCreateOptions{
		validatedGCPNodePoolCreateOptions: &validatedGCPNodePoolCreateOptions{
			RawGCPNodePoolCreateOptions: o,
		},
	}, nil
}

func (o *ValidatedGCPNodePoolCreateOptions) Complete(_ context.Context, _ *core.CreateNodePoolOptions) (core.PlatformOptions, error) {
	// TODO: Add completion logic if needed

	return &CompletedGCPNodePoolCreateOptions{
		completedGCPNodePoolCreateOptions: &completedGCPNodePoolCreateOptions{
			GCPNodePoolCreateOptions: o.GCPNodePoolCreateOptions,
		},
	}, nil
}

func NewCreateCommand(coreOpts *core.CreateNodePoolOptions) *cobra.Command {
	platformOpts := DefaultOptions()
	cmd := &cobra.Command{
		Use:          "gcp",
		Short:        "Creates basic functional NodePool resources for GCP platform",
		SilenceUsage: true,
	}

	BindDeveloperOptions(platformOpts, cmd.Flags())

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		validOpts, err := platformOpts.Validate(ctx, coreOpts)
		if err != nil {
			return err
		}

		opts, err := validOpts.Complete(ctx, coreOpts)
		if err != nil {
			return err
		}
		return coreOpts.CreateRunFunc(opts)(cmd, args)
	}

	return cmd
}

func (o *CompletedGCPNodePoolCreateOptions) UpdateNodePool(ctx context.Context, nodePool *hyperv1.NodePool, hcluster *hyperv1.HostedCluster, _ crclient.Client) error {
	// Build boot disk configuration only if any boot-disk flag is set
	var bootDisk *hyperv1.GCPBootDisk
	if o.BootDiskSize > 0 || len(o.BootDiskType) > 0 || len(o.BootDiskEncryptionKey) > 0 {
		bootDisk = &hyperv1.GCPBootDisk{}
		if o.BootDiskSize > 0 {
			bootDisk.DiskSizeGB = int64(o.BootDiskSize)
		}
		if len(o.BootDiskType) > 0 {
			bootDisk.DiskType = o.BootDiskType
		}
		if len(o.BootDiskEncryptionKey) > 0 {
			bootDisk.EncryptionKey = hyperv1.GCPDiskEncryptionKey{
				KMSKeyName: o.BootDiskEncryptionKey,
			}
		}
	}

	// Build service account configuration
	var serviceAccount *hyperv1.GCPNodeServiceAccount
	if len(o.ServiceAccountEmail) > 0 {
		serviceAccount = &hyperv1.GCPNodeServiceAccount{
			Email: hyperv1.GCPServiceAccountEmail(o.ServiceAccountEmail),
		}
	}

	// Build resource labels (sorted by key for deterministic output)
	keys := make([]string, 0, len(o.ResourceLabels))
	for k := range o.ResourceLabels {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	var resourceLabels []hyperv1.GCPResourceLabel
	for _, key := range keys {
		resourceLabels = append(resourceLabels, hyperv1.GCPResourceLabel{
			Key:   key,
			Value: ptr.To(o.ResourceLabels[key]),
		})
	}

	// Build basic GCP NodePool platform using shared helper
	nodePool.Spec.Platform.GCP = util.BuildGCPNodePoolPlatform(util.GCPNodePoolPlatformOptions{
		Zone:        o.Zone,
		Subnet:      o.Subnet,
		MachineType: o.MachineType,
		Arch:        nodePool.Spec.Arch,
		Image:       o.Image,
	})

	// Add nodepool-specific advanced fields
	nodePool.Spec.Platform.GCP.BootDisk = bootDisk
	nodePool.Spec.Platform.GCP.ServiceAccount = serviceAccount
	nodePool.Spec.Platform.GCP.ResourceLabels = resourceLabels
	nodePool.Spec.Platform.GCP.NetworkTags = convertStringSliceToResourceNames(o.NetworkTags)
	nodePool.Spec.Platform.GCP.ProvisioningModel = hyperv1.GCPProvisioningModel(o.ProvisioningModel)

	return nil
}

func convertStringSliceToResourceNames(tags []string) []hyperv1.GCPResourceName {
	var result []hyperv1.GCPResourceName
	for _, tag := range tags {
		result = append(result, hyperv1.GCPResourceName(tag))
	}
	return result
}

func (o *CompletedGCPNodePoolCreateOptions) Type() hyperv1.PlatformType {
	return hyperv1.GCPPlatform
}
