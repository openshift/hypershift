package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	awsinfra "github.com/openshift/hypershift/cmd/infra/aws"
	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	"github.com/openshift/hypershift/cmd/util"
	apifixtures "github.com/openshift/hypershift/examples/fixtures"
	"github.com/openshift/hypershift/support/releaseinfo/registryclient"

	"github.com/spf13/cobra"
)

func NewCreateCommand(opts *core.CreateOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "aws",
		Short:        "Creates basic functional HostedCluster resources on AWS",
		SilenceUsage: true,
	}

	opts.AWSPlatform = core.AWSPlatformOptions{
		Region:         "us-east-1",
		InstanceType:   "",
		RootVolumeType: "gp3",
		RootVolumeSize: 120,
		RootVolumeIOPS: 0,
		EndpointAccess: string(hyperv1.Public),
		MultiArch:      false,
	}

	cmd.Flags().StringVar(&opts.AWSPlatform.IAMJSON, "iam-json", opts.AWSPlatform.IAMJSON, "Path to file containing IAM information for the cluster. If not specified, IAM will be created")
	cmd.Flags().StringVar(&opts.AWSPlatform.Region, "region", opts.AWSPlatform.Region, "Region to use for AWS infrastructure.")
	cmd.Flags().StringSliceVar(&opts.AWSPlatform.Zones, "zones", opts.AWSPlatform.Zones, "The availability zones in which NodePools will be created")
	cmd.Flags().StringVar(&opts.AWSPlatform.InstanceType, "instance-type", opts.AWSPlatform.InstanceType, "Instance type for AWS instances.")
	cmd.Flags().StringVar(&opts.AWSPlatform.RootVolumeType, "root-volume-type", opts.AWSPlatform.RootVolumeType, "The type of the root volume (e.g. gp3, io2) for machines in the NodePool")
	cmd.Flags().Int64Var(&opts.AWSPlatform.RootVolumeIOPS, "root-volume-iops", opts.AWSPlatform.RootVolumeIOPS, "The iops of the root volume when specifying type:io1 for machines in the NodePool")
	cmd.Flags().Int64Var(&opts.AWSPlatform.RootVolumeSize, "root-volume-size", opts.AWSPlatform.RootVolumeSize, "The size of the root volume (min: 8) for machines in the NodePool")
	cmd.Flags().StringVar(&opts.AWSPlatform.RootVolumeEncryptionKey, "root-volume-kms-key", opts.AWSPlatform.RootVolumeEncryptionKey, "The KMS key ID or ARN to use for root volume encryption for machines in the NodePool")
	cmd.Flags().StringSliceVar(&opts.AWSPlatform.AdditionalTags, "additional-tags", opts.AWSPlatform.AdditionalTags, "Additional tags to set on AWS resources")
	cmd.Flags().StringVar(&opts.AWSPlatform.EndpointAccess, "endpoint-access", opts.AWSPlatform.EndpointAccess, "Access for control plane endpoints (Public, PublicAndPrivate, Private)")
	cmd.Flags().StringVar(&opts.AWSPlatform.EtcdKMSKeyARN, "kms-key-arn", opts.AWSPlatform.EtcdKMSKeyARN, "The ARN of the KMS key to use for Etcd encryption. If not supplied, etcd encryption will default to using a generated AESCBC key.")
	cmd.Flags().BoolVar(&opts.AWSPlatform.EnableProxy, "enable-proxy", opts.AWSPlatform.EnableProxy, "If a proxy should be set up, rather than allowing direct internet access from the nodes")
	cmd.Flags().StringVar(&opts.CredentialSecretName, "secret-creds", opts.CredentialSecretName, "A Kubernetes secret with needed AWS platform credentials: sts-creds, pull-secret, and a base-domain value. The secret must exist in the supplied \"--namespace\". If a value is provided through the flag '--pull-secret', that value will override the pull-secret value in 'secret-creds'.")
	cmd.Flags().StringVar(&opts.AWSPlatform.IssuerURL, "oidc-issuer-url", "", "The OIDC provider issuer URL")
	cmd.Flags().BoolVar(&opts.AWSPlatform.SingleNATGateway, "single-nat-gateway", opts.AWSPlatform.SingleNATGateway, "If enabled, only a single NAT gateway is created, even if multiple zones are specified")
	cmd.PersistentFlags().BoolVar(&opts.AWSPlatform.MultiArch, "multi-arch", opts.AWSPlatform.MultiArch, "If true, this flag indicates the Hosted Cluster will support multi-arch NodePools and will perform additional validation checks to ensure a multi-arch release image or stream was used.")

	opts.AWSPlatform.AWSCredentialsOpts.BindFlags(cmd.Flags())

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		if opts.Timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
			defer cancel()
		}

		err := validateAWSOptions(ctx, opts)
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

func applyPlatformSpecificsValues(ctx context.Context, exampleOptions *apifixtures.ExampleOptions, opts *core.CreateOptions) (err error) {
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
	if len(opts.CredentialSecretName) > 0 {
		//The opts.BaseDomain value is returned as-is if the input value len(opts.BaseDomain) > 0
		secretData, err = util.ExtractOptionsFromSecret(
			client,
			opts.CredentialSecretName,
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
		opt := awsinfra.CreateInfraOptions{
			Region:                opts.AWSPlatform.Region,
			InfraID:               opts.InfraID,
			AWSCredentialsOpts:    opts.AWSPlatform.AWSCredentialsOpts,
			Name:                  opts.Name,
			BaseDomain:            opts.BaseDomain,
			BaseDomainPrefix:      opts.BaseDomainPrefix,
			AdditionalTags:        opts.AWSPlatform.AdditionalTags,
			Zones:                 opts.AWSPlatform.Zones,
			EnableProxy:           opts.AWSPlatform.EnableProxy,
			SSHKeyFile:            opts.SSHKeyFile,
			SingleNATGateway:      opts.AWSPlatform.SingleNATGateway,
			CredentialsSecretData: secretData,
		}
		infra, err = opt.CreateInfra(ctx, opts.Log)
		if err != nil {
			return fmt.Errorf("failed to create infra: %w", err)
		}
	}

	var iamInfo *awsinfra.CreateIAMOutput
	if len(opts.AWSPlatform.IAMJSON) > 0 {
		rawIAM, err := os.ReadFile(opts.AWSPlatform.IAMJSON)
		if err != nil {
			return fmt.Errorf("failed to read iam json file: %w", err)
		}
		iamInfo = &awsinfra.CreateIAMOutput{}
		if err = json.Unmarshal(rawIAM, iamInfo); err != nil {
			return fmt.Errorf("failed to load infra json: %w", err)
		}
	} else {
		opt := awsinfra.CreateIAMOptions{
			Region:                opts.AWSPlatform.Region,
			AWSCredentialsOpts:    opts.AWSPlatform.AWSCredentialsOpts,
			InfraID:               infra.InfraID,
			IssuerURL:             opts.AWSPlatform.IssuerURL,
			AdditionalTags:        opts.AWSPlatform.AdditionalTags,
			PrivateZoneID:         infra.PrivateZoneID,
			PublicZoneID:          infra.PublicZoneID,
			LocalZoneID:           infra.LocalZoneID,
			KMSKeyARN:             opts.AWSPlatform.EtcdKMSKeyARN,
			CredentialsSecretData: secretData,
		}
		iamInfo, err = opt.CreateIAM(ctx, client)
		if err != nil {
			return fmt.Errorf("failed to create iam: %w", err)
		}
	}

	tagMap, err := util.ParseAWSTags(opts.AWSPlatform.AdditionalTags)
	if err != nil {
		return fmt.Errorf("failed to parse additional tags: %w", err)
	}
	var tags []hyperv1.AWSResourceTag
	for k, v := range tagMap {
		tags = append(tags, hyperv1.AWSResourceTag{Key: k, Value: v})
	}

	var instanceType string
	if opts.AWSPlatform.InstanceType != "" {
		instanceType = opts.AWSPlatform.InstanceType
	} else {
		// Aligning with AWS IPI instance type defaults
		switch opts.Arch {
		case hyperv1.ArchitectureAMD64:
			instanceType = "m5.large"
		case hyperv1.ArchitectureARM64:
			instanceType = "m6g.large"
		}
	}

	exampleOptions.BaseDomain = infra.BaseDomain
	exampleOptions.BaseDomainPrefix = infra.BaseDomainPrefix
	exampleOptions.MachineCIDR = infra.MachineCIDR
	exampleOptions.IssuerURL = iamInfo.IssuerURL
	exampleOptions.PrivateZoneID = infra.PrivateZoneID
	exampleOptions.PublicZoneID = infra.PublicZoneID
	exampleOptions.InfraID = infra.InfraID
	exampleOptions.ExternalDNSDomain = opts.ExternalDNSDomain
	if exampleOptions.EtcdStorageClass == "" {
		exampleOptions.EtcdStorageClass = "gp3-csi"
	}
	var zones []apifixtures.ExampleAWSOptionsZones
	for _, outputZone := range infra.Zones {
		zones = append(zones, apifixtures.ExampleAWSOptionsZones{
			Name:     outputZone.Name,
			SubnetID: &outputZone.SubnetID,
		})
	}

	exampleOptions.AWS = &apifixtures.ExampleAWSOptions{
		Region:                  infra.Region,
		Zones:                   zones,
		VPCID:                   infra.VPCID,
		InstanceProfile:         iamInfo.ProfileName,
		InstanceType:            instanceType,
		Roles:                   iamInfo.Roles,
		KMSProviderRoleARN:      iamInfo.KMSProviderRoleARN,
		KMSKeyARN:               iamInfo.KMSKeyARN,
		RootVolumeSize:          opts.AWSPlatform.RootVolumeSize,
		RootVolumeType:          opts.AWSPlatform.RootVolumeType,
		RootVolumeIOPS:          opts.AWSPlatform.RootVolumeIOPS,
		RootVolumeEncryptionKey: opts.AWSPlatform.RootVolumeEncryptionKey,
		ResourceTags:            tags,
		EndpointAccess:          opts.AWSPlatform.EndpointAccess,
		ProxyAddress:            infra.ProxyAddr,
		MultiArch:               opts.AWSPlatform.MultiArch,
	}
	return nil
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
func validateMultiArchRelease(ctx context.Context, opts *core.CreateOptions) error {
	// Validate the release image is multi-arch when the multi-arch flag is set and a release image is provided
	if opts.AWSPlatform.MultiArch && len(opts.ReleaseImage) > 0 {
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
	if opts.AWSPlatform.MultiArch && len(opts.ReleaseStream) > 0 && !strings.Contains(opts.ReleaseStream, "multi") {
		return fmt.Errorf("release stream is not a multi-arch stream")
	}

	return nil
}

// validateAWSOptions validates different AWS flag parameters
func validateAWSOptions(ctx context.Context, opts *core.CreateOptions) error {
	if err := ValidateCreateCredentialInfo(opts.AWSPlatform.AWSCredentialsOpts, opts.CredentialSecretName, opts.Namespace, opts.PullSecretFile); err != nil {
		return err
	}

	if err := validateMultiArchRelease(ctx, opts); err != nil {
		return err
	}

	return nil
}
