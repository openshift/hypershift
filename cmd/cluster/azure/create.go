package azure

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	azureinfra "github.com/openshift/hypershift/cmd/infra/azure"
	azurenodepool "github.com/openshift/hypershift/cmd/nodepool/azure"
	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/support/azureutil"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const (
	SATokenIssuerSecret = "sa-token-issuer-key"
	ObjectEncoding      = "utf-8"
)

var _ core.Platform = (*CreateOptions)(nil)

func DefaultOptions() *RawCreateOptions {
	return &RawCreateOptions{
		Location:     "eastus",
		NodePoolOpts: azurenodepool.DefaultOptions(),
	}
}

func NewCreateCommand(opts *core.RawCreateOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "azure",
		Short:        "Creates basic functional HostedCluster resources on Azure",
		SilenceUsage: true,
	}

	azureOpts := DefaultOptions()
	BindOptions(azureOpts, cmd.Flags())
	_ = cmd.MarkPersistentFlagRequired("pull-secret")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		if opts.Timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
			defer cancel()
		}

		if err := core.CreateCluster(ctx, opts, azureOpts); err != nil {
			opts.Log.Error(err, "Failed to create cluster")
			return err
		}
		return nil
	}

	return cmd
}

func BindOptions(opts *RawCreateOptions, flags *pflag.FlagSet) {
	bindCoreOptions(opts, flags)
	azurenodepool.BindOptions(opts.NodePoolOpts, flags)
}

func bindCoreOptions(opts *RawCreateOptions, flags *pflag.FlagSet) {
	// ARO HCP (managed Azure) only flags; these flags should not be used for self-managed Azure
	flags.StringVar(&opts.KMSUserAssignedCredsSecretName, "kms-credentials-secret-name", opts.KMSUserAssignedCredsSecretName, util.KMSCredentialsSecretNameDescription)
	flags.StringVar(&opts.ManagedIdentitiesFile, "managed-identities-file", opts.ManagedIdentitiesFile, util.ManagedIdentitiesFileDescription)
	flags.StringVar(&opts.DataPlaneIdentitiesFile, "data-plane-identities-file", opts.DataPlaneIdentitiesFile, util.DataPlaneIdentitiesFileDescription)
	flags.BoolVar(&opts.AssignCustomHCPRoles, "assign-custom-hcp-roles", opts.AssignCustomHCPRoles, util.AssignCustomHCPRolesDescription)

	// self-managed Azure only flags; these flags should not be used for ARO HCP (managed Azure)
	flags.StringVar(&opts.WorkloadIdentitiesFile, "workload-identities-file", opts.WorkloadIdentitiesFile, util.WorkloadIdentitiesFileDescription)

	// flags used for both ARO HCP and self-managed Azure
	// In ARO HCP, it assigns roles to managed identities; in self-managed Azure, it assigns roles to workload identities.
	// The self-managed HCP CLI version of this flag is named assign-identity-roles.
	flags.BoolVar(&opts.AssignServicePrincipalRoles, "assign-service-principal-roles", opts.AssignServicePrincipalRoles, util.AssignServicePrincipalRolesDescription)

	// general flags used for both managed and self-managed Azure
	flags.StringVar(&opts.CredentialsFile, "azure-creds", opts.CredentialsFile, util.AzureCredsDescription)
	flags.StringVar(&opts.Location, "location", opts.Location, util.LocationDescription)
	flags.StringVar(&opts.EncryptionKeyID, "encryption-key-id", opts.EncryptionKeyID, util.EncryptionKeyIDDescription)
	flags.StringSliceVar(&opts.AvailabilityZones, "availability-zones", opts.AvailabilityZones, util.AvailabilityZonesDescription)
	flags.StringVar(&opts.ResourceGroupName, "resource-group-name", opts.ResourceGroupName, util.ResourceGroupNameDescription)
	flags.StringVar(&opts.VnetID, "vnet-id", opts.VnetID, util.VnetIDDescription)
	flags.StringVar(&opts.NetworkSecurityGroupID, "network-security-group-id", opts.NetworkSecurityGroupID, util.NetworkSecurityGroupIDDescription)
	flags.StringToStringVarP(&opts.ResourceGroupTags, "resource-group-tags", "t", opts.ResourceGroupTags, util.ResourceGroupTagsDescription)
	flags.StringVar(&opts.SubnetID, "subnet-id", opts.SubnetID, util.SubnetIDDescription)
	flags.StringVar(&opts.IssuerURL, "oidc-issuer-url", "", util.OIDCIssuerURLDescription)
	flags.StringVar(&opts.ServiceAccountTokenIssuerKeyPath, "sa-token-issuer-private-key-path", "", util.SATokenIssuerKeyPathDescription)
	flags.StringVar(&opts.DNSZoneRGName, "dns-zone-rg-name", opts.DNSZoneRGName, util.DNSZoneRGNameDescription)
}

// BindDeveloperOptions binds developer/development only options for the Azure create cluster command
func BindDeveloperOptions(opts *RawCreateOptions, flags *pflag.FlagSet) {
	bindCoreOptions(opts, flags)
}

// BindProductFlags binds customer-facing flags for self-managed Azure in the product CLI
func BindProductFlags(opts *RawCreateOptions, flags *pflag.FlagSet) {
	// Required credentials
	flags.StringVar(&opts.CredentialsFile, "azure-creds", opts.CredentialsFile, util.AzureCredsDescription)

	// General flags used for self-managed Azure
	flags.StringVar(&opts.Location, "location", opts.Location, util.LocationDescription)
	flags.StringSliceVar(&opts.AvailabilityZones, "availability-zones", opts.AvailabilityZones, util.AvailabilityZonesDescription)
	flags.StringVar(&opts.ResourceGroupName, "resource-group-name", opts.ResourceGroupName, util.ResourceGroupNameDescription)
	flags.StringVar(&opts.VnetID, "vnet-id", opts.VnetID, util.VnetIDDescription)
	flags.StringVar(&opts.SubnetID, "subnet-id", opts.SubnetID, util.SubnetIDDescription)
	flags.StringVar(&opts.NetworkSecurityGroupID, "network-security-group-id", opts.NetworkSecurityGroupID, util.NetworkSecurityGroupIDDescription)
	flags.StringToStringVarP(&opts.ResourceGroupTags, "resource-group-tags", "t", opts.ResourceGroupTags, util.ResourceGroupTagsDescription)
	flags.StringVar(&opts.DNSZoneRGName, "dns-zone-rg-name", opts.DNSZoneRGName, util.DNSZoneRGNameDescription)

	// Self-managed Azure identity flags
	flags.StringVar(&opts.WorkloadIdentitiesFile, "workload-identities-file", opts.WorkloadIdentitiesFile, util.WorkloadIdentitiesFileDescription)
	flags.StringVar(&opts.IssuerURL, "oidc-issuer-url", "", util.OIDCIssuerURLDescription)
	flags.StringVar(&opts.ServiceAccountTokenIssuerKeyPath, "sa-token-issuer-private-key-path", "", util.SATokenIssuerKeyPathDescription)
	flags.BoolVar(&opts.AssignServicePrincipalRoles, "auto-assign-roles", opts.AssignServicePrincipalRoles, util.AutoAssignRolesDescription)

	// Encryption
	flags.StringVar(&opts.EncryptionKeyID, "encryption-key-id", opts.EncryptionKeyID, util.EncryptionKeyIDDescription)

	// Nodepool flags
	azurenodepool.BindProductFlags(opts.NodePoolOpts, flags)
}

// Validate validates the Azure create cluster command options
func (o *RawCreateOptions) Validate(ctx context.Context, _ *core.CreateOptions) (core.PlatformCompleter, error) {
	var err error

	// Check if the network security group is set and the resource group is not
	if o.NetworkSecurityGroupID != "" && o.ResourceGroupName == "" {
		return nil, fmt.Errorf("flag --resource-group-name is required when using --network-security-group-id")
	}

	// The DNS zone resource group name is required when assigning azure roles to the control plane components
	// since several will need to be scoped to this resource group.
	if o.AssignServicePrincipalRoles && o.DNSZoneRGName == "" {
		return nil, fmt.Errorf("flag --dns-zone-rg-name is required")
	}

	// Validate that workload identities file and managed identities files are mutually exclusive
	if o.WorkloadIdentitiesFile != "" && o.ManagedIdentitiesFile != "" {
		return nil, fmt.Errorf("flags --workload-identities-file and --managed-identities-file are mutually exclusive")
	}
	if o.WorkloadIdentitiesFile != "" && o.DataPlaneIdentitiesFile != "" {
		return nil, fmt.Errorf("flags --workload-identities-file and --data-plane-identities-file are mutually exclusive")
	}

	// Validate that data plane identities file requires managed identities file
	if o.DataPlaneIdentitiesFile != "" && o.ManagedIdentitiesFile == "" {
		return nil, fmt.Errorf("--data-plane-identities-file requires --managed-identities-file")
	}
	if o.ManagedIdentitiesFile != "" && o.DataPlaneIdentitiesFile == "" {
		return nil, fmt.Errorf("--managed-identities-file requires --data-plane-identities-file")
	}

	validOpts := &ValidatedCreateOptions{
		validatedCreateOptions: &validatedCreateOptions{
			RawCreateOptions: o,
		},
	}

	// Validate the availability zones
	for _, az := range o.AvailabilityZones {
		if !slices.Contains([]string{"1", "2", "3"}, az) {
			return nil, fmt.Errorf("invalid value for --availability-zone: %s", az)
		}
	}

	// Validate the nodepool options
	// Note: We pass nil for core.CreateNodePoolOptions since cluster create doesn't have NodePool options yet
	completer, err := o.NodePoolOpts.Validate(ctx, nil)
	if err != nil {
		return nil, err
	}

	// Type assert to get the validated options back
	if validated, ok := completer.(*azurenodepool.ValidatedAzurePlatformCreateOptions); ok {
		validOpts.ValidatedAzurePlatformCreateOptions = validated
	} else {
		return nil, fmt.Errorf("unexpected type returned from Validate")
	}

	return validOpts, nil
}

// Complete completes the Azure create cluster command options
func (o *ValidatedCreateOptions) Complete(ctx context.Context, opts *core.CreateOptions) (core.Platform, error) {
	output := &CreateOptions{
		completedCreateOptions: &completedCreateOptions{
			ValidatedCreateOptions: o,
			name:                   opts.Name,
			namespace:              opts.Namespace,
			externalDNSDomain:      opts.ExternalDNSDomain,
		},
	}

	// Load or create infrastructure for the cluster
	if opts.InfrastructureJSON != "" {
		rawInfra, err := os.ReadFile(opts.InfrastructureJSON)
		if err != nil {
			return nil, fmt.Errorf("failed to read infra json file: %w", err)
		}
		if err := yaml.Unmarshal(rawInfra, &output.infra); err != nil {
			return nil, fmt.Errorf("failed to deserialize infra json file: %w", err)
		}
	} else {
		infraOpts, err := CreateInfraOptions(ctx, o, opts)
		if err != nil {
			return nil, err
		}
		output.infra, err = infraOpts.Run(ctx, opts.Log)
		if err != nil {
			return nil, fmt.Errorf("failed to create infra: %w", err)
		}
	}

	// Set the encryption key information
	if o.EncryptionKeyID != "" {
		var err error
		output.encryptionKey, err = azureutil.GetAzureEncryptionKeyInfo(o.EncryptionKeyID)
		if err != nil {
			return nil, fmt.Errorf("failed to get encryption key info: %w", err)
		}
	}

	// Set the Azure credentials
	azureCredsRaw, err := os.ReadFile(o.CredentialsFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read --azure-creds file %s: %w", o.CredentialsFile, err)
	}
	if err := yaml.Unmarshal(azureCredsRaw, &output.creds); err != nil {
		return nil, fmt.Errorf("failed to unmarshal --azure-creds file: %w", err)
	}

	return output, nil
}

// ApplyPlatformSpecifics applies the Azure platform specific settings to the HostedCluster
func (o *CreateOptions) ApplyPlatformSpecifics(cluster *hyperv1.HostedCluster) error {
	cluster.Spec.DNS = hyperv1.DNSSpec{
		BaseDomain:    o.infra.BaseDomain,
		PublicZoneID:  o.infra.PublicZoneID,
		PrivateZoneID: o.infra.PrivateZoneID,
	}

	cluster.Spec.InfraID = o.infra.InfraID

	cluster.Spec.IssuerURL = o.IssuerURL

	if len(o.ServiceAccountTokenIssuerKeyPath) > 0 {
		cluster.Spec.ServiceAccountSigningKey = &corev1.LocalObjectReference{
			Name: o.name + SATokenIssuerSecret,
		}
	}

	cluster.Spec.Platform = hyperv1.PlatformSpec{
		Type: hyperv1.AzurePlatform,
		Azure: &hyperv1.AzurePlatformSpec{
			SubscriptionID:    o.creds.SubscriptionID,
			TenantID:          o.creds.TenantID,
			Location:          o.infra.Location,
			ResourceGroupName: o.infra.ResourceGroupName,
			VnetID:            o.infra.VNetID,
			SubnetID:          o.infra.SubnetID,
			SecurityGroupID:   o.infra.SecurityGroupID,
		},
	}

	// Configure authentication based on whether workload identities or managed identities are provided
	if o.infra.WorkloadIdentities != nil {
		// Self-managed Azure with workload identities
		cluster.Spec.Platform.Azure.AzureAuthenticationConfig = hyperv1.AzureAuthenticationConfiguration{
			AzureAuthenticationConfigType: hyperv1.AzureAuthenticationTypeWorkloadIdentities,
			WorkloadIdentities:            o.infra.WorkloadIdentities,
		}
	} else {
		// Managed Azure with managed identities
		cluster.Spec.Platform.Azure.AzureAuthenticationConfig = hyperv1.AzureAuthenticationConfiguration{
			AzureAuthenticationConfigType: hyperv1.AzureAuthenticationTypeManagedIdentities,
			ManagedIdentities:             o.infra.ControlPlaneMIs,
		}

		if o.infra.ControlPlaneMIs != nil {
			cluster.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities.DataPlane = o.infra.DataPlaneIdentities

			cluster.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities.ControlPlane.CloudProvider.ObjectEncoding = ObjectEncoding
			cluster.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities.ControlPlane.NodePoolManagement.ObjectEncoding = ObjectEncoding
			cluster.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities.ControlPlane.ControlPlaneOperator.ObjectEncoding = ObjectEncoding
			cluster.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities.ControlPlane.ImageRegistry.ObjectEncoding = ObjectEncoding
			cluster.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities.ControlPlane.Ingress.ObjectEncoding = ObjectEncoding
			cluster.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities.ControlPlane.Network.ObjectEncoding = ObjectEncoding
			cluster.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities.ControlPlane.Disk.ObjectEncoding = ObjectEncoding
			cluster.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities.ControlPlane.File.ObjectEncoding = ObjectEncoding
		}
	}

	if o.encryptionKey != nil {
		cluster.Spec.SecretEncryption = &hyperv1.SecretEncryptionSpec{
			Type: hyperv1.KMS,
			KMS: &hyperv1.KMSSpec{
				Provider: hyperv1.AZURE,
				Azure: &hyperv1.AzureKMSSpec{
					ActiveKey: hyperv1.AzureKMSKey{
						KeyVaultName: o.encryptionKey.KeyVaultName,
						KeyName:      o.encryptionKey.KeyName,
						KeyVersion:   o.encryptionKey.KeyVersion,
					},
					KMS: hyperv1.ManagedIdentity{
						CredentialsSecretName: o.KMSUserAssignedCredsSecretName,
						ObjectEncoding:        ObjectEncoding,
					},
				},
			},
		}
	}

	cluster.Spec.Services = core.GetIngressServicePublishingStrategyMapping(cluster.Spec.Networking.NetworkType, o.externalDNSDomain != "")
	if o.externalDNSDomain != "" {
		for i, svc := range cluster.Spec.Services {
			switch svc.Service {
			case hyperv1.APIServer:
				cluster.Spec.Services[i].Route = &hyperv1.RoutePublishingStrategy{
					Hostname: fmt.Sprintf("api-%s.%s", cluster.Name, o.externalDNSDomain),
				}

			case hyperv1.OAuthServer:
				cluster.Spec.Services[i].Route = &hyperv1.RoutePublishingStrategy{
					Hostname: fmt.Sprintf("oauth-%s.%s", cluster.Name, o.externalDNSDomain),
				}

			case hyperv1.Konnectivity:
				cluster.Spec.Services[i].Route = &hyperv1.RoutePublishingStrategy{
					Hostname: fmt.Sprintf("konnectivity-%s.%s", cluster.Name, o.externalDNSDomain),
				}
			case hyperv1.Ignition:
				cluster.Spec.Services[i].Route = &hyperv1.RoutePublishingStrategy{
					Hostname: fmt.Sprintf("ignition-%s.%s", cluster.Name, o.externalDNSDomain),
				}
			}
		}
	}
	return nil
}

// GenerateNodePools generates the initial nodepool(s) for the Azure HostedCluster create cluster command
func (o *CreateOptions) GenerateNodePools(constructor core.DefaultNodePoolConstructor) []*hyperv1.NodePool {
	var vmImage hyperv1.AzureVMImage
	if o.MarketplacePublisher != "" {
		// Use marketplace image when marketplace flags are provided
		marketplaceImage := &hyperv1.AzureMarketplaceImage{
			Publisher: o.MarketplacePublisher,
			Offer:     o.MarketplaceOffer,
			SKU:       o.MarketplaceSKU,
			Version:   o.MarketplaceVersion,
		}

		// Set ImageGeneration if specified by the user
		if o.NodePoolOpts.ImageGeneration != "" {
			switch o.NodePoolOpts.ImageGeneration {
			case "Gen1":
				marketplaceImage.ImageGeneration = ptr.To(hyperv1.Gen1)
			case "Gen2":
				marketplaceImage.ImageGeneration = ptr.To(hyperv1.Gen2)
			}
		}

		vmImage = hyperv1.AzureVMImage{
			Type:             hyperv1.AzureMarketplace,
			AzureMarketplace: marketplaceImage,
		}
	} else if o.infra.BootImageID != "" {
		// Use boot image ID only when it's been explicitly set during infra creation
		vmImage = hyperv1.AzureVMImage{
			Type:    hyperv1.ImageID,
			ImageID: ptr.To(o.infra.BootImageID),
		}
	} else {
		// Set Type to AzureMarketplace with minimal AzureMarketplace field
		// This signals to the nodepool controller to populate marketplace details from the release payload
		// while preserving the user's image generation preference
		marketplaceImage := &hyperv1.AzureMarketplaceImage{}

		// Set ImageGeneration if specified by the user
		if o.NodePoolOpts.ImageGeneration != "" {
			switch o.NodePoolOpts.ImageGeneration {
			case "Gen1":
				marketplaceImage.ImageGeneration = ptr.To(hyperv1.Gen1)
			case "Gen2":
				marketplaceImage.ImageGeneration = ptr.To(hyperv1.Gen2)
			}
		}

		vmImage = hyperv1.AzureVMImage{
			Type:             hyperv1.AzureMarketplace,
			AzureMarketplace: marketplaceImage,
		}
	}

	azureNodePool := constructor(hyperv1.AzurePlatform, "")
	instanceType := o.NodePoolOpts.InstanceType
	if strings.TrimSpace(instanceType) == "" {
		// Aligning with Azure IPI instance type defaults
		switch azureNodePool.Spec.Arch {
		case hyperv1.ArchitectureAMD64:
			instanceType = "Standard_D4s_v5"
		case hyperv1.ArchitectureARM64:
			instanceType = "Standard_D4ps_v5"
		}
	}

	if len(o.AvailabilityZones) > 0 {
		var nodePools []*hyperv1.NodePool
		for _, availabilityZone := range o.AvailabilityZones {
			nodePool := constructor(hyperv1.AzurePlatform, availabilityZone)
			if nodePool.Spec.Management.UpgradeType == "" {
				nodePool.Spec.Management.UpgradeType = hyperv1.UpgradeTypeReplace
			}
			nodePool.Spec.Platform.Azure = &hyperv1.AzureNodePoolPlatform{
				VMSize: instanceType,
				Image:  vmImage,
				OSDisk: hyperv1.AzureNodePoolOSDisk{
					SizeGiB:                o.NodePoolOpts.DiskSize,
					EncryptionSetID:        o.DiskEncryptionSetID,
					DiskStorageAccountType: hyperv1.AzureDiskStorageAccountType(o.DiskStorageAccountType),
				},
				AvailabilityZone: availabilityZone,
				SubnetID:         o.infra.SubnetID,
				EncryptionAtHost: o.EncryptionAtHost,
			}

			if o.EnableEphemeralOSDisk {
				nodePool.Spec.Platform.Azure.OSDisk.Persistence = hyperv1.EphemeralDiskPersistence
			}

			if len(o.DiagnosticsStorageAccountType) > 0 {
				nodePool.Spec.Platform.Azure.Diagnostics = &hyperv1.Diagnostics{
					StorageAccountType: o.DiagnosticsStorageAccountType,
				}

				if o.DiagnosticsStorageAccountType == hyperv1.AzureDiagnosticsStorageAccountTypeUserManaged &&
					o.DiagnosticsStorageAccountURI != "" {
					nodePool.Spec.Platform.Azure.Diagnostics = &hyperv1.Diagnostics{
						StorageAccountType: o.DiagnosticsStorageAccountType,
						UserManaged: &hyperv1.UserManagedDiagnostics{
							StorageAccountURI: o.DiagnosticsStorageAccountURI,
						},
					}
				}
			}

			nodePools = append(nodePools, nodePool)
		}
		return nodePools
	}

	if azureNodePool.Spec.Management.UpgradeType == "" {
		azureNodePool.Spec.Management.UpgradeType = hyperv1.UpgradeTypeReplace
	}
	azureNodePool.Spec.Platform.Azure = &hyperv1.AzureNodePoolPlatform{
		VMSize:           instanceType,
		Image:            vmImage,
		SubnetID:         o.infra.SubnetID,
		EncryptionAtHost: o.EncryptionAtHost,
		OSDisk: hyperv1.AzureNodePoolOSDisk{
			SizeGiB:                o.NodePoolOpts.DiskSize,
			EncryptionSetID:        o.DiskEncryptionSetID,
			DiskStorageAccountType: hyperv1.AzureDiskStorageAccountType(o.DiskStorageAccountType),
		},
	}

	if o.EnableEphemeralOSDisk {
		azureNodePool.Spec.Platform.Azure.OSDisk.Persistence = hyperv1.EphemeralDiskPersistence
	}

	if len(o.DiagnosticsStorageAccountType) > 0 {
		azureNodePool.Spec.Platform.Azure.Diagnostics = &hyperv1.Diagnostics{
			StorageAccountType: o.DiagnosticsStorageAccountType,
		}

		if o.DiagnosticsStorageAccountType == hyperv1.AzureDiagnosticsStorageAccountTypeUserManaged &&
			o.DiagnosticsStorageAccountURI != "" {
			azureNodePool.Spec.Platform.Azure.Diagnostics = &hyperv1.Diagnostics{
				StorageAccountType: o.DiagnosticsStorageAccountType,
				UserManaged: &hyperv1.UserManagedDiagnostics{
					StorageAccountURI: o.DiagnosticsStorageAccountURI,
				},
			}
		}
	}

	return []*hyperv1.NodePool{azureNodePool}
}

// GenerateResources generates the Kubernetes resources for the Azure HostedCluster create cluster command
func (o *CreateOptions) GenerateResources() ([]crclient.Object, error) {
	var objects []crclient.Object

	// This secret is primarily generated because we need a way to pass the tenant ID to the HCP
	secret := credentialSecret(o.namespace, o.name)
	secret.Data = map[string][]byte{
		"AZURE_SUBSCRIPTION_ID": []byte(o.creds.SubscriptionID),
		"AZURE_TENANT_ID":       []byte(o.creds.TenantID),
	}
	objects = append(objects, secret)

	if len(o.ServiceAccountTokenIssuerKeyPath) > 0 {
		privateKey, err := os.ReadFile(o.ServiceAccountTokenIssuerKeyPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read pull secret file: %w", err)
		}

		saSecret := serviceAccountTokenIssuerSecret(o.namespace, o.name+SATokenIssuerSecret)
		saSecret.Data = map[string][]byte{
			"key": privateKey,
		}
		objects = append(objects, saSecret)
	}

	return objects, nil
}

// CreateInfraOptions creates the Azure infrastructure options for the HostedCluster create cluster command
func CreateInfraOptions(ctx context.Context, azureOpts *ValidatedCreateOptions, opts *core.CreateOptions) (azureinfra.CreateInfraOptions, error) {
	return azureinfra.CreateInfraOptions{
		Name:                        opts.Name,
		Location:                    azureOpts.Location,
		InfraID:                     opts.InfraID,
		CredentialsFile:             azureOpts.CredentialsFile,
		BaseDomain:                  opts.BaseDomain,
		VnetID:                      azureOpts.VnetID,
		ResourceGroupName:           azureOpts.ResourceGroupName,
		NetworkSecurityGroupID:      azureOpts.NetworkSecurityGroupID,
		ResourceGroupTags:           azureOpts.ResourceGroupTags,
		SubnetID:                    azureOpts.SubnetID,
		DNSZoneRG:                   azureOpts.DNSZoneRGName,
		ManagedIdentitiesFile:       azureOpts.ManagedIdentitiesFile,
		DataPlaneIdentitiesFile:     azureOpts.DataPlaneIdentitiesFile,
		WorkloadIdentitiesFile:      azureOpts.WorkloadIdentitiesFile,
		AssignServicePrincipalRoles: azureOpts.AssignServicePrincipalRoles,
		AssignCustomHCPRoles:        azureOpts.AssignCustomHCPRoles,
		DisableClusterCapabilities:  opts.DisableClusterCapabilities,
	}, nil
}
