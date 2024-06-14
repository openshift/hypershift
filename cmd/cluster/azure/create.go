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
	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/spf13/pflag"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/spf13/cobra"
	"sigs.k8s.io/yaml"
)

func DefaultOptions() *CreateOptions {
	return &CreateOptions{
		Location:     "eastus",
		InstanceType: "Standard_D4s_v4",
		DiskSizeGB:   120,
	}
}

func BindOptions(opts *CreateOptions, flags *pflag.FlagSet) {
	flags.StringVar(&opts.CredentialsFile, "azure-creds", opts.CredentialsFile, "Path to an Azure credentials file (required)")
	flags.StringVar(&opts.Location, "location", opts.Location, "Location for the cluster")
	flags.StringVar(&opts.EncryptionKeyID, "encryption-key-id", opts.EncryptionKeyID, "etcd encryption key identifier in the form of https://<vaultName>.vault.azure.net/keys/<keyName>/<keyVersion>")
	flags.StringVar(&opts.InstanceType, "instance-type", opts.InstanceType, "The instance type to use for nodes")
	flags.Int32Var(&opts.DiskSizeGB, "root-disk-size", opts.DiskSizeGB, "The size of the root disk for machines in the NodePool (minimum 16)")
	flags.StringSliceVar(&opts.AvailabilityZones, "availability-zones", opts.AvailabilityZones, "The availability zones in which NodePools will be created. Must be left unspecified if the region does not support AZs. If set, one nodepool per zone will be created.")
	flags.StringVar(&opts.ResourceGroupName, "resource-group-name", opts.ResourceGroupName, "A resource group name to create the HostedCluster infrastructure resources under.")
	flags.StringVar(&opts.VnetID, "vnet-id", opts.VnetID, "An existing VNET ID.")
	flags.StringVar(&opts.DiskEncryptionSetID, "disk-encryption-set-id", opts.DiskEncryptionSetID, "The Disk Encryption Set ID to use to encrypt the OS disks for the VMs.")
	flags.StringVar(&opts.NetworkSecurityGroupID, "network-security-group-id", opts.NetworkSecurityGroupID, "The Network Security Group ID to use in the default NodePool.")
	flags.BoolVar(&opts.EnableEphemeralOSDisk, "enable-ephemeral-disk", opts.EnableEphemeralOSDisk, "If enabled, the Azure VMs in the default NodePool will be setup with ephemeral OS disks")
	flags.StringVar(&opts.DiskStorageAccountType, "disk-storage-account-type", opts.DiskStorageAccountType, "The disk storage account type for the OS disks for the VMs.")
	flags.StringToStringVarP(&opts.ResourceGroupTags, "resource-group-tags", "t", opts.ResourceGroupTags, "Additional tags to apply to the resource group created (e.g. 'key1=value1,key2=value2')")
	flags.StringVar(&opts.SubnetID, "subnet-id", opts.SubnetID, "The subnet ID where the VMs will be placed.")
}

type CreateOptions struct {
	CredentialsFile        string
	Location               string
	EncryptionKeyID        string
	InstanceType           string
	DiskSizeGB             int32
	AvailabilityZones      []string
	ResourceGroupName      string
	VnetID                 string
	DiskEncryptionSetID    string
	NetworkSecurityGroupID string
	EnableEphemeralOSDisk  bool
	DiskStorageAccountType string
	ResourceGroupTags      map[string]string
	SubnetID               string

	externalDNSDomain string
	name, namespace   string

	infra         *azureinfra.CreateInfraOutput
	encryptionKey *AzureEncryptionKey
	creds         util.AzureCreds
}

type AzureEncryptionKey struct {
	KeyVaultName string
	KeyName      string
	KeyVersion   string
}

func (o *CreateOptions) Validate(ctx context.Context, opts *core.CreateOptions) error {
	// Check if the network security group is set and the resource group is not
	if o.NetworkSecurityGroupID != "" && o.ResourceGroupName == "" {
		return fmt.Errorf("flag --resource-group-name is required when using --network-security-group-id")
	}

	// Validate a resource group is provided when using the disk encryption set id flag
	if o.DiskEncryptionSetID != "" && o.ResourceGroupName == "" {
		return fmt.Errorf("--resource-group-name is required when using --disk-encryption-set-id")
	}
	return nil
}

func (o *CreateOptions) Complete(ctx context.Context, opts *core.CreateOptions) error {
	o.name, o.namespace = opts.Name, opts.Namespace
	o.externalDNSDomain = opts.ExternalDNSDomain

	if opts.InfrastructureJSON != "" {
		rawInfra, err := os.ReadFile(opts.InfrastructureJSON)
		if err != nil {
			return fmt.Errorf("failed to read infra json file: %w", err)
		}
		if err := yaml.Unmarshal(rawInfra, &o.infra); err != nil {
			return fmt.Errorf("failed to deserialize infra json file: %w", err)
		}
	} else {
		infraOpts, err := CreateInfraOptions(ctx, o, opts)
		if err != nil {
			return err
		}
		o.infra, err = infraOpts.Run(ctx, opts.Log)
		if err != nil {
			return fmt.Errorf("failed to create infra: %w", err)
		}
	}

	if o.EncryptionKeyID != "" {
		parsedKeyId, err := url.Parse(o.EncryptionKeyID)
		if err != nil {
			return fmt.Errorf("invalid encryption key identifier: %v", err)
		}

		key := strings.Split(strings.TrimPrefix(parsedKeyId.Path, "/keys/"), "/")
		if len(key) != 2 {
			return fmt.Errorf("invalid encryption key identifier, couldn't retrieve key name and version: %v", err)
		}

		o.encryptionKey = &AzureEncryptionKey{
			KeyVaultName: strings.Split(parsedKeyId.Hostname(), ".")[0],
			KeyName:      key[0],
			KeyVersion:   key[1],
		}
	}

	azureCredsRaw, err := os.ReadFile(o.CredentialsFile)
	if err != nil {
		return fmt.Errorf("failed to read --azure-creds file %s: %w", o.CredentialsFile, err)
	}
	if err := yaml.Unmarshal(azureCredsRaw, &o.creds); err != nil {
		return fmt.Errorf("failed to unmarshal --azure-creds file: %w", err)
	}

	return nil
}

func (o *CreateOptions) ApplyPlatformSpecifics(cluster *hyperv1.HostedCluster) error {
	cluster.Spec.DNS = hyperv1.DNSSpec{
		BaseDomain:    o.infra.BaseDomain,
		PublicZoneID:  o.infra.PublicZoneID,
		PrivateZoneID: o.infra.PrivateZoneID,
	}

	cluster.Spec.Platform = hyperv1.PlatformSpec{
		Type: hyperv1.AzurePlatform,
		Azure: &hyperv1.AzurePlatformSpec{
			Credentials:       corev1.LocalObjectReference{Name: credentialSecret(cluster.Namespace, cluster.Name).Name},
			SubscriptionID:    o.creds.SubscriptionID,
			Location:          o.infra.Location,
			ResourceGroupName: o.infra.ResourceGroupName,
			VnetID:            o.infra.VNetID,
			SubnetID:          o.infra.SubnetID,
			MachineIdentityID: o.infra.MachineIdentityID,
			SecurityGroupID:   o.infra.SecurityGroupID,
		},
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
			case hyperv1.OVNSbDb:
				cluster.Spec.Services[i].Route = &hyperv1.RoutePublishingStrategy{
					Hostname: fmt.Sprintf("ovn-sbdb-%s.%s", cluster.Name, o.externalDNSDomain),
				}
			}
		}
	}
	return nil
}

func credentialSecret(namespace, name string) *corev1.Secret {
	return &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name + "-cloud-credentials",
			Namespace: namespace,
		},
	}
}

func (o *CreateOptions) GenerateNodePools(constructor core.DefaultNodePoolConstructor) []*hyperv1.NodePool {
	if len(o.AvailabilityZones) > 0 {
		var nodePools []*hyperv1.NodePool
		for _, availabilityZone := range o.AvailabilityZones {
			nodePool := constructor(hyperv1.AzurePlatform, availabilityZone)
			if nodePool.Spec.Management.UpgradeType == "" {
				nodePool.Spec.Management.UpgradeType = hyperv1.UpgradeTypeReplace
			}
			nodePool.Spec.Platform.Azure = &hyperv1.AzureNodePoolPlatform{
				VMSize:                 o.InstanceType,
				ImageID:                o.infra.BootImageID,
				DiskSizeGB:             o.DiskSizeGB,
				AvailabilityZone:       availabilityZone,
				DiskEncryptionSetID:    o.DiskEncryptionSetID,
				EnableEphemeralOSDisk:  o.EnableEphemeralOSDisk,
				DiskStorageAccountType: o.DiskStorageAccountType,
			}
			nodePools = append(nodePools, nodePool)
		}
		return nodePools
	}
	nodePool := constructor(hyperv1.AzurePlatform, "")
	if nodePool.Spec.Management.UpgradeType == "" {
		nodePool.Spec.Management.UpgradeType = hyperv1.UpgradeTypeReplace
	}
	nodePool.Spec.Platform.Azure = &hyperv1.AzureNodePoolPlatform{
		VMSize:                 o.InstanceType,
		ImageID:                o.infra.BootImageID,
		DiskSizeGB:             o.DiskSizeGB,
		DiskEncryptionSetID:    o.DiskEncryptionSetID,
		EnableEphemeralOSDisk:  o.EnableEphemeralOSDisk,
		DiskStorageAccountType: o.DiskStorageAccountType,
	}
	return []*hyperv1.NodePool{nodePool}
}

func (o *CreateOptions) GenerateResources() ([]client.Object, error) {
	secret := credentialSecret(o.namespace, o.name)
	secret.Data = map[string][]byte{
		"AZURE_SUBSCRIPTION_ID": []byte(o.creds.SubscriptionID),
		"AZURE_TENANT_ID":       []byte(o.creds.TenantID),
		"AZURE_CLIENT_ID":       []byte(o.creds.ClientID),
		"AZURE_CLIENT_SECRET":   []byte(o.creds.ClientSecret),
	}
	return []client.Object{secret}, nil
}

var _ core.Platform = (*CreateOptions)(nil)

func NewCreateCommand(opts *core.CreateOptions) *cobra.Command {
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

		if err := CreateCluster(ctx, opts, azureOpts); err != nil {
			opts.Log.Error(err, "Failed to create cluster")
			return err
		}
		return nil
	}

	return cmd
}

func CreateCluster(ctx context.Context, opts *core.CreateOptions, azureOpts *CreateOptions) error {
	return core.CreateCluster(ctx, opts, azureOpts)
}

func CreateInfraOptions(ctx context.Context, azureOpts *CreateOptions, opts *core.CreateOptions) (azureinfra.CreateInfraOptions, error) {
	rhcosImage, err := lookupRHCOSImage(ctx, opts.Arch, opts.ReleaseImage, opts.PullSecretFile)
	if err != nil {
		return azureinfra.CreateInfraOptions{}, fmt.Errorf("failed to retrieve RHCOS image: %w", err)
	}

	return azureinfra.CreateInfraOptions{
		Name:                   opts.Name,
		Location:               azureOpts.Location,
		InfraID:                opts.InfraID,
		CredentialsFile:        azureOpts.CredentialsFile,
		BaseDomain:             opts.BaseDomain,
		RHCOSImage:             rhcosImage,
		VnetID:                 azureOpts.VnetID,
		ResourceGroupName:      azureOpts.ResourceGroupName,
		NetworkSecurityGroupID: azureOpts.NetworkSecurityGroupID,
		ResourceGroupTags:      azureOpts.ResourceGroupTags,
		SubnetID:               azureOpts.SubnetID,
	}, nil
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
