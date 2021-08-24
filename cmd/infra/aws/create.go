package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/route53"
	"github.com/spf13/cobra"

	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
)

type CreateInfraOptions struct {
	Region             string
	InfraID            string
	AWSCredentialsFile string
	Name               string
	BaseDomain         string
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
	Name            string `json:"Name"`
	BaseDomain      string `json:"baseDomain"`
	PublicZoneID    string `json:"publicZoneID"`
	PrivateZoneID   string `json:"privateZoneID"`
}

const (
	DefaultCIDRBlock  = "10.0.0.0/16"
	PrivateSubnetCIDR = "10.0.128.0/20"
	PublicSubnetCIDR  = "10.0.0.0/20"

	clusterTagValue = "owned"
)

func NewCreateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "aws",
		Short:        "Creates AWS infrastructure resources for a cluster",
		SilenceUsage: true,
	}

	opts := CreateInfraOptions{
		Region: "us-east-1",
		Name:   "example",
	}

	cmd.Flags().StringVar(&opts.InfraID, "infra-id", opts.InfraID, "Cluster ID with which to tag AWS resources (required)")
	cmd.Flags().StringVar(&opts.AWSCredentialsFile, "aws-creds", opts.AWSCredentialsFile, "Path to an AWS credentials file (required)")
	cmd.Flags().StringVar(&opts.OutputFile, "output-file", opts.OutputFile, "Path to file that will contain output information from infra resources (optional)")
	cmd.Flags().StringVar(&opts.Region, "region", opts.Region, "Region where cluster infra should be created")
	cmd.Flags().StringSliceVar(&opts.AdditionalTags, "additional-tags", opts.AdditionalTags, "Additional tags to set on AWS resources")
	cmd.Flags().StringVar(&opts.Name, "name", opts.Name, "A name for the cluster")
	cmd.Flags().StringVar(&opts.BaseDomain, "base-domain", opts.BaseDomain, "The ingress base domain for the cluster")

	cmd.MarkFlagRequired("infra-id")
	cmd.MarkFlagRequired("aws-creds")
	cmd.MarkFlagRequired("base-domain")

	cmd.Run = func(cmd *cobra.Command, args []string) {
		ctx, cancel := context.WithCancel(context.Background())
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT)
		go func() {
			<-sigs
			cancel()
		}()

		if err := opts.Run(ctx); err != nil {
			log.Error(err, "Failed to create infrastructure")
			os.Exit(1)
		}
		log.Info("Successfully created infrastructure")
	}

	return cmd
}

func (o *CreateInfraOptions) Run(ctx context.Context) error {
	result, err := o.CreateInfra(ctx)
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

func (o *CreateInfraOptions) CreateInfra(ctx context.Context) (*CreateInfraOutput, error) {
	log.Info("Creating infrastructure", "id", o.InfraID)

	awsSession := awsutil.NewSession("cli-create-infra")
	awsConfig := awsutil.NewConfig(o.AWSCredentialsFile, o.Region)
	ec2Client := ec2.New(awsSession, awsConfig)
	route53Client := route53.New(awsSession, awsutil.NewRoute53Config(o.AWSCredentialsFile))

	var err error
	if err = o.parseAdditionalTags(); err != nil {
		return nil, err
	}
	result := &CreateInfraOutput{
		InfraID:     o.InfraID,
		ComputeCIDR: DefaultCIDRBlock,
		Region:      o.Region,
		Name:        o.Name,
		BaseDomain:  o.BaseDomain,
	}
	result.Zone, err = o.firstZone(ec2Client)
	if err != nil {
		return nil, err
	}
	result.VPCID, err = o.createVPC(ec2Client)
	if err != nil {
		return nil, err
	}
	if err = o.CreateDHCPOptions(ec2Client, result.VPCID); err != nil {
		return nil, err
	}
	result.PrivateSubnetID, err = o.CreatePrivateSubnet(ec2Client, result.VPCID, result.Zone)
	if err != nil {
		return nil, err
	}
	result.PublicSubnetID, err = o.CreatePublicSubnet(ec2Client, result.VPCID, result.Zone)
	if err != nil {
		return nil, err
	}
	igwID, err := o.CreateInternetGateway(ec2Client, result.VPCID)
	if err != nil {
		return nil, err
	}
	natGatewayID, err := o.CreateNATGateway(ec2Client, result.PublicSubnetID, result.Zone)
	if err != nil {
		return nil, err
	}
	result.SecurityGroupID, err = o.CreateWorkerSecurityGroup(ec2Client, result.VPCID)
	if err != nil {
		return nil, err
	}
	privateRouteTable, err := o.CreatePrivateRouteTable(ec2Client, result.VPCID, natGatewayID, result.PrivateSubnetID, result.Zone)
	if err != nil {
		return nil, err
	}
	publicRouteTable, err := o.CreatePublicRouteTable(ec2Client, result.VPCID, igwID, result.PublicSubnetID, result.Zone)
	if err != nil {
		return nil, err
	}
	err = o.CreateVPCS3Endpoint(ec2Client, result.VPCID, privateRouteTable, publicRouteTable)
	if err != nil {
		return nil, err
	}
	result.PublicZoneID, err = o.LookupPublicZone(ctx, route53Client)
	if err != nil {
		return nil, err
	}
	result.PrivateZoneID, err = o.CreatePrivateZone(ctx, route53Client, result.VPCID)
	if err != nil {
		return nil, err
	}
	return result, nil
}
