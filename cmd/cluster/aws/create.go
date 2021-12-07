package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"os/signal"
	"syscall"

	apifixtures "github.com/openshift/hypershift/api/fixtures"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	awsinfra "github.com/openshift/hypershift/cmd/infra/aws"
	"github.com/openshift/hypershift/cmd/util"
	"github.com/spf13/cobra"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
)

func NewCreateCommand(opts *core.CreateOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "aws",
		Short:        "Creates basic functional HostedCluster resources on AWS",
		SilenceUsage: true,
	}

	opts.AWSPlatform = core.AWSPlatformOptions{
		AWSCredentialsFile: "",
		Region:             "us-east-1",
		InstanceType:       "m4.large",
		RootVolumeType:     "gp2",
		RootVolumeSize:     120,
		RootVolumeIOPS:     0,
		EndpointAccess:     string(hyperv1.Public),
	}

	cmd.Flags().StringVar(&opts.AWSPlatform.AWSCredentialsFile, "aws-creds", opts.AWSPlatform.AWSCredentialsFile, "Path to an AWS credentials file (required)")
	cmd.Flags().StringVar(&opts.AWSPlatform.IAMJSON, "iam-json", opts.AWSPlatform.IAMJSON, "Path to file containing IAM information for the cluster. If not specified, IAM will be created")
	cmd.Flags().StringVar(&opts.AWSPlatform.Region, "region", opts.AWSPlatform.Region, "Region to use for AWS infrastructure.")
	cmd.Flags().StringVar(&opts.AWSPlatform.InstanceType, "instance-type", opts.AWSPlatform.InstanceType, "Instance type for AWS instances.")
	cmd.Flags().StringVar(&opts.AWSPlatform.BaseDomain, "base-domain", opts.AWSPlatform.BaseDomain, "The ingress base domain for the cluster")
	cmd.Flags().StringVar(&opts.AWSPlatform.RootVolumeType, "root-volume-type", opts.AWSPlatform.RootVolumeType, "The type of the root volume (e.g. gp2, io1) for machines in the NodePool")
	cmd.Flags().Int64Var(&opts.AWSPlatform.RootVolumeIOPS, "root-volume-iops", opts.AWSPlatform.RootVolumeIOPS, "The iops of the root volume when specifying type:io1 for machines in the NodePool")
	cmd.Flags().Int64Var(&opts.AWSPlatform.RootVolumeSize, "root-volume-size", opts.AWSPlatform.RootVolumeSize, "The size of the root volume (min: 8) for machines in the NodePool")
	cmd.Flags().StringSliceVar(&opts.AWSPlatform.AdditionalTags, "additional-tags", opts.AWSPlatform.AdditionalTags, "Additional tags to set on AWS resources")
	cmd.Flags().StringVar(&opts.AWSPlatform.EndpointAccess, "endpoint-access", opts.AWSPlatform.EndpointAccess, "Access for control plane endpoints (Public, PublicAndPrivate, Private)")

	cmd.MarkFlagRequired("aws-creds")

	cmd.Run = func(cmd *cobra.Command, args []string) {
		ctx, cancel := context.WithCancel(context.Background())
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT)
		go func() {
			<-sigs
			cancel()
		}()

		if err := CreateCluster(ctx, opts); err != nil {
			log.Error(err, "Failed to create cluster")
			os.Exit(1)
		}
	}

	return cmd
}

func CreateCluster(ctx context.Context, opts *core.CreateOptions) error {
	return core.CreateCluster(ctx, opts, applyPlatformSpecificsValues)
}

func applyPlatformSpecificsValues(ctx context.Context, exampleOptions *apifixtures.ExampleOptions, opts *core.CreateOptions) (err error) {
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
	if opts.AWSPlatform.BaseDomain == "" {
		if infra != nil {
			opts.AWSPlatform.BaseDomain = infra.BaseDomain
		} else {
			return fmt.Errorf("base-domain flag is required if infra-json is not provided")
		}
	}
	if infra == nil {
		if len(infraID) == 0 {
			infraID = fmt.Sprintf("%s-%s", opts.Name, utilrand.String(5))
		}
		opt := awsinfra.CreateInfraOptions{
			Region:             opts.AWSPlatform.Region,
			InfraID:            infraID,
			AWSCredentialsFile: opts.AWSPlatform.AWSCredentialsFile,
			Name:               opts.Name,
			BaseDomain:         opts.AWSPlatform.BaseDomain,
			AdditionalTags:     opts.AWSPlatform.AdditionalTags,
		}
		infra, err = opt.CreateInfra(ctx)
		if err != nil {
			return fmt.Errorf("failed to create infra: %w", err)
		}
	}

	var iamInfo *awsinfra.CreateIAMOutput
	if len(opts.AWSPlatform.IAMJSON) > 0 {
		rawIAM, err := ioutil.ReadFile(opts.AWSPlatform.IAMJSON)
		if err != nil {
			return fmt.Errorf("failed to read iam json file: %w", err)
		}
		iamInfo = &awsinfra.CreateIAMOutput{}
		if err = json.Unmarshal(rawIAM, iamInfo); err != nil {
			return fmt.Errorf("failed to load infra json: %w", err)
		}
	} else {
		opt := awsinfra.CreateIAMOptions{
			Region:             opts.AWSPlatform.Region,
			AWSCredentialsFile: opts.AWSPlatform.AWSCredentialsFile,
			InfraID:            infra.InfraID,
			IssuerURL:          opts.AWSPlatform.IssuerURL,
			AdditionalTags:     opts.AWSPlatform.AdditionalTags,
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

	exampleOptions.BaseDomain = infra.BaseDomain
	exampleOptions.ComputeCIDR = infra.ComputeCIDR
	exampleOptions.IssuerURL = iamInfo.IssuerURL
	exampleOptions.PrivateZoneID = infra.PrivateZoneID
	exampleOptions.PublicZoneID = infra.PublicZoneID
	exampleOptions.InfraID = infraID

	exampleOptions.AWS = &apifixtures.ExampleAWSOptions{
		Region:                      infra.Region,
		Zone:                        infra.Zone,
		VPCID:                       infra.VPCID,
		SubnetID:                    infra.PrivateSubnetID,
		SecurityGroupID:             infra.SecurityGroupID,
		InstanceProfile:             iamInfo.ProfileName,
		InstanceType:                opts.AWSPlatform.InstanceType,
		Roles:                       iamInfo.Roles,
		KubeCloudControllerRoleARN:  iamInfo.KubeCloudControllerRoleARN,
		NodePoolManagementRoleARN:   iamInfo.NodePoolManagementRoleARN,
		ControlPlaneOperatorRoleARN: iamInfo.ControlPlaneOperatorRoleARN,
		RootVolumeSize:              opts.AWSPlatform.RootVolumeSize,
		RootVolumeType:              opts.AWSPlatform.RootVolumeType,
		RootVolumeIOPS:              opts.AWSPlatform.RootVolumeIOPS,
		ResourceTags:                tags,
		EndpointAccess:              opts.AWSPlatform.EndpointAccess,
	}
	return nil
}
