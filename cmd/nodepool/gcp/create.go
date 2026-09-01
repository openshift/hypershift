package gcp

import (
	"context"
	"fmt"
	"regexp"
	"slices"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/nodepool/core"

	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const (
	defaultGCPMachineType = "n2-standard-4"
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
			BootDiskSize:      120,
			BootDiskType:      "pd-standard",
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
	flags.StringVar(&opts.MachineType, "machine-type", opts.MachineType, "The GCP machine type for node instances (e.g. n2-standard-4)")
	flags.StringVar(&opts.Zone, "zone", opts.Zone, "The GCP zone for node instances (e.g. us-central1-a)")
	flags.StringVar(&opts.Subnet, "subnet", opts.Subnet, "The subnet name for node instances")
	flags.Int32Var(&opts.BootDiskSize, "boot-disk-size", opts.BootDiskSize, "The size of the boot disk in GB (minimum 8)")
	flags.StringVar(&opts.BootDiskType, "boot-disk-type", opts.BootDiskType, "The type of the boot disk (e.g. pd-standard, pd-ssd)")
	flags.StringVar(&opts.BootDiskEncryptionKey, "boot-disk-encryption-key", opts.BootDiskEncryptionKey, "The GCP KMS key for boot disk encryption")
	flags.StringVar(&opts.ServiceAccountEmail, "service-account-email", opts.ServiceAccountEmail, "The Google Service Account email for node instances")
	flags.StringVar(&opts.ProvisioningModel, "provisioning-model", opts.ProvisioningModel, "The provisioning model for node instances (Standard, Spot, Preemptible)")
	flags.StringSliceVar(&opts.NetworkTags, "network-tags", opts.NetworkTags, "Network tags to apply to node instances (comma-separated)")
	flags.StringToStringVar(&opts.ResourceLabels, "resource-labels", opts.ResourceLabels, "Resource labels to apply to node instances (key=value pairs)")
	flags.StringVar(&opts.Image, "image", opts.Image, "The GCP boot image for node instances")
}

func (o *RawGCPNodePoolCreateOptions) Validate(_ context.Context, _ *core.CreateNodePoolOptions) (core.NodePoolPlatformCompleter, error) {
	// Validate boot disk size
	if o.BootDiskSize < 8 {
		return nil, fmt.Errorf("boot disk size must be at least 8 GB, got %d", o.BootDiskSize)
	}

	// Validate zone format: {region}-{zone} (e.g., us-central1-a)
	zoneRegex := regexp.MustCompile(`^[a-z]+(?:-[a-z0-9]+)*-[a-z]$`)
	if len(o.Zone) > 0 && !zoneRegex.MatchString(o.Zone) {
		return nil, fmt.Errorf("zone must be in the form of region-zone (e.g., us-central1-a), got %q", o.Zone)
	}

	// Validate machine type format
	machineTypeRegex := regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	if len(o.MachineType) > 0 && !machineTypeRegex.MatchString(o.MachineType) {
		return nil, fmt.Errorf("machine type must contain only lowercase letters, digits, and hyphens, got %q", o.MachineType)
	}

	// Validate provisioning model
	validProvisioningModels := []string{"Standard", "Spot", "Preemptible"}
	if len(o.ProvisioningModel) > 0 && !slices.Contains(validProvisioningModels, o.ProvisioningModel) {
		return nil, fmt.Errorf("provisioning model must be one of %v, got %q", validProvisioningModels, o.ProvisioningModel)
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
	// Set machine type with defaults based on architecture
	machineType := o.MachineType
	if len(machineType) == 0 {
		switch nodePool.Spec.Arch {
		case "amd64":
			machineType = defaultGCPMachineType
		case "arm64":
			// GCP doesn't have direct arm64 equivalent, use default for now
			machineType = defaultGCPMachineType
		default:
			machineType = defaultGCPMachineType
		}
	}

	// Build boot disk configuration
	bootDisk := &hyperv1.GCPBootDisk{
		DiskSizeGB: int64(o.BootDiskSize),
		DiskType:   o.BootDiskType,
	}
	if len(o.BootDiskEncryptionKey) > 0 {
		bootDisk.EncryptionKey = hyperv1.GCPDiskEncryptionKey{
			KMSKeyName: o.BootDiskEncryptionKey,
		}
	}

	// Build service account configuration
	var serviceAccount *hyperv1.GCPNodeServiceAccount
	if len(o.ServiceAccountEmail) > 0 {
		serviceAccount = &hyperv1.GCPNodeServiceAccount{
			Email: hyperv1.GCPServiceAccountEmail(o.ServiceAccountEmail),
		}
	}

	// Build resource labels
	var resourceLabels []hyperv1.GCPResourceLabel
	for key, value := range o.ResourceLabels {
		labelValue := value
		resourceLabels = append(resourceLabels, hyperv1.GCPResourceLabel{
			Key:   key,
			Value: ptr.To(labelValue),
		})
	}

	// Convert provisioning model string to enum
	var provisioningModel hyperv1.GCPProvisioningModel
	switch o.ProvisioningModel {
	case "Spot":
		provisioningModel = hyperv1.GCPProvisioningModelSpot
	case "Preemptible":
		provisioningModel = hyperv1.GCPProvisioningModelPreemptible
	default:
		provisioningModel = hyperv1.GCPProvisioningModelStandard
	}

	// Build GCP NodePool platform configuration
	nodePool.Spec.Platform.GCP = &hyperv1.GCPNodePoolPlatform{
		MachineType:       machineType,
		Zone:              o.Zone,
		Subnet:            hyperv1.GCPResourceName(o.Subnet),
		BootDisk:          bootDisk,
		ServiceAccount:    serviceAccount,
		ResourceLabels:    resourceLabels,
		NetworkTags:       convertStringSliceToResourceNames(o.NetworkTags),
		ProvisioningModel: provisioningModel,
	}

	// Set image if provided
	if len(o.Image) > 0 {
		nodePool.Spec.Platform.GCP.Image = o.Image
	}

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
