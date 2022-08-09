package aws

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"
	"github.com/aws/aws-sdk-go/service/route53"
	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	"github.com/openshift/hypershift/cmd/log"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
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
	EnableProxy        bool
	SSHKeyFile         string

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
	MachineCIDR     string                   `json:"machineCIDR"`
	VPCID           string                   `json:"vpcID"`
	Zones           []*CreateInfraOutputZone `json:"zones"`
	SecurityGroupID string                   `json:"securityGroupID"`
	Name            string                   `json:"Name"`
	BaseDomain      string                   `json:"baseDomain"`
	PublicZoneID    string                   `json:"publicZoneID"`
	PrivateZoneID   string                   `json:"privateZoneID"`
	LocalZoneID     string                   `json:"localZoneID"`
	ProxyAddr       string                   `json:"proxyAddr"`
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
	cmd.Flags().BoolVar(&opts.EnableProxy, "enable-proxy", opts.EnableProxy, "If a proxy should be set up, rather than allowing direct internet access from the nodes")

	cmd.MarkFlagRequired("infra-id")
	cmd.MarkFlagRequired("aws-creds")
	cmd.MarkFlagRequired("base-domain")

	l := log.Log
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := opts.Run(cmd.Context(), l); err != nil {
			l.Error(err, "Failed to create infrastructure")
			return err
		}
		l.Info("Successfully created infrastructure")
		return nil
	}

	return cmd
}

func (o *CreateInfraOptions) Run(ctx context.Context, l logr.Logger) error {
	result, err := o.CreateInfra(ctx, l)
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

func (o *CreateInfraOptions) CreateInfra(ctx context.Context, l logr.Logger) (*CreateInfraOutput, error) {
	l.Info("Creating infrastructure", "id", o.InfraID)

	awsSession := awsutil.NewSession("cli-create-infra", o.AWSCredentialsFile, o.AWSKey, o.AWSSecretKey, o.Region)
	ec2Client := ec2.New(awsSession, awsutil.NewConfig())
	route53Client := route53.New(awsSession, awsutil.NewAWSRoute53Config())

	var err error
	if err = o.parseAdditionalTags(); err != nil {
		return nil, err
	}
	result := &CreateInfraOutput{
		InfraID:     o.InfraID,
		MachineCIDR: DefaultCIDRBlock,
		Region:      o.Region,
		Name:        o.Name,
		BaseDomain:  o.BaseDomain,
	}
	if len(o.Zones) == 0 {
		zone, err := o.firstZone(l, ec2Client)
		if err != nil {
			return nil, err
		}
		o.Zones = append(o.Zones, zone)
	}

	// VPC resources
	result.VPCID, err = o.createVPC(l, ec2Client)
	if err != nil {
		return nil, err
	}
	if err = o.CreateDHCPOptions(l, ec2Client, result.VPCID); err != nil {
		return nil, err
	}
	igwID, err := o.CreateInternetGateway(l, ec2Client, result.VPCID)
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
		privateSubnetID, err := o.CreatePrivateSubnet(l, ec2Client, result.VPCID, zone, privateNetwork.String())
		if err != nil {
			return nil, err
		}
		publicSubnetID, err := o.CreatePublicSubnet(l, ec2Client, result.VPCID, zone, publicNetwork.String())
		if err != nil {
			return nil, err
		}
		var natGatewayID string
		publicSubnetIDs = append(publicSubnetIDs, publicSubnetID)
		if !o.EnableProxy {
			natGatewayID, err = o.CreateNATGateway(l, ec2Client, publicSubnetID, zone)
			if err != nil {
				return nil, err
			}
		}
		privateRouteTable, err := o.CreatePrivateRouteTable(l, ec2Client, result.VPCID, natGatewayID, privateSubnetID, zone)
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
	publicRouteTable, err := o.CreatePublicRouteTable(l, ec2Client, result.VPCID, igwID, publicSubnetIDs)
	if err != nil {
		return nil, err
	}
	endpointRouteTableIds = append(endpointRouteTableIds, aws.String(publicRouteTable))
	err = o.CreateVPCS3Endpoint(l, ec2Client, result.VPCID, endpointRouteTableIds)
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

	if o.EnableProxy {
		var sshKeyFile []byte
		if o.SSHKeyFile != "" {
			sshKeyFile, err = ioutil.ReadFile(o.SSHKeyFile)
			if err != nil {
				return nil, fmt.Errorf("failed to read ssh-key-file from %s: %w", o.SSHKeyFile, err)
			}
		}
		result.ProxyAddr, err = o.createProxyHost(ctx, l, ec2Client, result.Zones[0].SubnetID, result.VPCID, string(sshKeyFile))
		if err != nil {
			return nil, fmt.Errorf("failed to create proxy host: %w", err)
		}

	}
	return result, nil
}

func (o *CreateInfraOptions) createProxyHost(ctx context.Context, l logr.Logger, client ec2iface.EC2API, subnetID, vpcID string, sshKeys string) (string, error) {
	const securityGroupName = "proxy-sg"
	sgCreateResult, err := client.CreateSecurityGroupWithContext(ctx, &ec2.CreateSecurityGroupInput{
		GroupName:         aws.String(securityGroupName),
		Description:       aws.String("proxy security group"),
		VpcId:             aws.String(vpcID),
		TagSpecifications: o.ec2TagSpecifications("security-group", securityGroupName),
	})
	if err != nil {
		return "", fmt.Errorf("failed to create bastion security group: %w", err)
	}

	var sgResult *ec2.DescribeSecurityGroupsOutput
	err = retry.OnError(ec2Backoff(), func(error) bool { return true }, func() error {
		var err error
		sgResult, err = client.DescribeSecurityGroupsWithContext(ctx, &ec2.DescribeSecurityGroupsInput{
			GroupIds: []*string{sgCreateResult.GroupId},
		})
		if err != nil || len(sgResult.SecurityGroups) == 0 {
			return errors.New("not found yet")
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("cannot find security group that was just created (%s)", aws.StringValue(sgCreateResult.GroupId))
	}
	sg := sgResult.SecurityGroups[0]
	l.Info("Created security group", "name", securityGroupName, "id", aws.StringValue(sg.GroupId))

	permissions := []*ec2.IpPermission{
		{
			IpProtocol: aws.String("tcp"),
			IpRanges: []*ec2.IpRange{{
				CidrIp: aws.String("0.0.0.0/0"),
			}},
			FromPort: aws.Int64(22),
			ToPort:   aws.Int64(22),
		},
		{
			IpProtocol: aws.String("-1"),
			IpRanges: []*ec2.IpRange{{
				CidrIp: aws.String("10.0.0.0/8"),
			}},
			FromPort: aws.Int64(-1),
			ToPort:   aws.Int64(-1),
		},
	}
	_, err = client.AuthorizeSecurityGroupIngressWithContext(ctx, &ec2.AuthorizeSecurityGroupIngressInput{
		GroupId:       sg.GroupId,
		IpPermissions: permissions,
	})
	if err != nil {
		return "", fmt.Errorf("failed to authorize security group: %w", err)
	}
	l.Info("Authorized security group for proxy")

	result, err := client.RunInstancesWithContext(ctx, &ec2.RunInstancesInput{
		ImageId:      aws.String("resolve:ssm:/aws/service/ami-amazon-linux-latest/amzn2-ami-hvm-x86_64-gp2"),
		MaxCount:     aws.Int64(1),
		MinCount:     aws.Int64(1),
		InstanceType: aws.String("t2.micro"),
		UserData:     aws.String(base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf(proxyConfigurationScript, sshKeys)))),
		NetworkInterfaces: []*ec2.InstanceNetworkInterfaceSpecification{
			{
				DeviceIndex:              aws.Int64(0),
				AssociatePublicIpAddress: aws.Bool(true),
				SubnetId:                 aws.String(subnetID),
				Groups:                   []*string{sg.GroupId},
			},
		},
		TagSpecifications: o.ec2TagSpecifications("instance", o.Name+"-"+o.InfraID+"-http-proxy"),
	})
	if err != nil {
		return "", fmt.Errorf("failed to launch proxy host: %w", err)
	}
	l.Info("Created proxy host")

	return fmt.Sprintf("http://%s:3128", *result.Instances[0].PrivateIpAddress), nil
}

func ec2Backoff() wait.Backoff {
	return wait.Backoff{
		Steps:    10,
		Duration: 3 * time.Second,
		Factor:   1.0,
		Jitter:   0.1,
	}
}

const proxyConfigurationScript = `#!/bin/bash
yum install -y squid
# By default, squid only allows connect on port 443
sed -E 's/(^http_access deny CONNECT.*)/#\1/' -i /etc/squid/squid.conf
systemctl enable --now squid
mkdir -p /home/ec2-user/.ssh
chmod 0700 /home/ec2-user/.ssh
echo -e '%s' >/home/ec2-user/.ssh/authorized_keys
chmod 0600 /home/ec2-user/.ssh/authorized_keys
chown -R ec2-user:ec2-user /home/ec2-user/.ssh
`
