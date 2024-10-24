package azure

import (
	"context"
	"fmt"
	"slices"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/nodepool/core"
	"github.com/openshift/hypershift/cmd/util"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"k8s.io/utils/ptr"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type AzurePlatformCreateOptions struct {
	InstanceType                  string
	DiskSize                      int32
	AvailabilityZone              string
	DiagnosticsStorageAccountType hyperv1.AzureDiagnosticsStorageAccountType
	DiagnosticsStorageAccountURI  string
	DiskEncryptionSetID           string
	EnableEphemeralOSDisk         bool
	DiskStorageAccountType        string
	SubnetID                      string
	ImageID                       string
	Arch                          string
	EncryptionAtHost              string
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
			DiskSize: 120,
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
	flags.Var(&opts.DiagnosticsStorageAccountType, "diagnostics-storage-account-type", "Specifies the type of storage account for storing diagnostics data. Supported values: Disabled, Managed, UserManaged.")
	flags.StringVar(&opts.DiagnosticsStorageAccountURI, "diagnostics-storage-account-uri", opts.DiagnosticsStorageAccountURI, "Specifies the URI of the storage account for diagnostics data. Applicable only if --diagnostics-storage-account-type is set to UserManaged.")
	flags.StringVar(&opts.DiskEncryptionSetID, "disk-encryption-set-id", opts.DiskEncryptionSetID, "The Disk Encryption Set ID to use to encrypt the OS disks for the VMs.")
	flags.BoolVar(&opts.EnableEphemeralOSDisk, "enable-ephemeral-disk", opts.EnableEphemeralOSDisk, "If enabled, the Azure VMs in the NodePool will be setup with ephemeral OS disks")
	flags.StringVar(&opts.DiskStorageAccountType, "disk-storage-account-type", opts.DiskStorageAccountType, "The disk storage account type for the OS disks for the VMs.")
	flags.StringVar(&opts.SubnetID, "nodepool-subnet-id", opts.SubnetID, "The subnet id where the VMs will be placed.")
	flags.StringVar(&opts.ImageID, "image-id", opts.ImageID, "The Image ID to boot the VMs with.")
	flags.StringVar(&opts.MarketplacePublisher, "marketplace-publisher", opts.MarketplacePublisher, "The Azure Marketplace image publisher.")
	flags.StringVar(&opts.MarketplaceOffer, "marketplace-offer", opts.MarketplaceOffer, "The Azure Marketplace image offer.")
	flags.StringVar(&opts.MarketplaceSKU, "marketplace-sku", opts.MarketplaceSKU, "The Azure Marketplace image SKU.")
	flags.StringVar(&opts.MarketplaceVersion, "marketplace-version", opts.MarketplaceVersion, "The Azure Marketplace image version.")
	flags.StringVar(&opts.EncryptionAtHost, "encryption-at-host", opts.EncryptionAtHost, "Enables or disables encryption at host on Azure VMs. Supported values: Enabled, Disabled.")
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

// TODO: HOSTEDCP-1974: Unify Validate/Complete Function Signatures Across Providers
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

	if o.DiagnosticsStorageAccountType != hyperv1.AzureDiagnosticsStorageAccountTypeUserManaged && len(o.DiagnosticsStorageAccountURI) > 0 {
		return nil, fmt.Errorf("--diagnostics-storage-account-uri is applicable only if --diagnostics-storage-account-type is set to %s", hyperv1.AzureDiagnosticsStorageAccountTypeUserManaged)
	}

	if o.EncryptionAtHost != "" && o.EncryptionAtHost != "Enabled" && o.EncryptionAtHost != "Disabled" {
		return nil, fmt.Errorf("flag --enable-encryption-at-host has an invalid value; accepted values are 'Enabled' and 'Disabled'")
	}

	if !slices.Contains([]string{"", "1", "2", "3"}, o.AvailabilityZone) {
		return nil, fmt.Errorf("invalid value for --availability-zone: %s", o.AvailabilityZone)
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
	nodePool.Spec.Platform.Azure = o.NodePoolPlatform(nodePool)
	return nil
}

func (o *CompletedAzurePlatformCreateOptions) NodePoolPlatform(nodePool *hyperv1.NodePool) *hyperv1.AzureNodePoolPlatform {
	var vmImage hyperv1.AzureVMImage
	if o.ImageID != "" {
		vmImage = hyperv1.AzureVMImage{
			Type:    hyperv1.ImageID,
			ImageID: ptr.To(o.ImageID),
		}
	} else {
		vmImage = hyperv1.AzureVMImage{
			Type: hyperv1.AzureMarketplace,
			AzureMarketplace: &hyperv1.AzureMarketplaceImage{
				Publisher: o.MarketplacePublisher,
				Offer:     o.MarketplaceOffer,
				SKU:       o.MarketplaceSKU,
				Version:   o.MarketplaceVersion,
			},
		}
	}

	instanceType := o.completetedAzurePlatformCreateOptions.AzurePlatformCreateOptions.InstanceType
	if strings.TrimSpace(instanceType) == "" {
		// Aligning with Azure IPI instance type defaults
		switch nodePool.Spec.Arch {
		case hyperv1.ArchitectureAMD64:
			instanceType = "Standard_D4s_v3"
		case hyperv1.ArchitectureARM64:
			instanceType = "Standard_D4ps_v5"
		}
	}

	var persistence hyperv1.AzureDiskPersistence
	if o.EnableEphemeralOSDisk {
		persistence = hyperv1.EphemeralDiskPersistence
	}

	platform := &hyperv1.AzureNodePoolPlatform{
		VMSize: instanceType,
		OSDisk: hyperv1.AzureNodePoolOSDisk{
			SizeGiB:                o.DiskSize,
			DiskStorageAccountType: hyperv1.AzureDiskStorageAccountType(o.DiskStorageAccountType),
			Persistence:            persistence,
			EncryptionSetID:        o.DiskEncryptionSetID,
		},
		AvailabilityZone: o.AvailabilityZone,
		SubnetID:         o.SubnetID,
		Image:            vmImage,
		EncryptionAtHost: o.EncryptionAtHost,
	}

	if len(o.DiagnosticsStorageAccountType) > 0 {
		platform.Diagnostics = &hyperv1.Diagnostics{
			StorageAccountType: o.DiagnosticsStorageAccountType,
		}

		if o.DiagnosticsStorageAccountType == hyperv1.AzureDiagnosticsStorageAccountTypeUserManaged &&
			o.DiagnosticsStorageAccountURI != "" {
			platform.Diagnostics = &hyperv1.Diagnostics{
				StorageAccountType: o.DiagnosticsStorageAccountType,
				UserManaged: &hyperv1.UserManagedDiagnostics{
					StorageAccountURI: o.DiagnosticsStorageAccountURI,
				},
			}
		}
	}

	return platform
}

func (o *AzurePlatformCreateOptions) Type() hyperv1.PlatformType {
	return hyperv1.AzurePlatform
}
