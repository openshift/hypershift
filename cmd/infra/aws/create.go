package aws

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"
	"github.com/spf13/cobra"
)

type CreateInfraOptions struct {
	Region             string
	InfraID            string
	AWSCredentialsFile string
	OutputFile         string
	AdditionalTags     []string

	additionalEC2Tags []*ec2.Tag
}

type CreateInfraOutput struct {
	Region          string `json:"region"`
	Zone            string `json:"zone"`
	InfraID         string `json:"infraID"`
	ComputeCIDR     string `json:"computeCIDR"`
	VPCID           string `json:"vpcID"`
	PrivateSubnetID string `json:"privateSubnetID"`
	PublicSubnetID  string `json:"publicSubnetID"`
	SecurityGroupID string `json:"securityGroupID"`
}

const (
	DefaultCIDRBlock  = "10.0.0.0/16"
	PrivateSubnetCIDR = "10.0.128.0/20"
	PublicSubnetCIDR  = "10.0.0.0/20"

	clusterTagValue = "owned"
)

func NewCreateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "aws",
		Short: "Creates AWS infrastructure resources for a cluster",
	}

	opts := CreateInfraOptions{
		Region: "us-east-1",
	}

	cmd.Flags().StringVar(&opts.InfraID, "infra-id", opts.InfraID, "Cluster ID with which to tag AWS resources (required)")
	cmd.Flags().StringVar(&opts.AWSCredentialsFile, "aws-creds", opts.AWSCredentialsFile, "Path to an AWS credentials file (required)")
	cmd.Flags().StringVar(&opts.OutputFile, "output-file", opts.OutputFile, "Path to file that will contain output information from infra resources (optional)")
	cmd.Flags().StringVar(&opts.Region, "region", opts.Region, "Region where cluster infra should be created")
	cmd.Flags().StringSliceVar(&opts.AdditionalTags, "additional-tags", opts.AdditionalTags, "Additional tags to set on AWS resources")

	cmd.MarkFlagRequired("infra-id")
	cmd.MarkFlagRequired("aws-creds")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return opts.Run()
	}

	return cmd
}

func (o *CreateInfraOptions) Run() error {
	result, err := o.CreateInfra()
	if err != nil {
		return err
	}
	out := os.Stdout
	if len(o.OutputFile) > 0 {
		var err error
		out, err = os.Create(o.OutputFile)
		if err != nil {
			return fmt.Errorf("cannot create output file: %w", err)
		}
		defer out.Close()
	}
	outputBytes, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize result: %w", err)
	}
	_, err = out.Write(outputBytes)
	if err != nil {
		return fmt.Errorf("failed to write result: %w", err)
	}
	return nil
}

func (o *CreateInfraOptions) CreateInfra() (*CreateInfraOutput, error) {
	var err error
	if err = o.parseAdditionalTags(); err != nil {
		return nil, err
	}
	result := &CreateInfraOutput{
		InfraID:     o.InfraID,
		ComputeCIDR: DefaultCIDRBlock,
		Region:      o.Region,
	}
	client, err := AWSClient(o.AWSCredentialsFile, o.Region)
	if err != nil {
		return nil, err
	}
	result.Zone, err = o.firstZone(client)
	if err != nil {
		return nil, err
	}
	result.VPCID, err = o.createVPC(client)
	if err != nil {
		return nil, err
	}
	if err = o.CreateDHCPOptions(client, result.VPCID); err != nil {
		return nil, err
	}
	result.PrivateSubnetID, err = o.CreatePrivateSubnet(client, result.VPCID, result.Zone)
	if err != nil {
		return nil, err
	}
	result.PublicSubnetID, err = o.CreatePublicSubnet(client, result.VPCID, result.Zone)
	if err != nil {
		return nil, err
	}
	igwID, err := o.CreateInternetGateway(client, result.VPCID)
	if err != nil {
		return nil, err
	}
	natGatewayID, err := o.CreateNATGateway(client, result.PublicSubnetID, result.Zone)
	if err != nil {
		return nil, err
	}
	result.SecurityGroupID, err = o.CreateWorkerSecurityGroup(client, result.VPCID)
	if err != nil {
		return nil, err
	}
	privateRouteTable, err := o.CreatePrivateRouteTable(client, result.VPCID, natGatewayID, result.PrivateSubnetID, result.Zone)
	if err != nil {
		return nil, err
	}
	publicRouteTable, err := o.CreatePublicRouteTable(client, result.VPCID, igwID, result.PublicSubnetID, result.Zone)
	if err != nil {
		return nil, err
	}
	err = o.CreateVPCS3Endpoint(client, result.VPCID, privateRouteTable, publicRouteTable)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func AWSClient(creds, region string) (ec2iface.EC2API, error) {
	awsConfig := &aws.Config{
		Region: aws.String(region),
	}
	awsConfig.Credentials = credentials.NewSharedCredentials(creds, "default")
	s, err := session.NewSession(awsConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create client session: %w", err)
	}
	return ec2.New(s), nil
}
