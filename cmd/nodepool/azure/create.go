package azure

import (
	"context"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/nodepool/core"
	"github.com/openshift/hypershift/cmd/util"
	"k8s.io/utils/ptr"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type AzurePlatformCreateOptions struct {
	InstanceType           string
	DiskSize               int32
	AvailabilityZone       string
	DiskEncryptionSetID    string
	EnableEphemeralOSDisk  bool
	DiskStorageAccountType string
	SubnetID               string
	ImageID                string
}

type AzureMarketPlaceImageInfo struct {
	MarketplacePublisher string
	MarketplaceOffer     string
	MarketplaceSKU       string
	MarketplaceVersion   string
}

type RawAzurePlatformCreateOptions struct {
	*AzurePlatformCreateOptions
	*AzureMarketPlaceImageInfo
}

func DefaultOptions() *RawAzurePlatformCreateOptions {
	return &RawAzurePlatformCreateOptions{
		AzurePlatformCreateOptions: &AzurePlatformCreateOptions{
			InstanceType: "Standard_D4s_v4",
			DiskSize:     120,
		},
		AzureMarketPlaceImageInfo: &AzureMarketPlaceImageInfo{},
	}
}

func BindOptions(opts *RawAzurePlatformCreateOptions, flags *pflag.FlagSet) {
	bindCoreOptions(opts, flags)
}

func bindCoreOptions(opts *RawAzurePlatformCreateOptions, flags *pflag.FlagSet) {
	flags.StringVar(&opts.InstanceType, "instance-type", opts.InstanceType, "The instance type to use for the nodepool")
	flags.Int32Var(&opts.DiskSize, "root-disk-size", opts.DiskSize, "The size of the root disk for machines in the NodePool (minimum 16)")
	flags.StringVar(&opts.AvailabilityZone, "availability-zone", opts.AvailabilityZone, "The availabilityZone for the nodepool. Must be left unspecified if in a region that doesn't support AZs")
	flags.StringVar(&opts.DiskEncryptionSetID, "disk-encryption-set-id", opts.DiskEncryptionSetID, "The Disk Encryption Set ID to use to encrypt the OS disks for the VMs.")
	flags.BoolVar(&opts.EnableEphemeralOSDisk, "enable-ephemeral-disk", opts.EnableEphemeralOSDisk, "If enabled, the Azure VMs in the NodePool will be setup with ephemeral OS disks")
	flags.StringVar(&opts.DiskStorageAccountType, "disk-storage-account-type", opts.DiskStorageAccountType, "The disk storage account type for the OS disks for the VMs.")
	flags.StringVar(&opts.SubnetID, "nodepool-subnet-id", opts.SubnetID, "The subnet id where the VMs will be placed.")
	flags.StringVar(&opts.ImageID, "image-id", opts.ImageID, "The Image ID to boot the VMs with.")
	flags.StringVar(&opts.MarketplacePublisher, "marketplace-publisher", opts.MarketplacePublisher, "The Azure Marketplace image publisher.")
	flags.StringVar(&opts.MarketplaceOffer, "marketplace-offer", opts.MarketplaceOffer, "The Azure Marketplace image offer.")
	flags.StringVar(&opts.MarketplaceSKU, "marketplace-sku", opts.MarketplaceSKU, "The Azure Marketplace image SKU.")
	flags.StringVar(&opts.MarketplaceVersion, "marketplace-version", opts.MarketplaceVersion, "The Azure Marketplace image version.")
}

func BindDeveloperOptions(opts *RawAzurePlatformCreateOptions, flags *pflag.FlagSet) {
	bindCoreOptions(opts, flags)
}

// validatedAzurePlatformCreateOptions is a private wrapper that enforces a call of Validate() before Complete() can be invoked.
type validatedAzurePlatformCreateOptions struct {
	*RawAzurePlatformCreateOptions
}

type ValidatedAzurePlatformCreateOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*validatedAzurePlatformCreateOptions
}

// completedCreateOptions is a private wrapper that enforces a call of Complete() before nodepool creation can be invoked.
type completetedAzurePlatformCreateOptions struct {
	*AzurePlatformCreateOptions
	*AzureMarketPlaceImageInfo
}

type CompletedAzurePlatformCreateOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*completetedAzurePlatformCreateOptions
}

func (o *RawAzurePlatformCreateOptions) Validate() (*ValidatedAzurePlatformCreateOptions, error) {
	// Validate publisher, offer, sku, and version flags are provided if one of them is not empty
	marketplaceImageInfo := map[string]*string{
		"marketplace-publisher": &o.MarketplacePublisher,
		"marketplace-offer":     &o.MarketplaceOffer,
		"marketplace-sku":       &o.MarketplaceSKU,
		"marketplace-version":   &o.MarketplaceVersion,
	}
	if err := util.ValidateMarketplaceFlags(marketplaceImageInfo); err != nil {
		return nil, err
	}

	return &ValidatedAzurePlatformCreateOptions{
		validatedAzurePlatformCreateOptions: &validatedAzurePlatformCreateOptions{
			RawAzurePlatformCreateOptions: o,
		},
	}, nil
}

func (o *ValidatedAzurePlatformCreateOptions) Complete() (*CompletedAzurePlatformCreateOptions, error) {
	return &CompletedAzurePlatformCreateOptions{
		completetedAzurePlatformCreateOptions: &completetedAzurePlatformCreateOptions{
			AzurePlatformCreateOptions: o.AzurePlatformCreateOptions,
			AzureMarketPlaceImageInfo:  o.AzureMarketPlaceImageInfo,
		},
	}, nil
}

func NewCreateCommand(coreOpts *core.CreateNodePoolOptions) *cobra.Command {
	platformOpts := DefaultOptions()
	cmd := &cobra.Command{
		Use:          "azure",
		Short:        "Creates basic functional NodePool resources for Azure platform",
		SilenceUsage: true,
	}
	BindDeveloperOptions(platformOpts, cmd.Flags())
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

func (o *CompletedAzurePlatformCreateOptions) UpdateNodePool(_ context.Context, nodePool *hyperv1.NodePool, _ *hyperv1.HostedCluster, _ crclient.Client) error {
	nodePool.Spec.Platform.Azure = o.NodePoolPlatform()
	return nil
}

func (o *CompletedAzurePlatformCreateOptions) NodePoolPlatform() *hyperv1.AzureNodePoolPlatform {
	var vmImage hyperv1.AzureVMImage
	if o.ImageID != "" {
		vmImage = hyperv1.AzureVMImage{
			Type:    hyperv1.ImageID,
			ImageID: ptr.To(o.ImageID),
		}
	} else {
		vmImage = hyperv1.AzureVMImage{
			Type: hyperv1.AzureMarketplace,
			AzureMarketplace: &hyperv1.MarketplaceImage{
				Publisher: o.MarketplacePublisher,
				Offer:     o.MarketplaceOffer,
				SKU:       o.MarketplaceSKU,
				Version:   o.MarketplaceVersion,
			},
		}
	}

	platform := &hyperv1.AzureNodePoolPlatform{
		VMSize:                 o.InstanceType,
		DiskSizeGB:             o.DiskSize,
		AvailabilityZone:       o.AvailabilityZone,
		DiskEncryptionSetID:    o.DiskEncryptionSetID,
		EnableEphemeralOSDisk:  o.EnableEphemeralOSDisk,
		DiskStorageAccountType: o.DiskStorageAccountType,
		SubnetID:               o.SubnetID,
		Image:                  vmImage,
	}

	return platform
}

func (o *AzurePlatformCreateOptions) Type() hyperv1.PlatformType {
	return hyperv1.AzurePlatform
}
