package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/api/util/ipnet"
	"github.com/openshift/hypershift/cmd/cluster/core"
	awsinfra "github.com/openshift/hypershift/cmd/infra/aws"
	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/support/releaseinfo/registryclient"
	hyperutil "github.com/openshift/hypershift/support/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/spf13/cobra"
	flag "github.com/spf13/pflag"
)

type CreateOptions struct {
	Credentials             awsutil.AWSCredentialsOptions
	CredentialSecretName    string
	AdditionalTags          []string
	IAMJSON                 string
	InstanceType            string
	IssuerURL               string
	PrivateZoneID           string
	PublicZoneID            string
	Region                  string
	RootVolumeIOPS          int64
	RootVolumeSize          int64
	RootVolumeType          string
	RootVolumeEncryptionKey string
	EndpointAccess          string
	Zones                   []string
	EtcdKMSKeyARN           string
	EnableProxy             bool
	SingleNATGateway        bool
	MultiArch               bool

	infra             *awsinfra.CreateInfraOutput
	iamInfo           *awsinfra.CreateIAMOutput
	arch              string
	externalDNSDomain string
}

func (o *CreateOptions) Validate(ctx context.Context, opts *core.CreateOptions) error {
	// Validate if mgmt cluster and NodePool CPU arches don't match, a multi-arch release image or stream was used
	// Exception for ppc64le arch since management cluster would be in x86 and node pools are going to be in ppc64le arch
	if !o.MultiArch && (!opts.Render || opts.RenderInto != "") && opts.Arch != hyperv1.ArchitecturePPC64LE {
		mgmtClusterCPUArch, err := hyperutil.GetMgmtClusterCPUArch(ctx)
		if err != nil {
			return err
		}

		if err = hyperutil.DoesMgmtClusterAndNodePoolCPUArchMatch(mgmtClusterCPUArch, opts.Arch); err != nil {
			opts.Log.Info(fmt.Sprintf("WARNING: %v", err))
		}
	}
	return nil
}

func (o *CreateOptions) Complete(ctx context.Context, opts *core.CreateOptions) error {
	o.arch = opts.Arch
	o.externalDNSDomain = opts.ExternalDNSDomain

	if opts.EtcdStorageClass == "" {
		opts.EtcdStorageClass = "gp3-csi"
	}

	client, err := util.GetClient()
	if err != nil {
		return err
	}

	// Load or create infrastructure for the cluster
	var infra *awsinfra.CreateInfraOutput
	if len(opts.InfrastructureJSON) > 0 {
		rawInfra, err := os.ReadFile(opts.InfrastructureJSON)
		if err != nil {
			return fmt.Errorf("failed to read infra json file: %w", err)
		}
		infra = &awsinfra.CreateInfraOutput{}
		if err = json.Unmarshal(rawInfra, infra); err != nil {
			return fmt.Errorf("failed to load infra json: %w", err)
		}
	}

	var secretData *util.CredentialsSecretData
	if len(o.CredentialSecretName) > 0 {
		//The opts.BaseDomain value is returned as-is if the input value len(opts.BaseDomain) > 0
		secretData, err = util.ExtractOptionsFromSecret(
			client,
			o.CredentialSecretName,
			opts.Namespace,
			opts.BaseDomain)
		if err != nil {
			return err
		}
		opts.BaseDomain = secretData.BaseDomain
	}
	if opts.BaseDomain == "" {
		if infra != nil {
			opts.BaseDomain = infra.BaseDomain
		} else {
			return fmt.Errorf("base-domain flag is required if infra-json is not provided")
		}
	}
	if infra == nil {
		opt := CreateInfraOptions(o, opts)
		infra, err = opt.CreateInfra(ctx, opts.Log)
		if err != nil {
			return fmt.Errorf("failed to create infra: %w", err)
		}
	}
	o.infra = infra

	var iamInfo *awsinfra.CreateIAMOutput
	if len(o.IAMJSON) > 0 {
		rawIAM, err := os.ReadFile(o.IAMJSON)
		if err != nil {
			return fmt.Errorf("failed to read iam json file: %w", err)
		}
		iamInfo = &awsinfra.CreateIAMOutput{}
		if err = json.Unmarshal(rawIAM, iamInfo); err != nil {
			return fmt.Errorf("failed to load infra json: %w", err)
		}
	} else {
		opt := CreateIAMOptions(o, infra)
		iamInfo, err = opt.CreateIAM(ctx, client)
		if err != nil {
			return fmt.Errorf("failed to create iam: %w", err)
		}
	}
	o.iamInfo = iamInfo

	// TODO: drop support for this flag, it's really muddying the waters for the CLI
	if len(o.CredentialSecretName) > 0 {
		var secret *corev1.Secret
		secret, err = util.GetSecret(o.CredentialSecretName, opts.Namespace)
		if err != nil {
			return err
		}
		for from, into := range map[string]*[]byte{
			"pullSecret":     &opts.PullSecret,
			"ssh-publickey":  &opts.PublicKey,
			"ssh-privatekey": &opts.PrivateKey,
		} {
			value := secret.Data[from]
			if len(value) == 0 {
				return fmt.Errorf("secret %s/%s does not contain key %q", opts.Namespace, o.CredentialSecretName, from)
			}
			*into = value
		}
	}
	return nil
}

func (o *CreateOptions) ApplyPlatformSpecifics(cluster *hyperv1.HostedCluster) error {
	tagMap, err := util.ParseAWSTags(o.AdditionalTags)
	if err != nil {
		return fmt.Errorf("failed to parse additional tags: %w", err)
	}
	var tags []hyperv1.AWSResourceTag
	for k, v := range tagMap {
		tags = append(tags, hyperv1.AWSResourceTag{Key: k, Value: v})
	}

	cluster.Spec.InfraID = o.infra.InfraID
	cluster.Spec.IssuerURL = o.iamInfo.IssuerURL

	if o.infra.MachineCIDR != "" {
		cluster.Spec.Networking.MachineNetwork = []hyperv1.MachineNetworkEntry{{CIDR: *ipnet.MustParseCIDR(o.infra.MachineCIDR)}}
	}

	var baseDomainPrefix *string
	if o.infra.BaseDomainPrefix == "none" {
		baseDomainPrefix = ptr.To("")
	} else if o.infra.BaseDomainPrefix != "" {
		baseDomainPrefix = ptr.To(o.infra.BaseDomainPrefix)
	}
	cluster.Spec.DNS = hyperv1.DNSSpec{
		BaseDomain:       o.infra.BaseDomain,
		BaseDomainPrefix: baseDomainPrefix,
		PublicZoneID:     o.infra.PublicZoneID,
		PrivateZoneID:    o.infra.PrivateZoneID,
	}

	endpointAccess := hyperv1.AWSEndpointAccessType(o.EndpointAccess)
	cluster.Spec.Platform = hyperv1.PlatformSpec{
		Type: hyperv1.AWSPlatform,
		AWS: &hyperv1.AWSPlatformSpec{
			Region:   o.Region,
			RolesRef: o.iamInfo.Roles,
			CloudProviderConfig: &hyperv1.AWSCloudProviderConfig{
				VPC: o.infra.VPCID,
				Subnet: &hyperv1.AWSResourceReference{
					ID: &o.infra.Zones[0].SubnetID,
				},
				Zone: o.infra.Zones[0].Name,
			},
			ResourceTags:   tags,
			MultiArch:      o.MultiArch,
			EndpointAccess: endpointAccess,
		},
	}

	if o.infra.ProxyAddr != "" {
		cluster.Spec.Configuration.Proxy = &configv1.ProxySpec{
			HTTPProxy:  o.infra.ProxyAddr,
			HTTPSProxy: o.infra.ProxyAddr,
		}
	}

	if len(o.iamInfo.KMSProviderRoleARN) > 0 {
		cluster.Spec.SecretEncryption = &hyperv1.SecretEncryptionSpec{
			Type: hyperv1.KMS,
			KMS: &hyperv1.KMSSpec{
				Provider: hyperv1.AWS,
				AWS: &hyperv1.AWSKMSSpec{
					Region: o.Region,
					ActiveKey: hyperv1.AWSKMSKeyEntry{
						ARN: o.iamInfo.KMSKeyARN,
					},
					Auth: hyperv1.AWSKMSAuthSpec{
						AWSKMSRoleARN: o.iamInfo.KMSProviderRoleARN,
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
				if endpointAccess == hyperv1.Public {
					cluster.Spec.Services[i].Route = &hyperv1.RoutePublishingStrategy{
						Hostname: fmt.Sprintf("konnectivity-%s.%s", cluster.Name, o.externalDNSDomain),
					}
				}

			case hyperv1.Ignition:
				if endpointAccess == hyperv1.Public {
					cluster.Spec.Services[i].Route = &hyperv1.RoutePublishingStrategy{
						Hostname: fmt.Sprintf("ignition-%s.%s", cluster.Name, o.externalDNSDomain),
					}
				}
			case hyperv1.OVNSbDb:
				if endpointAccess == hyperv1.Public {
					cluster.Spec.Services[i].Route = &hyperv1.RoutePublishingStrategy{
						Hostname: fmt.Sprintf("ovn-sbdb-%s.%s", cluster.Name, o.externalDNSDomain),
					}
				}
			}
		}

	}

	return nil
}

func (o *CreateOptions) GenerateNodePools(constructor core.DefaultNodePoolConstructor) []*hyperv1.NodePool {
	var instanceType string
	if o.InstanceType != "" {
		instanceType = o.InstanceType
	} else {
		// Aligning with AWS IPI instance type defaults
		switch o.arch {
		case hyperv1.ArchitectureAMD64:
			instanceType = "m5.large"
		case hyperv1.ArchitectureARM64:
			instanceType = "m6g.large"
		}
	}

	var nodePools []*hyperv1.NodePool
	for _, zone := range o.infra.Zones {
		nodePool := constructor(hyperv1.AWSPlatform, zone.Name)
		if nodePool.Spec.Management.UpgradeType == "" {
			nodePool.Spec.Management.UpgradeType = hyperv1.UpgradeTypeReplace
		}
		nodePool.Spec.Platform.AWS = &hyperv1.AWSNodePoolPlatform{
			InstanceType:    instanceType,
			InstanceProfile: o.iamInfo.ProfileName,
			Subnet: hyperv1.AWSResourceReference{
				ID: &zone.SubnetID,
			},
			RootVolume: &hyperv1.Volume{
				Size:          o.RootVolumeSize,
				Type:          o.RootVolumeType,
				IOPS:          o.RootVolumeIOPS,
				EncryptionKey: o.RootVolumeEncryptionKey,
			},
		}
		nodePools = append(nodePools, nodePool)
	}
	return nodePools
}

func (o *CreateOptions) GenerateResources() ([]client.Object, error) {
	return nil, nil
}

func DefaultOptions() *CreateOptions {
	return &CreateOptions{
		Region:         "us-east-1",
		RootVolumeType: "gp3",
		RootVolumeSize: 120,
		EndpointAccess: string(hyperv1.Public),
	}
}

func BindOptions(opts *CreateOptions, flags *flag.FlagSet) {
	bindCoreOptions(opts, flags)
	opts.Credentials.BindProductFlags(flags)
}

func bindCoreOptions(opts *CreateOptions, flags *flag.FlagSet) {
	flags.StringVar(&opts.Region, "region", opts.Region, "Region to use for AWS infrastructure.")
	flags.StringSliceVar(&opts.Zones, "zones", opts.Zones, "The availability zones in which NodePools will be created")
	flags.StringVar(&opts.InstanceType, "instance-type", opts.InstanceType, "Instance type for AWS instances.")
	flags.StringVar(&opts.RootVolumeType, "root-volume-type", opts.RootVolumeType, "The type of the root volume (e.g. gp3, io2) for machines in the NodePool")
	flags.Int64Var(&opts.RootVolumeIOPS, "root-volume-iops", opts.RootVolumeIOPS, "The iops of the root volume when specifying type:io1 for machines in the NodePool")
	flags.Int64Var(&opts.RootVolumeSize, "root-volume-size", opts.RootVolumeSize, "The size of the root volume (min: 8) for machines in the NodePool")
	flags.StringVar(&opts.RootVolumeEncryptionKey, "root-volume-kms-key", opts.RootVolumeEncryptionKey, "The KMS key ID or ARN to use for root volume encryption for machines in the NodePool")
	flags.StringSliceVar(&opts.AdditionalTags, "additional-tags", opts.AdditionalTags, "Additional tags to set on AWS resources")
	flags.StringVar(&opts.EndpointAccess, "endpoint-access", opts.EndpointAccess, "Access for control plane endpoints (Public, PublicAndPrivate, Private)")
	flags.StringVar(&opts.EtcdKMSKeyARN, "kms-key-arn", opts.EtcdKMSKeyARN, "The ARN of the KMS key to use for Etcd encryption. If not supplied, etcd encryption will default to using a generated AESCBC key.")
	flags.BoolVar(&opts.EnableProxy, "enable-proxy", opts.EnableProxy, "If a proxy should be set up, rather than allowing direct internet access from the nodes")
	flags.StringVar(&opts.CredentialSecretName, "secret-creds", opts.CredentialSecretName, "A Kubernetes secret with needed AWS platform credentials: sts-creds, pull-secret, and a base-domain value. The secret must exist in the supplied \"--namespace\". If a value is provided through the flag '--pull-secret', that value will override the pull-secret value in 'secret-creds'.")
	flags.StringVar(&opts.IssuerURL, "oidc-issuer-url", "", "The OIDC provider issuer URL")
	flags.BoolVar(&opts.MultiArch, "multi-arch", opts.MultiArch, "If true, this flag indicates the Hosted Cluster will support multi-arch NodePools and will perform additional validation checks to ensure a multi-arch release image or stream was used.")
}

func BindDeveloperOptions(opts *CreateOptions, flags *flag.FlagSet) {
	bindCoreOptions(opts, flags)
	flags.StringVar(&opts.IAMJSON, "iam-json", opts.IAMJSON, "Path to file containing IAM information for the cluster. If not specified, IAM will be created")
	flags.BoolVar(&opts.SingleNATGateway, "single-nat-gateway", opts.SingleNATGateway, "If enabled, only a single NAT gateway is created, even if multiple zones are specified")
	opts.Credentials.BindFlags(flags)
}

var _ core.Platform = (*CreateOptions)(nil)

func NewCreateCommand(opts *core.CreateOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "aws",
		Short:        "Creates basic functional HostedCluster resources on AWS",
		SilenceUsage: true,
	}

	awsOpts := DefaultOptions()
	BindDeveloperOptions(awsOpts, cmd.Flags())
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		if opts.Timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
			defer cancel()
		}

		err := validateAWSOptions(ctx, opts, awsOpts)
		if err != nil {
			return err
		}

		if err = CreateCluster(ctx, opts, awsOpts); err != nil {
			opts.Log.Error(err, "Failed to create cluster")
			return err
		}
		return nil
	}

	return cmd
}

func CreateCluster(ctx context.Context, opts *core.CreateOptions, awsOpts *CreateOptions) error {
	if err := core.Validate(ctx, opts); err != nil {
		return err
	}
	return core.CreateCluster(ctx, opts, awsOpts)
}

func CreateInfraOptions(awsOpts *CreateOptions, opts *core.CreateOptions) awsinfra.CreateInfraOptions {
	return awsinfra.CreateInfraOptions{
		Region:             awsOpts.Region,
		InfraID:            opts.InfraID,
		AWSCredentialsOpts: awsOpts.Credentials,
		Name:               opts.Name,
		BaseDomain:         opts.BaseDomain,
		BaseDomainPrefix:   opts.BaseDomainPrefix,
		AdditionalTags:     awsOpts.AdditionalTags,
		Zones:              awsOpts.Zones,
		EnableProxy:        awsOpts.EnableProxy,
		SSHKeyFile:         opts.SSHKeyFile,
		SingleNATGateway:   awsOpts.SingleNATGateway,
	}
}

func CreateIAMOptions(awsOpts *CreateOptions, infra *awsinfra.CreateInfraOutput) awsinfra.CreateIAMOptions {
	return awsinfra.CreateIAMOptions{
		Region:             awsOpts.Region,
		AWSCredentialsOpts: awsOpts.Credentials,
		InfraID:            infra.InfraID,
		IssuerURL:          awsOpts.IssuerURL,
		AdditionalTags:     awsOpts.AdditionalTags,
		PrivateZoneID:      infra.PrivateZoneID,
		PublicZoneID:       infra.PublicZoneID,
		LocalZoneID:        infra.LocalZoneID,
		KMSKeyARN:          awsOpts.EtcdKMSKeyARN,
	}
}

// ValidateCreateCredentialInfo validates if the credentials secret name is empty that the aws-creds and pull-secret flags are
// not empty; validates if the credentials secret is not empty, that it can be retrieved
func ValidateCreateCredentialInfo(opts awsutil.AWSCredentialsOptions, credentialSecretName, namespace, pullSecretFile string) error {
	if err := ValidateCredentialInfo(opts, credentialSecretName, namespace); err != nil {
		return err
	}

	if len(credentialSecretName) == 0 {
		if err := util.ValidateRequiredOption("pull-secret", pullSecretFile); err != nil {
			return err
		}
	}
	return nil
}

// validateMultiArchRelease validates a release image or release stream is multi-arch if the multi-arch flag is set
func validateMultiArchRelease(ctx context.Context, opts *core.CreateOptions, awsOpts *CreateOptions) error {
	// Validate the release image is multi-arch when the multi-arch flag is set and a release image is provided
	if awsOpts.MultiArch && len(opts.ReleaseImage) > 0 {
		pullSecret, err := os.ReadFile(opts.PullSecretFile)
		if err != nil {
			return fmt.Errorf("failed to read pull secret file: %w", err)
		}

		validMultiArchRelease, err := registryclient.IsMultiArchManifestList(ctx, opts.ReleaseImage, pullSecret)
		if err != nil {
			return err
		}

		if !validMultiArchRelease {
			return fmt.Errorf("release image is not a multi-arch image")
		}
	}

	// Validate the release stream is multi-arch when the multi-arch flag is set and a release stream is provided
	if awsOpts.MultiArch && len(opts.ReleaseStream) > 0 && !strings.Contains(opts.ReleaseStream, "multi") {
		return fmt.Errorf("release stream is not a multi-arch stream")
	}

	return nil
}

// validateAWSOptions validates different AWS flag parameters
func validateAWSOptions(ctx context.Context, opts *core.CreateOptions, awsOpts *CreateOptions) error {
	if err := ValidateCreateCredentialInfo(awsOpts.Credentials, awsOpts.CredentialSecretName, opts.Namespace, opts.PullSecretFile); err != nil {
		return err
	}

	if err := validateMultiArchRelease(ctx, opts, awsOpts); err != nil {
		return err
	}

	return nil
}
