package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"

	"github.com/spf13/cobra"
	utilrand "k8s.io/apimachinery/pkg/util/rand"

	apifixtures "github.com/openshift/hypershift/api/fixtures"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	awsinfra "github.com/openshift/hypershift/cmd/infra/aws"
	"github.com/openshift/hypershift/cmd/util"
)

type CreateOptions struct {
	AWSCredentialsFile string
	AdditionalTags     []string
	IAMJSON            string
	InstanceType       string
	IssuerURL          string
	PrivateZoneID      string
	PublicZoneID       string
	Region             string
	RootVolumeIOPS     int64
	RootVolumeSize     int64
	RootVolumeType     string
	EndpointAccess     string
	Zones              []string
	EtcdKMSKeyARN      string
}

func NewCreateCommand(opts *core.CreateOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "aws",
		Short:        "Creates basic functional HostedCluster resources on AWS",
		SilenceUsage: true,
	}

	platformOpts := CreateOptions{
		AWSCredentialsFile: "",
		Region:             "us-east-1",
		InstanceType:       "m5.large",
		RootVolumeType:     "gp3",
		RootVolumeSize:     120,
		RootVolumeIOPS:     0,
		EndpointAccess:     string(hyperv1.Public),
	}

	cmd.Flags().StringVar(&platformOpts.AWSCredentialsFile, "aws-creds", platformOpts.AWSCredentialsFile, "Path to an AWS credentials file (required)")
	cmd.Flags().StringVar(&platformOpts.IAMJSON, "iam-json", platformOpts.IAMJSON, "Path to file containing IAM information for the cluster. If not specified, IAM will be created")
	cmd.Flags().StringVar(&platformOpts.Region, "region", platformOpts.Region, "Region to use for AWS infrastructure.")
	cmd.Flags().StringSliceVar(&platformOpts.Zones, "zones", platformOpts.Zones, "The availablity zones in which NodePools will be created")
	cmd.Flags().StringVar(&platformOpts.InstanceType, "instance-type", platformOpts.InstanceType, "Instance type for AWS instances.")
	cmd.Flags().StringVar(&platformOpts.RootVolumeType, "root-volume-type", platformOpts.RootVolumeType, "The type of the root volume (e.g. gp3, io2) for machines in the NodePool")
	cmd.Flags().Int64Var(&platformOpts.RootVolumeIOPS, "root-volume-iops", platformOpts.RootVolumeIOPS, "The iops of the root volume when specifying type:io1 for machines in the NodePool")
	cmd.Flags().Int64Var(&platformOpts.RootVolumeSize, "root-volume-size", platformOpts.RootVolumeSize, "The size of the root volume (min: 8) for machines in the NodePool")
	cmd.Flags().StringSliceVar(&platformOpts.AdditionalTags, "additional-tags", platformOpts.AdditionalTags, "Additional tags to set on AWS resources")
	cmd.Flags().StringVar(&platformOpts.EndpointAccess, "endpoint-access", platformOpts.EndpointAccess, "Access for control plane endpoints (Public, PublicAndPrivate, Private)")
	cmd.Flags().StringVar(&platformOpts.EtcdKMSKeyARN, "kms-key-arn", platformOpts.EtcdKMSKeyARN, "The ARN of the KMS key to use for Etcd encryption. If not supplied, etcd encryption will default to using a generated AESCBC key.")

	cmd.MarkFlagRequired("aws-creds")

	cmd.RunE = opts.CreateRunFunc(&platformOpts)

	return cmd
}

func (o *CreateOptions) ApplyPlatformSpecifics(ctx context.Context, exampleOptions *apifixtures.ExampleOptions, opts *core.CreateOptions) (err error) {
	client := util.GetClientOrDie()
	infraID := opts.InfraID

	// Load or create infrastructure for the cluster
	var infra *awsinfra.CreateInfraOutput
	if len(opts.InfrastructureJSON) > 0 {
		rawInfra, err := ioutil.ReadFile(opts.InfrastructureJSON)
		if err != nil {
			return fmt.Errorf("failed to read infra json file: %w", err)
		}
		infra = &awsinfra.CreateInfraOutput{}
		if err = json.Unmarshal(rawInfra, infra); err != nil {
			return fmt.Errorf("failed to load infra json: %w", err)
		}
	}
	if opts.BaseDomain == "" {
		if infra != nil {
			opts.BaseDomain = infra.BaseDomain
		} else {
			return fmt.Errorf("base-domain flag is required if infra-json is not provided")
		}
	}
	if infra == nil {
		if len(infraID) == 0 {
			infraID = fmt.Sprintf("%s-%s", opts.Name, utilrand.String(5))
		}
		opt := awsinfra.CreateInfraOptions{
			Region:             o.Region,
			InfraID:            infraID,
			AWSCredentialsFile: o.AWSCredentialsFile,
			Name:               opts.Name,
			BaseDomain:         opts.BaseDomain,
			AdditionalTags:     o.AdditionalTags,
			Zones:              o.Zones,
		}
		infra, err = opt.CreateInfra(ctx)
		if err != nil {
			return fmt.Errorf("failed to create infra: %w", err)
		}
	}

	var iamInfo *awsinfra.CreateIAMOutput
	if len(o.IAMJSON) > 0 {
		rawIAM, err := ioutil.ReadFile(o.IAMJSON)
		if err != nil {
			return fmt.Errorf("failed to read iam json file: %w", err)
		}
		iamInfo = &awsinfra.CreateIAMOutput{}
		if err = json.Unmarshal(rawIAM, iamInfo); err != nil {
			return fmt.Errorf("failed to load infra json: %w", err)
		}
	} else {
		opt := awsinfra.CreateIAMOptions{
			Region:             o.Region,
			AWSCredentialsFile: o.AWSCredentialsFile,
			InfraID:            infra.InfraID,
			IssuerURL:          o.IssuerURL,
			AdditionalTags:     o.AdditionalTags,
			PrivateZoneID:      infra.PrivateZoneID,
			PublicZoneID:       infra.PublicZoneID,
			LocalZoneID:        infra.LocalZoneID,
			KMSKeyARN:          o.EtcdKMSKeyARN,
		}
		iamInfo, err = opt.CreateIAM(ctx, client)
		if err != nil {
			return fmt.Errorf("failed to create iam: %w", err)
		}
	}

	tagMap, err := util.ParseAWSTags(o.AdditionalTags)
	if err != nil {
		return fmt.Errorf("failed to parse additional tags: %w", err)
	}
	var tags []hyperv1.AWSResourceTag
	for k, v := range tagMap {
		tags = append(tags, hyperv1.AWSResourceTag{Key: k, Value: v})
	}

	exampleOptions.BaseDomain = infra.BaseDomain
	exampleOptions.ComputeCIDR = infra.ComputeCIDR
	exampleOptions.IssuerURL = iamInfo.IssuerURL
	exampleOptions.PrivateZoneID = infra.PrivateZoneID
	exampleOptions.PublicZoneID = infra.PublicZoneID
	exampleOptions.InfraID = infraID
	var zones []apifixtures.ExampleAWSOptionsZones
	for _, outputZone := range infra.Zones {
		zones = append(zones, apifixtures.ExampleAWSOptionsZones{
			Name:     outputZone.Name,
			SubnetID: &outputZone.SubnetID,
		})
	}
	exampleOptions.AWS = &apifixtures.ExampleAWSOptions{
		Region:                      infra.Region,
		Zones:                       zones,
		VPCID:                       infra.VPCID,
		SecurityGroupID:             infra.SecurityGroupID,
		InstanceProfile:             iamInfo.ProfileName,
		InstanceType:                o.InstanceType,
		Roles:                       iamInfo.Roles,
		KubeCloudControllerRoleARN:  iamInfo.KubeCloudControllerRoleARN,
		NodePoolManagementRoleARN:   iamInfo.NodePoolManagementRoleARN,
		ControlPlaneOperatorRoleARN: iamInfo.ControlPlaneOperatorRoleARN,
		KMSProviderRoleARN:          iamInfo.KMSProviderRoleARN,
		KMSKeyARN:                   iamInfo.KMSKeyARN,
		RootVolumeSize:              o.RootVolumeSize,
		RootVolumeType:              o.RootVolumeType,
		RootVolumeIOPS:              o.RootVolumeIOPS,
		ResourceTags:                tags,
		EndpointAccess:              o.EndpointAccess,
	}
	return nil
}
