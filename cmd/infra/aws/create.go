package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/route53"
	"github.com/spf13/cobra"

	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	"github.com/openshift/hypershift/cmd/log"
)

type CreateInfraOptions struct {
	Region             string
	InfraID            string
	AWSCredentialsFile string
	AWSKey             string
	AWSSecretKey       string
	Name               string
	BaseDomain         string
	Zones              []string
	OutputFile         string
	AdditionalTags     []string

	additionalEC2Tags []*ec2.Tag
}

type CreateInfraOutputZone struct {
	Name     string `json:"name"`
	SubnetID string `json:"subnetID"`
}

type CreateInfraOutput struct {
	Region          string                   `json:"region"`
	Zone            string                   `json:"zone"`
	InfraID         string                   `json:"infraID"`
	ComputeCIDR     string                   `json:"computeCIDR"`
	VPCID           string                   `json:"vpcID"`
	Zones           []*CreateInfraOutputZone `json:"zones"`
	SecurityGroupID string                   `json:"securityGroupID"`
	Name            string                   `json:"Name"`
	BaseDomain      string                   `json:"baseDomain"`
	PublicZoneID    string                   `json:"publicZoneID"`
	PrivateZoneID   string                   `json:"privateZoneID"`
	LocalZoneID     string                   `json:"localZoneID"`
}

const (
	DefaultCIDRBlock      = "10.0.0.0/16"
	basePrivateSubnetCIDR = "10.0.128.0/20"
	basePublicSubnetCIDR  = "10.0.0.0/20"

	clusterTagValue         = "owned"
	hypershiftLocalZoneName = "hypershift.local"
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
	cmd.Flags().StringSliceVar(&opts.Zones, "zones", opts.Zones, "The availablity zones in which NodePool can be created")

	cmd.MarkFlagRequired("infra-id")
	cmd.MarkFlagRequired("aws-creds")
	cmd.MarkFlagRequired("base-domain")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := opts.Run(cmd.Context()); err != nil {
			log.Log.Error(err, "Failed to create infrastructure")
			return err
		}
		log.Log.Info("Successfully created infrastructure")
		return nil
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
	log.Log.Info("Creating infrastructure", "id", o.InfraID)

	awsSession := awsutil.NewSession("cli-create-infra", o.AWSCredentialsFile, o.AWSKey, o.AWSSecretKey, o.Region)
	ec2Client := ec2.New(awsSession, awsutil.NewConfig())
	route53Client := route53.New(awsSession, awsutil.NewAWSRoute53Config())

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
	if len(o.Zones) == 0 {
		zone, err := o.firstZone(ec2Client)
		if err != nil {
			return nil, err
		}
		o.Zones = append(o.Zones, zone)
	}

	// VPC resources
	result.VPCID, err = o.createVPC(ec2Client)
	if err != nil {
		return nil, err
	}
	if err = o.CreateDHCPOptions(ec2Client, result.VPCID); err != nil {
		return nil, err
	}
	igwID, err := o.CreateInternetGateway(ec2Client, result.VPCID)
	if err != nil {
		return nil, err
	}
	result.SecurityGroupID, err = o.CreateWorkerSecurityGroup(ec2Client, result.VPCID)
	if err != nil {
		return nil, err
	}

	// Per zone resources
	var endpointRouteTableIds []*string
	var publicSubnetIDs []string
	_, privateNetwork, err := net.ParseCIDR(basePrivateSubnetCIDR)
	if err != nil {
		return nil, err
	}
	_, publicNetwork, err := net.ParseCIDR(basePublicSubnetCIDR)
	if err != nil {
		return nil, err
	}
	for _, zone := range o.Zones {
		privateSubnetID, err := o.CreatePrivateSubnet(ec2Client, result.VPCID, zone, privateNetwork.String())
		if err != nil {
			return nil, err
		}
		publicSubnetID, err := o.CreatePublicSubnet(ec2Client, result.VPCID, zone, publicNetwork.String())
		if err != nil {
			return nil, err
		}
		publicSubnetIDs = append(publicSubnetIDs, publicSubnetID)
		natGatewayID, err := o.CreateNATGateway(ec2Client, publicSubnetID, zone)
		if err != nil {
			return nil, err
		}
		privateRouteTable, err := o.CreatePrivateRouteTable(ec2Client, result.VPCID, natGatewayID, privateSubnetID, zone)
		if err != nil {
			return nil, err
		}
		endpointRouteTableIds = append(endpointRouteTableIds, aws.String(privateRouteTable))
		result.Zones = append(result.Zones, &CreateInfraOutputZone{
			Name:     zone,
			SubnetID: privateSubnetID,
		})
		// increment each subnet by /20
		privateNetwork.IP[2] = privateNetwork.IP[2] + 16
		publicNetwork.IP[2] = publicNetwork.IP[2] + 16
	}
	publicRouteTable, err := o.CreatePublicRouteTable(ec2Client, result.VPCID, igwID, publicSubnetIDs)
	if err != nil {
		return nil, err
	}
	endpointRouteTableIds = append(endpointRouteTableIds, aws.String(publicRouteTable))
	err = o.CreateVPCS3Endpoint(ec2Client, result.VPCID, endpointRouteTableIds)
	if err != nil {
		return nil, err
	}
	result.PublicZoneID, err = o.LookupPublicZone(ctx, route53Client)
	if err != nil {
		return nil, err
	}
	result.PrivateZoneID, err = o.CreatePrivateZone(ctx, route53Client, fmt.Sprintf("%s.%s", o.Name, o.BaseDomain), result.VPCID)
	if err != nil {
		return nil, err
	}
	result.LocalZoneID, err = o.CreatePrivateZone(ctx, route53Client, fmt.Sprintf("%s.%s", o.Name, hypershiftLocalZoneName), result.VPCID)
	if err != nil {
		return nil, err
	}

	return result, nil
}
