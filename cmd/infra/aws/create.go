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
	"github.com/aws/aws-sdk-go/service/iam"
	"github.com/aws/aws-sdk-go/service/iam/iamiface"
	"github.com/spf13/cobra"
)

type CreateInfraOptions struct {
	Region             string
	InfraID            string
	AWSCredentialsFile string
	OutputFile         string
}

type CreateInfraOutput struct {
	Region                string `json:"region"`
	Zone                  string `json:"zone"`
	InfraID               string `json:"infraID"`
	ComputeCIDR           string `json:"computeCIDR"`
	VPCID                 string `json:"vpcID"`
	PrivateSubnetID       string `json:"privateSubnetID"`
	PublicSubnetID        string `json:"publicSubnetID"`
	SecurityGroupID       string `json:"securityGroupID"`
	WorkerInstanceProfile string `json:"workerInstanceProfile"`
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
	result := &CreateInfraOutput{
		InfraID:               o.InfraID,
		ComputeCIDR:           DefaultCIDRBlock,
		Region:                o.Region,
		WorkerInstanceProfile: fmt.Sprintf("%s-worker-profile", o.InfraID),
	}
	client, err := o.AWSClient()
	if err != nil {
		return nil, err
	}
	result.Zone, err = o.firstZone(client.EC2)
	if err != nil {
		return nil, err
	}
	result.VPCID, err = o.createVPC(client.EC2)
	if err != nil {
		return nil, err
	}
	if err = o.CreateDHCPOptions(client.EC2, result.VPCID); err != nil {
		return nil, err
	}
	result.PrivateSubnetID, err = o.CreatePrivateSubnet(client.EC2, result.VPCID, result.Zone)
	if err != nil {
		return nil, err
	}
	result.PublicSubnetID, err = o.CreatePublicSubnet(client.EC2, result.VPCID, result.Zone)
	if err != nil {
		return nil, err
	}
	igwID, err := o.CreateInternetGateway(client.EC2, result.VPCID)
	if err != nil {
		return nil, err
	}
	natGatewayID, err := o.CreateNATGateway(client.EC2, result.PublicSubnetID, result.Zone)
	if err != nil {
		return nil, err
	}
	result.SecurityGroupID, err = o.CreateWorkerSecurityGroup(client.EC2, result.VPCID)
	if err != nil {
		return nil, err
	}
	privateRouteTable, err := o.CreatePrivateRouteTable(client.EC2, result.VPCID, natGatewayID, result.PrivateSubnetID, result.Zone)
	if err != nil {
		return nil, err
	}
	publicRouteTable, err := o.CreatePublicRouteTable(client.EC2, result.VPCID, igwID, result.PublicSubnetID, result.Zone)
	if err != nil {
		return nil, err
	}
	err = o.CreateVPCS3Endpoint(client.EC2, result.VPCID, privateRouteTable, publicRouteTable)
	if err != nil {
		return nil, err
	}
	err = o.CreateWorkerInstanceProfile(client.IAM, result.WorkerInstanceProfile)
	if err != nil {
		return nil, err
	}
	return result, nil
}

type AWSClient struct {
	EC2 ec2iface.EC2API
	IAM iamiface.IAMAPI
}

func (o *CreateInfraOptions) AWSClient() (*AWSClient, error) {
	awsConfig := &aws.Config{
		Region: aws.String(o.Region),
	}
	awsConfig.Credentials = credentials.NewSharedCredentials(o.AWSCredentialsFile, "default")
	s, err := session.NewSession(awsConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create client session: %w", err)
	}
	return &AWSClient{
		EC2: ec2.New(s),
		IAM: iam.New(s),
	}, nil
}
