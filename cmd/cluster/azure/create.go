package azure

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	azureinfra "github.com/openshift/hypershift/cmd/infra/azure"
	azurenodepool "github.com/openshift/hypershift/cmd/nodepool/azure"
	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/cmd/version"
	"github.com/openshift/hypershift/support/releaseinfo"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const SATokenIssuerSecret = "sa-token-issuer-key"

func DefaultOptions(client crclient.Client, log logr.Logger) (*RawCreateOptions, error) {
	rawCreateOptions := &RawCreateOptions{
		Location:           "eastus",
		TechPreviewEnabled: false,
		NodePoolOpts:       azurenodepool.DefaultOptions(),
	}

	if client == nil {
		return rawCreateOptions, nil
	}

	techPreviewCM := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "hypershift", Name: "feature-gate"}}
	if err := client.Get(context.Background(), crclient.ObjectKeyFromObject(techPreviewCM), techPreviewCM); err != nil && !apierrors.IsNotFound(err) {
		log.Info("Warning: Failed to get feature-gate configmap, proceeding without tech preview", "error", err)
	}

	if techPreviewCM != nil && techPreviewCM.Data["TechPreviewEnabled"] == "true" {
		rawCreateOptions.TechPreviewEnabled = true
	}

	return rawCreateOptions, nil
}

func BindOptions(opts *RawCreateOptions, flags *pflag.FlagSet) {
	bindCoreOptions(opts, flags)
	azurenodepool.BindOptions(opts.NodePoolOpts, flags)
}

func bindCoreOptions(opts *RawCreateOptions, flags *pflag.FlagSet) {
	flags.StringVar(&opts.CredentialsFile, "azure-creds", opts.CredentialsFile, "Path to an Azure credentials file (required)")
	flags.StringVar(&opts.Location, "location", opts.Location, "Location for the cluster")
	flags.StringVar(&opts.EncryptionKeyID, "encryption-key-id", opts.EncryptionKeyID, "etcd encryption key identifier in the form of https://<vaultName>.vault.azure.net/keys/<keyName>/<keyVersion>")
	flags.StringSliceVar(&opts.AvailabilityZones, "availability-zones", opts.AvailabilityZones, "The availability zones in which NodePools will be created. Must be left unspecified if the region does not support AZs. If set, one nodepool per zone will be created.")
	flags.StringVar(&opts.ResourceGroupName, "resource-group-name", opts.ResourceGroupName, "A resource group name to create the HostedCluster infrastructure resources under.")
	flags.StringVar(&opts.VnetID, "vnet-id", opts.VnetID, "An existing VNET ID.")
	flags.StringVar(&opts.NetworkSecurityGroupID, "network-security-group-id", opts.NetworkSecurityGroupID, "The Network Security Group ID to use in the default NodePool.")
	flags.StringToStringVarP(&opts.ResourceGroupTags, "resource-group-tags", "t", opts.ResourceGroupTags, "Additional tags to apply to the resource group created (e.g. 'key1=value1,key2=value2')")
	flags.StringVar(&opts.SubnetID, "subnet-id", opts.SubnetID, "The subnet ID where the VMs will be placed.")
	flags.StringVar(&opts.IssuerURL, "oidc-issuer-url", "", "The OIDC provider issuer URL")
	flags.StringVar(&opts.ServiceAccountTokenIssuerKeyPath, "sa-token-issuer-private-key-path", "", "The file to the private key for the service account token issuer")

	if opts.TechPreviewEnabled {
		flags.StringVar(&opts.KMSClientID, "kms-client-id", opts.KMSClientID, "The client ID of a managed identity used in KMS to authenticate to Azure.")
		flags.StringVar(&opts.KMSCertName, "kms-cert-name", opts.KMSCertName, "The backing certificate name related to the managed identity used in KMS to authenticate to Azure.")
		flags.StringVar(&opts.DNSZoneRGName, "dns-zone-rg-name", opts.DNSZoneRGName, "The name of the resource group where the DNS Zone resides. This is needed for the ingress controller. This is just the name and not the full ID of the resource group.")
		flags.StringVar(&opts.ManagedIdentitiesFile, "managed-identities-file", opts.ManagedIdentitiesFile, "Path to a file containing the managed identities configuration in json format.")
		flags.StringVar(&opts.DataPlaneIdentitiesFile, "data-plane-identities-file", opts.ManagedIdentitiesFile, "Path to a file containing the client IDs of the managed identities for the data plane configured in json format.")
		flags.BoolVar(&opts.AssignServicePrincipalRoles, "assign-service-principal-roles", opts.AssignServicePrincipalRoles, "Assign the service principal roles to the managed identities.")
		flags.BoolVar(&opts.AssignCustomHCPRoles, "assign-custom-hcp-roles", opts.AssignCustomHCPRoles, "Assign custom roles to HCP identities")
	}
}

func BindDeveloperOptions(opts *RawCreateOptions, flags *pflag.FlagSet) {
	bindCoreOptions(opts, flags)

	flags.StringVar(&opts.RHCOSImage, "rhcos-image", opts.RHCOSImage, "The RHCOS image to use.")
}

type RawCreateOptions struct {
	CredentialsFile                  string
	Location                         string
	EncryptionKeyID                  string
	AvailabilityZones                []string
	ResourceGroupName                string
	VnetID                           string
	NetworkSecurityGroupID           string
	ResourceGroupTags                map[string]string
	SubnetID                         string
	RHCOSImage                       string
	KMSClientID                      string
	KMSCertName                      string
	TechPreviewEnabled               bool
	DNSZoneRGName                    string
	ManagedIdentitiesFile            string
	DataPlaneIdentitiesFile          string
	AssignServicePrincipalRoles      bool
	AssignCustomHCPRoles             bool
	IssuerURL                        string
	ServiceAccountTokenIssuerKeyPath string

	NodePoolOpts *azurenodepool.RawAzurePlatformCreateOptions
}

type AzureEncryptionKey struct {
	KeyVaultName string
	KeyName      string
	KeyVersion   string
}

// validatedCreateOptions is a private wrapper that enforces a call of Validate() before Complete() can be invoked.
type validatedCreateOptions struct {
	*RawCreateOptions

	*azurenodepool.ValidatedAzurePlatformCreateOptions
}

type ValidatedCreateOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*validatedCreateOptions
}

func (o *RawCreateOptions) Validate(_ context.Context, _ *core.CreateOptions) (core.PlatformCompleter, error) {
	var err error

	// Check if the network security group is set and the resource group is not
	if o.NetworkSecurityGroupID != "" && o.ResourceGroupName == "" {
		return nil, fmt.Errorf("flag --resource-group-name is required when using --network-security-group-id")
	}

	if o.TechPreviewEnabled {
		if o.KMSClientID != "" && o.KMSCertName == "" {
			return nil, fmt.Errorf("flag --kms-cert-name is required when using --kms-client-id")
		}

		if o.KMSClientID == "" && o.KMSCertName != "" {
			return nil, fmt.Errorf("flag --kms-client-id is required when using --kms-cert-name")
		}

		if o.ManagedIdentitiesFile == "" {
			return nil, fmt.Errorf("flag --managed-identities-file is required")
		}

		if o.AssignServicePrincipalRoles && o.DNSZoneRGName == "" {
			return nil, fmt.Errorf("flag --dns-zone-rg-name is required")
		}
	}

	validOpts := &ValidatedCreateOptions{
		validatedCreateOptions: &validatedCreateOptions{
			RawCreateOptions: o,
		},
	}

	for _, az := range o.AvailabilityZones {
		if !slices.Contains([]string{"1", "2", "3"}, az) {
			return nil, fmt.Errorf("invalid value for --availability-zone: %s", az)
		}
	}

	validOpts.ValidatedAzurePlatformCreateOptions, err = o.NodePoolOpts.Validate()

	return validOpts, err
}

// completedCreateOptions is a private wrapper that enforces a call of Complete() before cluster creation can be invoked.
type completedCreateOptions struct {
	*ValidatedCreateOptions

	externalDNSDomain string
	name, namespace   string

	infra         *azureinfra.CreateInfraOutput
	encryptionKey *AzureEncryptionKey
	creds         util.AzureCreds
}

type CreateOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*completedCreateOptions
}

func (o *ValidatedCreateOptions) Complete(ctx context.Context, opts *core.CreateOptions) (core.Platform, error) {
	output := &CreateOptions{
		completedCreateOptions: &completedCreateOptions{
			ValidatedCreateOptions: o,
			name:                   opts.Name,
			namespace:              opts.Namespace,
			externalDNSDomain:      opts.ExternalDNSDomain,
		},
	}

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

	if o.EncryptionKeyID != "" {
		parsedKeyId, err := url.Parse(o.EncryptionKeyID)
		if err != nil {
			return nil, fmt.Errorf("invalid encryption key identifier: %v", err)
		}

		key := strings.Split(strings.TrimPrefix(parsedKeyId.Path, "/keys/"), "/")
		if len(key) != 2 {
			return nil, fmt.Errorf("invalid encryption key identifier, couldn't retrieve key name and version: %v", err)
		}

		output.encryptionKey = &AzureEncryptionKey{
			KeyVaultName: strings.Split(parsedKeyId.Hostname(), ".")[0],
			KeyName:      key[0],
			KeyVersion:   key[1],
		}
	}

	azureCredsRaw, err := os.ReadFile(o.CredentialsFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read --azure-creds file %s: %w", o.CredentialsFile, err)
	}
	if err := yaml.Unmarshal(azureCredsRaw, &output.creds); err != nil {
		return nil, fmt.Errorf("failed to unmarshal --azure-creds file: %w", err)
	}

	return output, nil
}

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

	if o.TechPreviewEnabled {
		cluster.Spec.Platform.Azure.ManagedIdentities = o.infra.ControlPlaneMIs
		cluster.Spec.Platform.Azure.ManagedIdentities.DataPlane = o.infra.DataPlaneIdentities
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

	if o.encryptionKey != nil && o.TechPreviewEnabled {
		cluster.Spec.SecretEncryption.KMS.Azure.KMS = hyperv1.ManagedIdentity{
			ClientID:        o.KMSClientID,
			CertificateName: o.KMSCertName,
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

func serviceAccountTokenIssuerSecret(namespace, name string) *corev1.Secret {
	return &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
}

func (o *CreateOptions) GenerateNodePools(constructor core.DefaultNodePoolConstructor) []*hyperv1.NodePool {
	var vmImage hyperv1.AzureVMImage
	if o.MarketplacePublisher == "" {
		vmImage = hyperv1.AzureVMImage{
			Type:    hyperv1.ImageID,
			ImageID: ptr.To(o.infra.BootImageID),
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

	azureNodePool := constructor(hyperv1.AzurePlatform, "")
	instanceType := o.NodePoolOpts.InstanceType
	if strings.TrimSpace(instanceType) == "" {
		// Aligning with Azure IPI instance type defaults
		switch azureNodePool.Spec.Arch {
		case hyperv1.ArchitectureAMD64:
			instanceType = "Standard_D4s_v3"
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

var _ core.Platform = (*CreateOptions)(nil)

func NewCreateCommand(opts *core.RawCreateOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "azure",
		Short:        "Creates basic functional HostedCluster resources on Azure",
		SilenceUsage: true,
	}

	client, err := util.GetClient()
	if err != nil {
		opts.Log.Info(fmt.Sprintf("Failed to get client, proceeding without checking feature gate CM: %s", err.Error()))
	}

	azureOpts, err := DefaultOptions(client, opts.Log)
	if err != nil {
		opts.Log.Error(err, "Failed to create default options")
		return nil
	}
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

func CreateInfraOptions(ctx context.Context, azureOpts *ValidatedCreateOptions, opts *core.CreateOptions) (azureinfra.CreateInfraOptions, error) {
	rhcosImage := azureOpts.RHCOSImage
	if rhcosImage == "" && azureOpts.MarketplacePublisher == "" {
		var err error
		rhcosImage, err = lookupRHCOSImage(ctx, opts.Arch, opts.ReleaseImage, opts.ReleaseStream, opts.PullSecretFile)
		if err != nil {
			return azureinfra.CreateInfraOptions{}, fmt.Errorf("failed to retrieve RHCOS image: %w", err)
		}
	}

	return azureinfra.CreateInfraOptions{
		Name:                        opts.Name,
		Location:                    azureOpts.Location,
		InfraID:                     opts.InfraID,
		CredentialsFile:             azureOpts.CredentialsFile,
		BaseDomain:                  opts.BaseDomain,
		RHCOSImage:                  rhcosImage,
		VnetID:                      azureOpts.VnetID,
		ResourceGroupName:           azureOpts.ResourceGroupName,
		NetworkSecurityGroupID:      azureOpts.NetworkSecurityGroupID,
		ResourceGroupTags:           azureOpts.ResourceGroupTags,
		SubnetID:                    azureOpts.SubnetID,
		TechPreviewEnabled:          azureOpts.TechPreviewEnabled,
		DNSZoneRG:                   azureOpts.DNSZoneRGName,
		ManagedIdentitiesFile:       azureOpts.ManagedIdentitiesFile,
		DataPlaneIdentitiesFile:     azureOpts.DataPlaneIdentitiesFile,
		AssignServicePrincipalRoles: azureOpts.AssignServicePrincipalRoles,
		AssignCustomHCPRoles:        azureOpts.AssignCustomHCPRoles,
	}, nil
}

// lookupRHCOSImage looks up a release image and extracts the RHCOS VHD image based on the nodepool arch
func lookupRHCOSImage(ctx context.Context, arch, image, releaseStream, pullSecretFile string) (string, error) {
	if len(image) == 0 && len(releaseStream) != 0 {
		defaultVersion, err := version.LookupDefaultOCPVersion(releaseStream)
		if err != nil {
			return "", fmt.Errorf("failed to lookup OCP release image for release stream, %s: %w", releaseStream, err)
		}
		image = defaultVersion.PullSpec
	}

	rhcosImage := ""
	releaseProvider := &releaseinfo.RegistryClientProvider{}

	pullSecret, err := os.ReadFile(pullSecretFile)
	if err != nil {
		return "", fmt.Errorf("failed to read pull secret file: %w", err)
	}

	releaseImage, err := releaseProvider.Lookup(ctx, image, pullSecret)
	if err != nil {
		return "", fmt.Errorf("failed to lookup release image: %w", err)
	}

	// We need to translate amd64 to x86_64 and arm64 to aarch64 since that is what is in the release image stream
	if _, ok := releaseImage.StreamMetadata.Architectures[hyperv1.ArchAliases[arch]]; !ok {
		return "", fmt.Errorf("arch does not exist in release image, arch: %s", arch)
	}

	rhcosImage = releaseImage.StreamMetadata.Architectures[hyperv1.ArchAliases[arch]].RHCOS.AzureDisk.URL

	if rhcosImage == "" {
		return "", fmt.Errorf("RHCOS VHD image is empty: %w", err)
	}

	return rhcosImage, nil
}
