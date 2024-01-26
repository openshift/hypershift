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
	"github.com/openshift/hypershift/support/infraid"
	"github.com/openshift/hypershift/support/releaseinfo"

	"github.com/spf13/cobra"
	"sigs.k8s.io/yaml"
)

func NewCreateCommand(opts *core.CreateOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "azure",
		Short:        "Creates basic functional HostedCluster resources on Azure",
		SilenceUsage: true,
	}

	opts.AzurePlatform.Location = "eastus"
	opts.AzurePlatform.DiskSizeGB = 120

	cmd.Flags().StringVar(&opts.AzurePlatform.CredentialsFile, "azure-creds", opts.AzurePlatform.CredentialsFile, "Path to an Azure credentials file (required)")
	cmd.Flags().StringVar(&opts.AzurePlatform.Location, "location", opts.AzurePlatform.Location, "Location for the cluster")
	cmd.Flags().StringVar(&opts.AzurePlatform.EncryptionKeyID, "encryption-key-id", opts.AzurePlatform.EncryptionKeyID, "etcd encryption key identifier in the form of https://<vaultName>.vault.azure.net/keys/<keyName>/<keyVersion>")
	cmd.Flags().StringVar(&opts.AzurePlatform.InstanceType, "instance-type", opts.AzurePlatform.InstanceType, "The instance type to use for nodes")
	cmd.Flags().Int32Var(&opts.AzurePlatform.DiskSizeGB, "root-disk-size", opts.AzurePlatform.DiskSizeGB, "The size of the root disk for machines in the NodePool (minimum 16)")
	cmd.Flags().StringSliceVar(&opts.AzurePlatform.AvailabilityZones, "availability-zones", opts.AzurePlatform.AvailabilityZones, "The availability zones in which NodePools will be created. Must be left unspecified if the region does not support AZs. If set, one nodepool per zone will be created.")
	cmd.Flags().StringVar(&opts.AzurePlatform.ResourceGroupName, "resource-group-name", opts.AzurePlatform.ResourceGroupName, "A resource group name to create the HostedCluster infrastructure resources under.")
	cmd.Flags().StringVar(&opts.AzurePlatform.DiskEncryptionSetID, "disk-encryption-set-id", opts.AzurePlatform.DiskEncryptionSetID, "The Disk Encryption Set ID to use to encrypt the OS disks for the VMs.")

	_ = cmd.MarkFlagRequired("azure-creds")
	_ = cmd.MarkPersistentFlagRequired("pull-secret")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		if opts.Timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
			defer cancel()
		}

		err := validate(opts)
		if err != nil {
			return err
		}

		if err = CreateCluster(ctx, opts); err != nil {
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
		rhcosImageMap, err := lookupRHCOSImage(ctx, opts.Arch, opts.ReleaseImage, opts.PullSecretFile)
		if err != nil {
			return fmt.Errorf("failed to retrieve RHCOS image: %w", err)
		}

		infraID := infraid.New(opts.Name)
		infra, err = (&azureinfra.CreateInfraOptions{
			Name:              opts.Name,
			Location:          opts.AzurePlatform.Location,
			InfraID:           infraID,
			CredentialsFile:   opts.AzurePlatform.CredentialsFile,
			BaseDomain:        opts.BaseDomain,
			RHCOSImage:        rhcosImageMap,
			Arch:              opts.Arch,
			ResourceGroupName: opts.AzurePlatform.ResourceGroupName,
		}).Run(ctx, opts.Log)
		if err != nil {
			return fmt.Errorf("failed to create infra: %w", err)
		}
	}

	var instanceType string
	if opts.AzurePlatform.InstanceType != "" {
		instanceType = opts.AzurePlatform.InstanceType
	} else {
		// Aligning with Azure IPI instance type defaults
		switch opts.Arch {
		case hyperv1.ArchitectureAMD64:
			instanceType = "Standard_D4s_v3"
		case hyperv1.ArchitectureARM64:
			instanceType = "Standard_D4ps_v5"
		}
	}

	exampleOptions.BaseDomain = infra.BaseDomain
	exampleOptions.PublicZoneID = infra.PublicZoneID
	exampleOptions.PrivateZoneID = infra.PrivateZoneID
	exampleOptions.InfraID = infra.InfraID
	exampleOptions.ExternalDNSDomain = opts.ExternalDNSDomain
	exampleOptions.Azure = &apifixtures.ExampleAzureOptions{
		Location:            infra.Location,
		ResourceGroupName:   infra.ResourceGroupName,
		VnetName:            infra.VnetName,
		VnetID:              infra.VNetID,
		SubnetName:          infra.SubnetName,
		BootImageInfo:       infra.BootImageInfo,
		MachineIdentityID:   infra.MachineIdentityID,
		InstanceType:        instanceType,
		SecurityGroupName:   infra.SecurityGroupName,
		DiskSizeGB:          opts.AzurePlatform.DiskSizeGB,
		AvailabilityZones:   opts.AzurePlatform.AvailabilityZones,
		DiskEncryptionSetID: opts.AzurePlatform.DiskEncryptionSetID,
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

// lookupRHCOSImage looks up a release image and extracts appropriate RHCOS images based on the arch
func lookupRHCOSImage(ctx context.Context, arch string, image string, pullSecretFile string) (map[string]string, error) {
	rhcosImageMap := make(map[string]string)
	releaseProvider := &releaseinfo.RegistryClientProvider{}

	pullSecret, err := os.ReadFile(pullSecretFile)
	if err != nil {
		return nil, fmt.Errorf("lookupRHCOSImage: failed to read pull secret file")
	}

	releaseImage, err := releaseProvider.Lookup(ctx, image, pullSecret)
	if err != nil {
		return nil, fmt.Errorf("lookupRHCOSImage: failed to lookup release image")
	}

	rhcosImage, err := retrieveRHCOSImageFromArch(releaseImage, arch)
	if err != nil {
		return nil, err
	}
	rhcosImageMap[arch] = rhcosImage

	// If the arch is arm64, we also need to look up the amd64 RHCOS image so NodePools of both CPU arches can be created in the Hosted Cluster
	if arch == hyperv1.ArchitectureARM64 {
		rhcosImage, err = retrieveRHCOSImageFromArch(releaseImage, hyperv1.ArchitectureAMD64)
		if err != nil {
			return nil, err
		}

		rhcosImageMap[hyperv1.ArchitectureAMD64] = rhcosImage
	}

	return rhcosImageMap, nil
}

// retrieveRHCOSImageFromArch retrieves the appropriate RHCOS image based on the arch
func retrieveRHCOSImageFromArch(releaseImage *releaseinfo.ReleaseImage, arch string) (string, error) {
	// We need to translate amd64 to x86_64 and arm64 to aarch64 since that is what is in the release image stream
	if _, ok := releaseImage.StreamMetadata.Architectures[hyperv1.ArchAliases[arch]]; !ok {
		return "", fmt.Errorf("retrieveRHCOSImageFromArch: arch does not exist in release image, arch: %s", arch)
	}

	rhcosImage := releaseImage.StreamMetadata.Architectures[hyperv1.ArchAliases[arch]].RHCOS.AzureDisk.URL

	if rhcosImage == "" {
		return "", fmt.Errorf("retrieveRHCOSImageFromArch: RHCOS VHD image is empty")
	}

	return rhcosImage, nil
}

// validate validates the core create options passed in by the user
func validate(opts *core.CreateOptions) error {
	// Resource group name is required when using DiskEncryptionSetID
	if opts.AzurePlatform.DiskEncryptionSetID != "" && opts.AzurePlatform.ResourceGroupName == "" {
		return fmt.Errorf("validate: resource-group-name is required when using disk-encryption-set-id")
	}
	return nil
}
