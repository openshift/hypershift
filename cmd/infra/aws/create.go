package aws

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	"github.com/openshift/hypershift/cmd/log"
	"github.com/openshift/hypershift/cmd/util"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"
	"github.com/aws/aws-sdk-go/service/ram"
	"github.com/aws/aws-sdk-go/service/route53"
	"github.com/aws/aws-sdk-go/service/sts"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
)

type CreateInfraOptions struct {
	AWSCredentialsOpts          awsutil.AWSCredentialsOptions
	Region                      string
	InfraID                     string
	Name                        string
	BaseDomain                  string
	BaseDomainPrefix            string
	Zones                       []string
	OutputFile                  string
	AdditionalTags              []string
	EnableProxy                 bool
	EnableSecureProxy           bool
	ProxyVPCEndpointServiceName string
	SingleNATGateway            bool
	VPCCIDR                     string

	CredentialsSecretData *util.CredentialsSecretData

	VPCOwnerCredentialOpts       awsutil.AWSCredentialsOptions
	PrivateZonesInClusterAccount bool

	PublicOnly bool

	additionalEC2Tags []*ec2.Tag
}

type CreateInfraOutputZone struct {
	Name     string `json:"name"`
	SubnetID string `json:"subnetID"`
}

type CreateInfraOutput struct {
	Region             string                   `json:"region"`
	Zone               string                   `json:"zone"`
	InfraID            string                   `json:"infraID"`
	MachineCIDR        string                   `json:"machineCIDR"`
	VPCID              string                   `json:"vpcID"`
	Zones              []*CreateInfraOutputZone `json:"zones"`
	Name               string                   `json:"Name"`
	BaseDomain         string                   `json:"baseDomain"`
	BaseDomainPrefix   string                   `json:"baseDomainPrefix"`
	PublicZoneID       string                   `json:"publicZoneID"`
	PrivateZoneID      string                   `json:"privateZoneID"`
	LocalZoneID        string                   `json:"localZoneID"`
	ProxyAddr          string                   `json:"proxyAddr"`
	SecureProxyAddr    string                   `json:"secureProxyAddr"`
	ProxyPrivateSSHKey string                   `json:"proxyPrivateSSHKey"`
	PublicOnly         bool                     `json:"publicOnly"`
	ProxyCA            string                   `json:"proxyCA"`

	// Fields related to shared VPCs
	VPCCreatorAccountID string `json:"vpcCreatorAccountID"`
	ClusterAccountID    string `json:"clusterAccountID"`
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
	cmd.Flags().StringVar(&opts.OutputFile, "output-file", opts.OutputFile, "Path to file that will contain output information from infra resources (optional)")
	cmd.Flags().StringVar(&opts.Region, "region", opts.Region, "Region where cluster infra should be created")
	cmd.Flags().StringSliceVar(&opts.AdditionalTags, "additional-tags", opts.AdditionalTags, "Additional tags to set on AWS resources")
	cmd.Flags().StringVar(&opts.Name, "name", opts.Name, "A name for the cluster")
	cmd.Flags().StringVar(&opts.BaseDomain, "base-domain", opts.BaseDomain, "The ingress base domain for the cluster")
	cmd.Flags().StringVar(&opts.BaseDomainPrefix, "base-domain-prefix", opts.BaseDomainPrefix, "The ingress base domain prefix for the cluster, defaults to cluster name. Use 'none' for an empty prefix")
	cmd.Flags().StringSliceVar(&opts.Zones, "zones", opts.Zones, "The availability zones in which NodePool can be created")
	cmd.Flags().BoolVar(&opts.EnableProxy, "enable-proxy", opts.EnableProxy, "If true, a proxy should be set up, rather than allowing direct internet access from the nodes")
	cmd.Flags().BoolVar(&opts.EnableSecureProxy, "enable-secure-proxy", opts.EnableSecureProxy, "If true, a secure proxy should be set up, rather than allowing direct internet access from the nodes")
	cmd.Flags().StringVar(&opts.ProxyVPCEndpointServiceName, "proxy-vpc-endpoint-service-name", opts.ProxyVPCEndpointServiceName, "The name of a VPC Endpoint Service offering a proxy service to use for the cluster")
	cmd.Flags().BoolVar(&opts.SingleNATGateway, "single-nat-gateway", opts.SingleNATGateway, "If enabled, only a single NAT gateway is created, even if multiple zones are specified")
	cmd.Flags().StringVar(&opts.VPCCIDR, "vpc-cidr", opts.VPCCIDR, "The CIDR to use for the cluster VPC")
	cmd.Flags().BoolVar(&opts.PrivateZonesInClusterAccount, "private-zones-in-cluster-account", opts.PrivateZonesInClusterAccount, "In shared VPC infrastructure, create private hosted zones in cluster account")
	cmd.Flags().BoolVar(&opts.PublicOnly, "public-only", opts.PublicOnly, "If true, no private subnets or NAT gateway are created")

	_ = cmd.MarkFlagRequired("infra-id")
	_ = cmd.MarkFlagRequired("base-domain")

	opts.AWSCredentialsOpts.BindFlags(cmd.Flags())
	opts.VPCOwnerCredentialOpts.BindVPCOwnerFlags(cmd.Flags())

	l := log.Log
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		err := opts.AWSCredentialsOpts.Validate()
		if err != nil {
			return err
		}
		if err = opts.Validate(); err != nil {
			return err
		}
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
	return o.Output(result)
}

func (o *CreateInfraOptions) Validate() error {
	if o.EnableProxy && o.EnableSecureProxy {
		return fmt.Errorf("specify either --enable-proxy or --enable-secure-proxy, but not both")
	}
	return nil
}

func (o *CreateInfraOptions) Output(result *CreateInfraOutput) error {
	// Write out stateful information
	out := os.Stdout
	if len(o.OutputFile) > 0 {
		var err error
		out, err = os.Create(o.OutputFile)
		if err != nil {
			return fmt.Errorf("cannot create output file: %w", err)
		}
		defer func(out *os.File) {
			_ = out.Close()
		}(out)
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

	if o.VPCCIDR == "" {
		o.VPCCIDR = DefaultCIDRBlock
	}

	if err := awsutil.ValidateVPCCIDR(o.VPCCIDR); err != nil {
		return nil, err
	}

	awsSession, err := o.AWSCredentialsOpts.GetSession("cli-create-infra", o.CredentialsSecretData, o.Region)
	if err != nil {
		return nil, err
	}
	var vpcOwnerAWSSession *session.Session
	if o.VPCOwnerCredentialOpts.AWSCredentialsFile != "" {
		vpcOwnerAWSSession, err = o.VPCOwnerCredentialOpts.GetSession("cli-create-infra", nil, o.Region)
		if err != nil {
			return nil, err
		}
	}

	var clusterCreatorEC2Client, ec2Client *ec2.EC2
	var vpcOwnerRoute53Client, route53Client *route53.Route53
	clusterCreatorEC2Client = ec2.New(awsSession, awsutil.NewConfig())
	if vpcOwnerAWSSession != nil {
		ec2Client = ec2.New(vpcOwnerAWSSession, awsutil.NewConfig())
	} else {
		ec2Client = clusterCreatorEC2Client
	}
	route53Client = route53.New(awsSession, awsutil.NewAWSRoute53Config())
	if vpcOwnerAWSSession != nil {
		vpcOwnerRoute53Client = route53.New(vpcOwnerAWSSession, awsutil.NewAWSRoute53Config())
	} else {
		vpcOwnerRoute53Client = route53Client
	}

	if err := o.parseAdditionalTags(); err != nil {
		return nil, err
	}

	result := &CreateInfraOutput{
		InfraID:          o.InfraID,
		MachineCIDR:      o.VPCCIDR,
		Region:           o.Region,
		Name:             o.Name,
		BaseDomain:       o.BaseDomain,
		BaseDomainPrefix: o.BaseDomainPrefix,
		PublicOnly:       o.PublicOnly,
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

	// Per zone resources
	_, cidrNetwork, err := net.ParseCIDR(o.VPCCIDR)
	if err != nil {
		return nil, err
	}
	publicNetwork := copyIPNet(cidrNetwork)
	publicNetwork.Mask = net.CIDRMask(20, 32)

	privateNetwork := copyIPNet(cidrNetwork)
	privateNetwork.Mask = net.CIDRMask(20, 32)
	privateNetwork.IP[2] += 128

	var endpointRouteTableIds []*string
	var publicSubnetIDs []string
	var natGatewayID string
	for _, zone := range o.Zones {
		var (
			privateSubnetID string
			err             error
		)
		if !o.PublicOnly {
			privateSubnetID, err = o.CreatePrivateSubnet(l, ec2Client, result.VPCID, zone, privateNetwork.String())
			if err != nil {
				return nil, err
			}
		}
		publicSubnetID, err := o.CreatePublicSubnet(l, ec2Client, result.VPCID, zone, publicNetwork.String())
		if err != nil {
			return nil, err
		}
		publicSubnetIDs = append(publicSubnetIDs, publicSubnetID)
		if !o.PublicOnly && !o.EnableProxy && !o.EnableSecureProxy && ((natGatewayID == "" && o.SingleNATGateway) || !o.SingleNATGateway) {
			natGatewayID, err = o.CreateNATGateway(l, ec2Client, publicSubnetID, zone)
			if err != nil {
				return nil, err
			}
		}
		if !o.PublicOnly {
			privateRouteTable, err := o.CreatePrivateRouteTable(l, ec2Client, result.VPCID, natGatewayID, privateSubnetID, zone)
			if err != nil {
				return nil, err
			}
			endpointRouteTableIds = append(endpointRouteTableIds, aws.String(privateRouteTable))
		}
		zoneSubnetID := privateSubnetID
		if o.PublicOnly {
			zoneSubnetID = publicSubnetID
		}
		result.Zones = append(result.Zones, &CreateInfraOutputZone{
			Name:     zone,
			SubnetID: zoneSubnetID,
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
	result.PublicZoneID, err = o.LookupPublicZone(ctx, l, route53Client)
	if err != nil {
		return nil, err
	}

	if vpcOwnerAWSSession != nil {
		if err := o.shareSubnets(ctx, l, vpcOwnerAWSSession, awsSession, publicSubnetIDs, result); err != nil {
			return nil, err
		}
	}

	privateZoneClient := vpcOwnerRoute53Client
	var initialVPC string
	if o.PrivateZonesInClusterAccount {
		privateZoneClient = route53Client

		// Create a dummy vpc that we can use to create the private hosted zones
		if initialVPC, err = o.createVPC(l, clusterCreatorEC2Client); err != nil {
			return nil, err
		}
	}

	result.PrivateZoneID, err = o.CreatePrivateZone(ctx, l, privateZoneClient, ZoneName(o.Name, o.BaseDomainPrefix, o.BaseDomain), result.VPCID, o.PrivateZonesInClusterAccount, vpcOwnerRoute53Client, initialVPC)
	if err != nil {
		return nil, err
	}
	result.LocalZoneID, err = o.CreatePrivateZone(ctx, l, privateZoneClient, fmt.Sprintf("%s.%s", o.Name, hypershiftLocalZoneName), result.VPCID, o.PrivateZonesInClusterAccount, vpcOwnerRoute53Client, initialVPC)
	if err != nil {
		return nil, err
	}

	if initialVPC != "" {
		if err := o.deleteVPC(l, clusterCreatorEC2Client, initialVPC); err != nil {
			return nil, err
		}
	}

	if o.EnableProxy || o.EnableSecureProxy {
		sgGroupID, err := o.createProxySecurityGroup(ctx, l, ec2Client, result.VPCID)
		if err != nil {
			return nil, fmt.Errorf("failed to create security group for proxy: %w", err)
		}

		if o.ProxyVPCEndpointServiceName != "" {
			result.ProxyAddr, err = o.createProxyVPCEndpoint(ctx, l, ec2Client, result.VPCID, result.Zones[0].SubnetID, sgGroupID)
			if err != nil {
				return nil, err
			}
		} else {
			proxyResult, err := o.createProxyHost(ctx, l, ec2Client, result.Zones[0].SubnetID, sgGroupID, o.EnableSecureProxy)
			if err != nil {
				return nil, fmt.Errorf("failed to create proxy host: %w", err)
			}
			result.ProxyAddr = proxyResult.HTTPProxyURL
			result.SecureProxyAddr = proxyResult.HTTPSProxyURL
			result.ProxyCA = proxyResult.CA
			result.ProxyPrivateSSHKey = proxyResult.PrivateKey
		}
	}
	return result, nil
}

func (o *CreateInfraOptions) createProxySecurityGroup(ctx context.Context, l logr.Logger, client ec2iface.EC2API, vpcID string) (*string, error) {
	securityGroupName := o.InfraID + "-proxy-sg"
	sgCreateResult, err := client.CreateSecurityGroupWithContext(ctx, &ec2.CreateSecurityGroupInput{
		GroupName:         aws.String(securityGroupName),
		Description:       aws.String("proxy security group"),
		VpcId:             aws.String(vpcID),
		TagSpecifications: o.ec2TagSpecifications("security-group", securityGroupName),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create bastion security group: %w", err)
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
		return nil, fmt.Errorf("cannot find security group that was just created (%s)", aws.StringValue(sgCreateResult.GroupId))
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
		return nil, fmt.Errorf("failed to authorize security group: %w", err)
	}
	l.Info("Authorized security group for proxy")

	return sg.GroupId, nil
}

func (o *CreateInfraOptions) createProxyVPCEndpoint(ctx context.Context, l logr.Logger, client ec2iface.EC2API, vpcID string, subnetID string, sgGroupID *string) (string, error) {
	output, err := client.CreateVpcEndpointWithContext(ctx, &ec2.CreateVpcEndpointInput{
		ServiceName: aws.String(o.ProxyVPCEndpointServiceName),
		VpcId:       aws.String(vpcID),
		SubnetIds:   []*string{aws.String(subnetID)},
		SecurityGroupIds: []*string{
			sgGroupID,
		},
		VpcEndpointType:   aws.String("Interface"),
		PrivateDnsEnabled: aws.Bool(false),
		TagSpecifications: o.ec2TagSpecifications("vpc-endpoint", o.InfraID+"-http-proxy"),
	})
	if err != nil {
		return "", fmt.Errorf("failed to create VPC endpoint for proxy: %w", err)
	}

	l.Info("Created VPC endpoint for proxy", "id", aws.StringValue(output.VpcEndpoint.VpcEndpointId))
	return fmt.Sprintf("http://%s:3128", *output.VpcEndpoint.DnsEntries[0].DnsName), nil
}

type proxyInfo struct {
	HTTPProxyURL  string
	HTTPSProxyURL string
	CA            string
	PrivateKey    string
}

func (o *CreateInfraOptions) createProxyHost(ctx context.Context, l logr.Logger, client ec2iface.EC2API, subnetID string, sgGroupID *string, isSecure bool) (*proxyInfo, error) {

	var result proxyInfo

	publicSSHKey, privateSSHKey, err := util.GenerateSSHKeys()
	if err != nil {
		return nil, fmt.Errorf("failed to generate proxy ssh keys: %w", err)
	}

	instanceType := "t2.micro"
	if isSecure {
		instanceType = "t3.medium"
	}

	runResult, err := client.RunInstancesWithContext(ctx, &ec2.RunInstancesInput{
		ImageId:      aws.String("resolve:ssm:/aws/service/ami-amazon-linux-latest/amzn2-ami-hvm-x86_64-gp2"),
		MaxCount:     aws.Int64(1),
		MinCount:     aws.Int64(1),
		InstanceType: aws.String(instanceType),
		UserData:     aws.String(base64.StdEncoding.EncodeToString([]byte(proxyConfigScript(isSecure, string(publicSSHKey))))),
		NetworkInterfaces: []*ec2.InstanceNetworkInterfaceSpecification{
			{
				DeviceIndex:              aws.Int64(0),
				AssociatePublicIpAddress: aws.Bool(true),
				SubnetId:                 aws.String(subnetID),
				Groups:                   []*string{sgGroupID},
			},
		},
		TagSpecifications: o.ec2TagSpecifications("instance", o.InfraID+"-http-proxy"),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to launch proxy host: %w", err)
	}
	l.Info("Created proxy host")

	privateIP := aws.StringValue(runResult.Instances[0].PrivateIpAddress)
	if isSecure {
		if err := client.WaitUntilInstanceRunningWithContext(ctx, &ec2.DescribeInstancesInput{
			InstanceIds: []*string{runResult.Instances[0].InstanceId},
		}); err != nil {
			return nil, fmt.Errorf("failed to wait for proxy host to be in running state: %w", err)
		}

		describeResult, err := client.DescribeInstancesWithContext(ctx, &ec2.DescribeInstancesInput{
			InstanceIds: []*string{runResult.Instances[0].InstanceId},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to describe proxy instance: %w", err)
		}

		publicIP := aws.StringValue(describeResult.Reservations[0].Instances[0].PublicIpAddress)

		backoff := wait.Backoff{
			Steps:    10,
			Duration: 10 * time.Second,
			Factor:   1.0,
			Jitter:   0.1,
		}

		var proxyCA string
		err = retry.OnError(backoff, func(error) bool { return true }, func() error {
			var err error
			proxyCA, err = fetchProxyCA(publicIP, privateIP, privateSSHKey)
			if err != nil {
				l.Info("Waiting to fetch proxy CA", "message", err.Error())
				return err
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("failed to fetch proxy CA trust bundle: %w", err)
		}
		l.Info("Obtained proxy CA trust bundle")
		result.HTTPSProxyURL = fmt.Sprintf("https://%s:3128", privateIP)
		result.CA = proxyCA
	}

	result.HTTPProxyURL = fmt.Sprintf("http://%s:3128", privateIP)
	result.PrivateKey = base64.StdEncoding.EncodeToString(privateSSHKey)

	return &result, nil
}

func (o *CreateInfraOptions) shareSubnets(ctx context.Context, l logr.Logger, vpcOwnerSession, clusterSession *session.Session, publicSubnetIDs []string, output *CreateInfraOutput) error {
	// Obtain account IDs for both accounts
	clusterSTSClient := sts.New(clusterSession, awsutil.NewConfig())
	clusterEC2Client := ec2.New(clusterSession, awsutil.NewConfig())
	vpcOwnerSTSClient := sts.New(vpcOwnerSession, awsutil.NewConfig())
	vpcOwnerEC2Client := ec2.New(vpcOwnerSession, awsutil.NewConfig())

	clusterAccountID, err := clusterSTSClient.GetCallerIdentityWithContext(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return err
	}
	vpcOwnerAccountID, err := vpcOwnerSTSClient.GetCallerIdentityWithContext(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return err
	}
	output.VPCCreatorAccountID = aws.StringValue(vpcOwnerAccountID.Account)
	output.ClusterAccountID = aws.StringValue(clusterAccountID.Account)

	privateSubnetIDsToShare := make([]*string, 0, len(output.Zones))
	publicSubnetIDsToShare := make([]*string, 0, len(publicSubnetIDs))
	allSubnetIDsToShare := make([]*string, 0, len(output.Zones)+len(publicSubnetIDs))
	//if publicOnly=true, the zone.SubnetID is the same as publicSubnetID. Skip this to avoid duplicate publicSubnetIDs will be added to allSubnetIDsToShare
	if !output.PublicOnly {
		for _, zone := range output.Zones {
			privateSubnetIDsToShare = append(privateSubnetIDsToShare, aws.String(zone.SubnetID))
		}
	}
	for _, subnetID := range publicSubnetIDs {
		publicSubnetIDsToShare = append(publicSubnetIDsToShare, aws.String(subnetID))
	}

	allSubnetIDsToShare = append(allSubnetIDsToShare, privateSubnetIDsToShare...)
	allSubnetIDsToShare = append(allSubnetIDsToShare, publicSubnetIDsToShare...)

	subnetsResult, err := vpcOwnerEC2Client.DescribeSubnetsWithContext(ctx, &ec2.DescribeSubnetsInput{
		SubnetIds: allSubnetIDsToShare,
	})
	if err != nil {
		return err
	}
	subnetArns := make([]*string, 0, len(subnetsResult.Subnets))
	for _, subnet := range subnetsResult.Subnets {
		subnetArns = append(subnetArns, subnet.SubnetArn)
	}

	// Share subnets
	l.Info("Sharing VPC subnets with cluster creator account", "subnetids", allSubnetIDsToShare)
	ramClient := ram.New(vpcOwnerSession, awsutil.NewConfig())
	if _, err = ramClient.CreateResourceShareWithContext(ctx, &ram.CreateResourceShareInput{
		Name:         aws.String(fmt.Sprintf("%s-share", o.InfraID)),
		Principals:   []*string{aws.String(output.ClusterAccountID)},
		ResourceArns: subnetArns,
		Tags: []*ram.Tag{
			{
				Key:   aws.String(clusterTag(o.InfraID)),
				Value: aws.String(clusterTagValue),
			},
		},
	}); err != nil {
		return err
	}

	// Wait for subnets to be visible in the cluster creator account
	backoff := wait.Backoff{
		Steps:    10,
		Duration: 30 * time.Second,
		Factor:   1.0,
		Jitter:   0.1,
	}
	var subnetResult *ec2.DescribeSubnetsOutput
	if err = retry.OnError(backoff, func(error) bool { return true }, func() error {
		var err error
		subnetResult, err = clusterEC2Client.DescribeSubnets(&ec2.DescribeSubnetsInput{
			SubnetIds: allSubnetIDsToShare,
		})
		if err != nil || len(subnetResult.Subnets) != len(allSubnetIDsToShare) {
			l.Info("Waiting for subnets to be available in cluster creator account")
			return fmt.Errorf("not ready yet")
		}
		return nil
	}); err != nil {
		return err
	}

	// Tag subnets in cluster creator account
	for _, subnet := range subnetsResult.Subnets {
		l.Info("Tagging subnet", "id", aws.StringValue(subnet.SubnetId))
		if _, err := clusterEC2Client.CreateTagsWithContext(ctx, &ec2.CreateTagsInput{
			Resources: []*string{subnet.SubnetId},
			Tags:      subnet.Tags,
		}); err != nil {
			return err
		}
	}
	return nil
}

func ec2Backoff() wait.Backoff {
	return wait.Backoff{
		Steps:    10,
		Duration: 3 * time.Second,
		Factor:   1.0,
		Jitter:   0.1,
	}
}

func ZoneName(clusterName, prefix, baseDomain string) string {
	if prefix == "none" {
		return baseDomain
	}

	if prefix == "" {
		prefix = clusterName
	}
	return fmt.Sprintf("%s.%s", prefix, baseDomain)
}

func copyIPNet(in *net.IPNet) *net.IPNet {
	result := *in
	resultIP := make(net.IP, len(in.IP))
	copy(resultIP, in.IP)
	result.IP = resultIP
	return &result
}
