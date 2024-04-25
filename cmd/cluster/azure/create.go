package azure

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	azureinfra "github.com/openshift/hypershift/cmd/infra/azure"
	apifixtures "github.com/openshift/hypershift/examples/fixtures"
	"github.com/openshift/hypershift/support/releaseinfo"

	"github.com/spf13/cobra"
	"sigs.k8s.io/yaml"
)

func NewCreateCommand(opts *core.CreateOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:               "azure",
		Short:             "Creates basic functional HostedCluster resources on Azure",
		SilenceUsage:      true,
		PersistentPreRunE: validateFlags,
	}

	opts.AzurePlatform.Location = "eastus"
	opts.AzurePlatform.InstanceType = "Standard_D4s_v4"
	opts.AzurePlatform.DiskSizeGB = 120

	cmd.Flags().StringVar(&opts.AzurePlatform.CredentialsFile, "azure-creds", opts.AzurePlatform.CredentialsFile, "Path to an Azure credentials file (required)")
	cmd.Flags().StringVar(&opts.AzurePlatform.Location, "location", opts.AzurePlatform.Location, "Location for the cluster")
	cmd.Flags().StringVar(&opts.AzurePlatform.EncryptionKeyID, "encryption-key-id", opts.AzurePlatform.EncryptionKeyID, "etcd encryption key identifier in the form of https://<vaultName>.vault.azure.net/keys/<keyName>/<keyVersion>")
	cmd.Flags().StringVar(&opts.AzurePlatform.InstanceType, "instance-type", opts.AzurePlatform.InstanceType, "The instance type to use for nodes")
	cmd.Flags().Int32Var(&opts.AzurePlatform.DiskSizeGB, "root-disk-size", opts.AzurePlatform.DiskSizeGB, "The size of the root disk for machines in the NodePool (minimum 16)")
	cmd.Flags().StringSliceVar(&opts.AzurePlatform.AvailabilityZones, "availability-zones", opts.AzurePlatform.AvailabilityZones, "The availability zones in which NodePools will be created. Must be left unspecified if the region does not support AZs. If set, one nodepool per zone will be created.")
	cmd.Flags().StringVar(&opts.AzurePlatform.ResourceGroupName, "resource-group-name", opts.AzurePlatform.ResourceGroupName, "A resource group name to create the HostedCluster infrastructure resources under.")
	cmd.Flags().StringVar(&opts.AzurePlatform.DiskEncryptionSetID, "disk-encryption-set-id", opts.AzurePlatform.DiskEncryptionSetID, "The Disk Encryption Set ID to use to encrypt the OS disks for the VMs.")
	cmd.Flags().StringVar(&opts.AzurePlatform.NetworkSecurityGroup, "network-security-group", opts.AzurePlatform.NetworkSecurityGroup, "The name of the Network Security Group to use in Virtual Network created for HostedCluster.")
	cmd.Flags().BoolVar(&opts.AzurePlatform.EnableEphemeralOSDisk, "enable-ephemeral-disk", opts.AzurePlatform.EnableEphemeralOSDisk, "If enabled, the Azure VMs in the default NodePool will be setup with ephemeral OS disks")
	cmd.Flags().StringVar(&opts.AzurePlatform.DiskStorageAccountType, "disk-storage-account-type", opts.AzurePlatform.DiskStorageAccountType, "The disk storage account type for the OS disks for the VMs.")
	cmd.Flags().StringToStringVarP(&opts.AzurePlatform.ResourceGroupTags, "resource-group-tags", "t", opts.AzurePlatform.ResourceGroupTags, "Additional tags to apply to the resource group created (e.g. 'key1=value1,key2=value2')")
	cmd.Flags().StringVar(&opts.AzurePlatform.SubnetID, "subnet-id", opts.AzurePlatform.SubnetID, "The subnet ID where the VMs will be placed.")

	_ = cmd.MarkFlagRequired("azure-creds")
	_ = cmd.MarkPersistentFlagRequired("pull-secret")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		if opts.Timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
			defer cancel()
		}

		if err := CreateCluster(ctx, opts); err != nil {
			opts.Log.Error(err, "Failed to create cluster")
			return err
		}
		return nil
	}

	return cmd
}

func CreateCluster(ctx context.Context, opts *core.CreateOptions) error {
	if err := core.Validate(ctx, opts); err != nil {
		return err
	}
	return core.CreateCluster(ctx, opts, applyPlatformSpecificsValues)
}

func applyPlatformSpecificsValues(ctx context.Context, exampleOptions *apifixtures.ExampleOptions, opts *core.CreateOptions) error {
	var infra *azureinfra.CreateInfraOutput
	var err error
	if opts.InfrastructureJSON != "" {
		rawInfra, err := os.ReadFile(opts.InfrastructureJSON)
		if err != nil {
			return fmt.Errorf("failed to read infra json file: %w", err)
		}
		if err := yaml.Unmarshal(rawInfra, &infra); err != nil {
			return fmt.Errorf("failed to deserialize infra json file: %w", err)
		}
	} else {
		rhcosImage, err := lookupRHCOSImage(ctx, opts.Arch, opts.ReleaseImage, opts.PullSecretFile)
		if err != nil {
			return fmt.Errorf("failed to retrieve RHCOS image: %w", err)
		}

		infra, err = (&azureinfra.CreateInfraOptions{
			Name:                 opts.Name,
			Location:             opts.AzurePlatform.Location,
			InfraID:              opts.InfraID,
			CredentialsFile:      opts.AzurePlatform.CredentialsFile,
			BaseDomain:           opts.BaseDomain,
			RHCOSImage:           rhcosImage,
			ResourceGroupName:    opts.AzurePlatform.ResourceGroupName,
			NetworkSecurityGroup: opts.AzurePlatform.NetworkSecurityGroup,
			ResourceGroupTags:    opts.AzurePlatform.ResourceGroupTags,
			SubnetID:             opts.AzurePlatform.SubnetID,
			SubnetName:           opts.AzurePlatform.SubnetName,
		}).Run(ctx, opts.Log)
		if err != nil {
			return fmt.Errorf("failed to create infra: %w", err)
		}
	}

	exampleOptions.BaseDomain = infra.BaseDomain
	exampleOptions.PublicZoneID = infra.PublicZoneID
	exampleOptions.PrivateZoneID = infra.PrivateZoneID
	exampleOptions.InfraID = infra.InfraID
	exampleOptions.ExternalDNSDomain = opts.ExternalDNSDomain
	exampleOptions.Azure = &apifixtures.ExampleAzureOptions{
		Location:               infra.Location,
		ResourceGroupName:      infra.ResourceGroupName,
		VnetName:               infra.VnetName,
		VnetID:                 infra.VNetID,
		SubnetID:               infra.SubnetID,
		SubnetName:             infra.SubnetName,
		BootImageID:            infra.BootImageID,
		MachineIdentityID:      infra.MachineIdentityID,
		InstanceType:           opts.AzurePlatform.InstanceType,
		SecurityGroupName:      infra.SecurityGroupName,
		DiskSizeGB:             opts.AzurePlatform.DiskSizeGB,
		AvailabilityZones:      opts.AzurePlatform.AvailabilityZones,
		DiskEncryptionSetID:    opts.AzurePlatform.DiskEncryptionSetID,
		EnableEphemeralOSDisk:  opts.AzurePlatform.EnableEphemeralOSDisk,
		DiskStorageAccountType: opts.AzurePlatform.DiskStorageAccountType,
	}

	if opts.AzurePlatform.EncryptionKeyID != "" {
		parsedKeyId, err := url.Parse(opts.AzurePlatform.EncryptionKeyID)
		if err != nil {
			return fmt.Errorf("invalid encryption key identifier: %v", err)
		}

		key := strings.Split(strings.TrimPrefix(parsedKeyId.Path, "/keys/"), "/")
		if len(key) != 2 {
			return fmt.Errorf("invalid encryption key identifier, couldn't retrieve key name and version: %v", err)
		}

		exampleOptions.Azure.EncryptionKey = &apifixtures.AzureEncryptionKey{
			KeyVaultName: strings.Split(parsedKeyId.Hostname(), ".")[0],
			KeyName:      key[0],
			KeyVersion:   key[1],
		}
	}

	azureCredsRaw, err := os.ReadFile(opts.AzurePlatform.CredentialsFile)
	if err != nil {
		return fmt.Errorf("failed to read --azure-creds file %s: %w", opts.AzurePlatform.CredentialsFile, err)
	}
	if err := yaml.Unmarshal(azureCredsRaw, &exampleOptions.Azure.Creds); err != nil {
		return fmt.Errorf("failed to unmarshal --azure-creds file: %w", err)
	}
	return nil
}

// lookupRHCOSImage looks up a release image and extracts the RHCOS VHD image based on the nodepool arch
func lookupRHCOSImage(ctx context.Context, arch string, image string, pullSecretFile string) (string, error) {
	rhcosImage := ""
	releaseProvider := &releaseinfo.RegistryClientProvider{}

	pullSecret, err := os.ReadFile(pullSecretFile)
	if err != nil {
		return "", fmt.Errorf("lookupRHCOSImage: failed to read pull secret file")
	}

	releaseImage, err := releaseProvider.Lookup(ctx, image, pullSecret)
	if err != nil {
		return "", fmt.Errorf("lookupRHCOSImage: failed to lookup release image")
	}

	// We need to translate amd64 to x86_64 and arm64 to aarch64 since that is what is in the release image stream
	if _, ok := releaseImage.StreamMetadata.Architectures[hyperv1.ArchAliases[arch]]; !ok {
		return "", fmt.Errorf("lookupRHCOSImage: arch does not exist in release image, arch: %s", arch)
	}

	rhcosImage = releaseImage.StreamMetadata.Architectures[hyperv1.ArchAliases[arch]].RHCOS.AzureDisk.URL

	if rhcosImage == "" {
		return "", fmt.Errorf("lookupRHCOSImage: RHCOS VHD image is empty")
	}

	return rhcosImage, nil
}

// validateFlags validates the core create option flags passed in by the user
func validateFlags(cmd *cobra.Command, _ []string) error {
	// Check if the network security group is set and the resource group is not
	nsg, err := cmd.Flags().GetString("network-security-group")
	if err != nil {
		return err
	}
	rg, err := cmd.Flags().GetString("resource-group-name")
	if err != nil {
		return err
	}

	if nsg != "" && rg == "" {
		return fmt.Errorf("flag --resource-group-name is required when using --network-security-group")
	}

	// Validate a resource group is provided when using the disk encryption set id flag
	desID, err := cmd.Flags().GetString("disk-encryption-set-id")
	if err != nil {
		return err
	}

	if desID != "" && rg == "" {
		return fmt.Errorf("resource-group-name is required when using disk-encryption-set-id")
	}

	return nil
}
