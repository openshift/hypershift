package azure

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/nodepool/core"

	"github.com/spf13/cobra"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type AzurePlatformCreateOptions struct {
	InstanceType           string
	DiskSize               int32
	AvailabilityZone       string
	ResourceGroupName      string
	DiskEncryptionSetID    string
	EnableEphemeralOSDisk  bool
	DiskStorageAccountType string
	SubnetName             string
}

func NewCreateCommand(coreOpts *core.CreateNodePoolOptions) *cobra.Command {
	platformOpts := &AzurePlatformCreateOptions{
		InstanceType: "Standard_D4s_v4",
		DiskSize:     120,
		SubnetName:   "default",
	}

	cmd := &cobra.Command{
		Use:          "azure",
		Short:        "Creates basic functional NodePool resources for Azure platform",
		SilenceUsage: true,
	}

	cmd.Flags().StringVar(&platformOpts.InstanceType, "instance-type", platformOpts.InstanceType, "The instance type to use for the nodepool")
	cmd.Flags().Int32Var(&platformOpts.DiskSize, "root-disk-size", platformOpts.DiskSize, "The size of the root disk for machines in the NodePool (minimum 16)")
	cmd.Flags().StringVar(&platformOpts.AvailabilityZone, "availability-zone", platformOpts.AvailabilityZone, "The availabilityZone for the nodepool. Must be left unspecified if in a region that doesn't support AZs")
	cmd.Flags().StringVar(&platformOpts.ResourceGroupName, "resource-group-name", platformOpts.ResourceGroupName, "A resource group name to create the HostedCluster infrastructure resources under.")
	cmd.Flags().StringVar(&platformOpts.DiskEncryptionSetID, "disk-encryption-set-id", platformOpts.DiskEncryptionSetID, "The Disk Encryption Set ID to use to encrypt the OS disks for the VMs.")
	cmd.Flags().BoolVar(&platformOpts.EnableEphemeralOSDisk, "enable-ephemeral-disk", platformOpts.EnableEphemeralOSDisk, "If enabled, the Azure VMs in the NodePool will be setup with ephemeral OS disks")
	cmd.Flags().StringVar(&platformOpts.DiskStorageAccountType, "disk-storage-account-type", platformOpts.DiskStorageAccountType, "The disk storage account type for the OS disks for the VMs.")
	cmd.Flags().StringVar(&platformOpts.SubnetName, "subnet-name", platformOpts.SubnetName, "The subnet name where the VMs will be placed.")

	cmd.RunE = coreOpts.CreateRunFunc(platformOpts)

	return cmd
}

func (o *AzurePlatformCreateOptions) UpdateNodePool(_ context.Context, nodePool *hyperv1.NodePool, _ *hyperv1.HostedCluster, _ crclient.Client) error {
	// Resource group name is required when using DiskEncryptionSetID
	if o.DiskEncryptionSetID != "" && o.ResourceGroupName == "" {
		return fmt.Errorf("resource-group-name is required when using disk-encryption-set-id")
	}

	nodePool.Spec.Platform.Type = hyperv1.AzurePlatform
	nodePool.Spec.Platform.Azure = &hyperv1.AzureNodePoolPlatform{
		VMSize:                 o.InstanceType,
		DiskSizeGB:             o.DiskSize,
		AvailabilityZone:       o.AvailabilityZone,
		DiskEncryptionSetID:    o.DiskEncryptionSetID,
		EnableEphemeralOSDisk:  o.EnableEphemeralOSDisk,
		DiskStorageAccountType: o.DiskStorageAccountType,
		SubnetName:             o.SubnetName,
	}
	return nil
}

func (o *AzurePlatformCreateOptions) Type() hyperv1.PlatformType {
	return hyperv1.AzurePlatform
}
