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
	nodepoolaws "github.com/openshift/hypershift/cmd/nodepool/aws"
	nodepoolcore "github.com/openshift/hypershift/cmd/nodepool/core"
	"github.com/openshift/hypershift/cmd/util"
)

type AWSPlatformOptions struct {
	AWSCredentialsFile string
	AdditionalTags     []string
	IAMJSON            string
	IssuerURL          string
	PrivateZoneID      string
	PublicZoneID       string
	Region             string
	EndpointAccess     string
	InfrastructureJSON string
	NodePoolOptions    *nodepoolaws.AWSPlatformCreateOptions
}

func NewCreateCommand(opts *core.CreateOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "aws",
		Short:        "Creates basic functional HostedCluster resources on AWS",
		SilenceUsage: true,
	}

	platformOpts := &AWSPlatformOptions{
		AWSCredentialsFile: "",
		Region:             "us-east-1",
		EndpointAccess:     string(hyperv1.Public),
		InfrastructureJSON: "",
	}

	cmd.Flags().StringVar(&platformOpts.AWSCredentialsFile, "aws-creds", platformOpts.AWSCredentialsFile, "Path to an AWS credentials file (required)")
	cmd.Flags().StringVar(&platformOpts.IAMJSON, "iam-json", platformOpts.IAMJSON, "Path to file containing IAM information for the cluster. If not specified, IAM will be created")
	cmd.Flags().StringVar(&platformOpts.Region, "region", platformOpts.Region, "Region to use for AWS infrastructure.")
	cmd.Flags().StringSliceVar(&platformOpts.AdditionalTags, "additional-tags", platformOpts.AdditionalTags, "Additional tags to set on AWS resources")
	cmd.Flags().StringVar(&platformOpts.EndpointAccess, "endpoint-access", platformOpts.EndpointAccess, "Access for control plane endpoints (Public, PublicAndPrivate, Private)")
	cmd.Flags().StringVar(&platformOpts.InfrastructureJSON, "infra-json", platformOpts.InfrastructureJSON, "Path to file containing infrastructure information for the cluster. If not specified, infrastructure will be created")

	cmd.MarkFlagRequired("aws-creds")

	platformOpts.NodePoolOptions = nodepoolaws.NewAWSPlatformCreateOptions(cmd)
	cmd.RunE = opts.CreateExecFunc(platformOpts)

	return cmd
}

func (o *AWSPlatformOptions) ApplyPlatformSpecifics(ctx context.Context, exampleOptions *apifixtures.ExampleOptions, name, infraID, baseDomain string) (err error) {
	client := util.GetClientOrDie()

	// Load or create infrastructure for the cluster
	var infra *awsinfra.CreateInfraOutput
	if len(o.InfrastructureJSON) > 0 {
		rawInfra, err := ioutil.ReadFile(o.InfrastructureJSON)
		if err != nil {
			return fmt.Errorf("failed to read infra json file: %w", err)
		}
		infra = &awsinfra.CreateInfraOutput{}
		if err = json.Unmarshal(rawInfra, infra); err != nil {
			return fmt.Errorf("failed to load infra json: %w", err)
		}
	}
	if baseDomain == "" {
		if infra != nil {
			baseDomain = infra.BaseDomain
		} else {
			return fmt.Errorf("base-domain flag is required if infra-json is not provided")
		}
	}
	if infra == nil {
		if len(infraID) == 0 {
			infraID = fmt.Sprintf("%s-%s", name, utilrand.String(5))
		}
		opt := awsinfra.CreateInfraOptions{
			Region:             o.Region,
			InfraID:            infraID,
			AWSCredentialsFile: o.AWSCredentialsFile,
			Name:               name,
			BaseDomain:         baseDomain,
			AdditionalTags:     o.AdditionalTags,
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

	exampleOptions.AWS = &apifixtures.ExampleAWSOptions{
		Region:                      infra.Region,
		Zone:                        infra.Zone,
		VPCID:                       infra.VPCID,
		SubnetID:                    infra.PrivateSubnetID,
		Roles:                       iamInfo.Roles,
		KubeCloudControllerRoleARN:  iamInfo.KubeCloudControllerRoleARN,
		NodePoolManagementRoleARN:   iamInfo.NodePoolManagementRoleARN,
		ControlPlaneOperatorRoleARN: iamInfo.ControlPlaneOperatorRoleARN,
		ResourceTags:                tags,
		EndpointAccess:              o.EndpointAccess,
	}

	// Update NodePool with Iam and Infra Output
	o.NodePoolOptions.InfraOutput = infra
	o.NodePoolOptions.IamOutput = iamInfo
	return nil
}

func (o *AWSPlatformOptions) NodePoolPlatformOptions() nodepoolcore.PlatformOptions {
	return o.NodePoolOptions
}

func (o *AWSPlatformOptions) Validate() error {
	return o.NodePoolOptions.Validate()
}
