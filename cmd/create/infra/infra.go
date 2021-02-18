package infra

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

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "infra",
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

const (
	DefaultCIDRBlock  = "10.0.0.0/16"
	PrivateSubnetCIDR = "10.0.128.0/20"
	PublicSubnetCIDR  = "10.0.0.0/20"
)

func (o *CreateInfraOptions) CreateInfra() (*CreateInfraOutput, error) {
	var err error
	result := &CreateInfraOutput{
		InfraID:     o.InfraID,
		ComputeCIDR: DefaultCIDRBlock,
		Region:      o.Region,
	}
	client, err := o.AWSClient()
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

func (o *CreateInfraOptions) firstZone(client ec2iface.EC2API) (string, error) {
	result, err := client.DescribeAvailabilityZones(&ec2.DescribeAvailabilityZonesInput{})
	if err != nil {
		return "", fmt.Errorf("failed to list availability zones: %w", err)
	}
	if len(result.AvailabilityZones) == 0 {
		return "", fmt.Errorf("No availability zones found")

	}
	return aws.StringValue(result.AvailabilityZones[0].ZoneName), nil
}

func (o *CreateInfraOptions) createVPC(client ec2iface.EC2API) (string, error) {
	createResult, err := client.CreateVpc(&ec2.CreateVpcInput{
		CidrBlock: aws.String(DefaultCIDRBlock),
		TagSpecifications: []*ec2.TagSpecification{
			{
				ResourceType: aws.String("vpc"),
				Tags:         ec2Tags(o.InfraID, fmt.Sprintf("%s-vpc", o.InfraID)),
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("failed to create VPC: %w", err)
	}
	vpcID := aws.StringValue(createResult.Vpc.VpcId)
	_, err = client.ModifyVpcAttribute(&ec2.ModifyVpcAttributeInput{
		VpcId:            aws.String(vpcID),
		EnableDnsSupport: &ec2.AttributeBooleanValue{Value: aws.Bool(true)},
	})
	if err != nil {
		return "", fmt.Errorf("failed to modify VPC attributes: %w", err)
	}
	_, err = client.ModifyVpcAttribute(&ec2.ModifyVpcAttributeInput{
		VpcId:              aws.String(vpcID),
		EnableDnsHostnames: &ec2.AttributeBooleanValue{Value: aws.Bool(true)},
	})
	if err != nil {
		return "", fmt.Errorf("failed to modify VPC attributes: %w", err)
	}
	return vpcID, nil
}

func (o *CreateInfraOptions) CreateVPCS3Endpoint(client ec2iface.EC2API, vpcID, privateRouteTableId, publicRouteTableId string) error {
	_, err := client.CreateVpcEndpoint(&ec2.CreateVpcEndpointInput{
		VpcId:       aws.String(vpcID),
		ServiceName: aws.String(fmt.Sprintf("com.amazonaws.%s.s3", o.Region)),
		RouteTableIds: []*string{
			aws.String(privateRouteTableId),
			aws.String(publicRouteTableId),
		},
		TagSpecifications: []*ec2.TagSpecification{
			{
				ResourceType: aws.String("vpc-endpoint"),
				Tags:         ec2Tags(o.InfraID, ""),
			},
		},
	})
	if err != nil {
		return fmt.Errorf("cannot create VPC S3 endpoint: %w", err)
	}
	return nil
}

func (o *CreateInfraOptions) CreateDHCPOptions(client ec2iface.EC2API, vpcID string) error {
	domainName := "ec2.internal"
	if o.Region != "us-east-1" {
		domainName = fmt.Sprintf("%s.compute.internal", o.Region)
	}
	result, err := client.CreateDhcpOptions(&ec2.CreateDhcpOptionsInput{
		DhcpConfigurations: []*ec2.NewDhcpConfiguration{
			{
				Key:    aws.String("domain-name"),
				Values: []*string{aws.String(domainName)},
			},
			{
				Key:    aws.String("domain-name-servers"),
				Values: []*string{aws.String("AmazonProvidedDNS")},
			},
		},
		TagSpecifications: []*ec2.TagSpecification{
			{
				ResourceType: aws.String("dhcp-options"),
				Tags:         ec2Tags(o.InfraID, ""),
			},
		},
	})
	if err != nil {
		return fmt.Errorf("cannot create dhcp-options: %w", err)
	}
	_, err = client.AssociateDhcpOptions(&ec2.AssociateDhcpOptionsInput{
		DhcpOptionsId: result.DhcpOptions.DhcpOptionsId,
		VpcId:         aws.String(vpcID),
	})
	if err != nil {
		return fmt.Errorf("cannot associate dhcp-options to VPC: %w", err)
	}
	return nil
}

func (o *CreateInfraOptions) CreatePrivateSubnet(client ec2iface.EC2API, vpcID string, zone string) (string, error) {
	result, err := client.CreateSubnet(&ec2.CreateSubnetInput{
		AvailabilityZone: aws.String(zone),
		VpcId:            aws.String(vpcID),
		CidrBlock:        aws.String(PrivateSubnetCIDR),
		TagSpecifications: []*ec2.TagSpecification{
			{
				ResourceType: aws.String("subnet"),
				Tags:         ec2Tags(o.InfraID, fmt.Sprintf("%s-private-%s", o.InfraID, zone)),
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("cannot create private subnet: %w", err)
	}
	return aws.StringValue(result.Subnet.SubnetId), nil
}

func (o *CreateInfraOptions) CreatePublicSubnet(client ec2iface.EC2API, vpcID string, zone string) (string, error) {
	result, err := client.CreateSubnet(&ec2.CreateSubnetInput{
		AvailabilityZone: aws.String(zone),
		VpcId:            aws.String(vpcID),
		CidrBlock:        aws.String(PublicSubnetCIDR),
		TagSpecifications: []*ec2.TagSpecification{
			{
				ResourceType: aws.String("subnet"),
				Tags:         ec2Tags(o.InfraID, fmt.Sprintf("%s-public-%s", o.InfraID, zone)),
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("cannot create public subnet: %w", err)
	}
	return aws.StringValue(result.Subnet.SubnetId), nil
}

func (o *CreateInfraOptions) CreateInternetGateway(client ec2iface.EC2API, vpcID string) (string, error) {
	result, err := client.CreateInternetGateway(&ec2.CreateInternetGatewayInput{
		TagSpecifications: []*ec2.TagSpecification{
			{
				ResourceType: aws.String("internet-gateway"),
				Tags:         ec2Tags(o.InfraID, fmt.Sprintf("%s-igw", o.InfraID)),
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("cannot create internet gateway: %w", err)
	}
	_, err = client.AttachInternetGateway(&ec2.AttachInternetGatewayInput{
		InternetGatewayId: result.InternetGateway.InternetGatewayId,
		VpcId:             aws.String(vpcID),
	})
	if err != nil {
		return "", fmt.Errorf("cannot attach internet gateway to vpc: %w", err)
	}
	return aws.StringValue(result.InternetGateway.InternetGatewayId), nil
}

func (o *CreateInfraOptions) CreateNATGateway(client ec2iface.EC2API, publicSubnetID, availabilityZone string) (string, error) {
	eipResult, err := client.AllocateAddress(&ec2.AllocateAddressInput{
		Domain: aws.String("vpc"),
	})
	if err != nil {
		return "", fmt.Errorf("cannot allocate EIP for NAT gateway: %w", err)
	}
	_, err = client.CreateTags(&ec2.CreateTagsInput{
		Resources: []*string{eipResult.AllocationId},
		Tags:      ec2Tags(o.InfraID, fmt.Sprintf("%s-eip-%s", o.InfraID, availabilityZone)),
	})
	if err != nil {
		return "", fmt.Errorf("cannot tag NAT gateway EIP: %w", err)
	}
	gatewayResult, err := client.CreateNatGateway(&ec2.CreateNatGatewayInput{
		AllocationId: eipResult.AllocationId,
		SubnetId:     aws.String(publicSubnetID),
		TagSpecifications: []*ec2.TagSpecification{
			{
				ResourceType: aws.String("natgateway"),
				Tags:         ec2Tags(o.InfraID, fmt.Sprintf("%s-nat-%s", o.InfraID, availabilityZone)),
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("cannot create NAT gateway: %w", err)
	}
	return aws.StringValue(gatewayResult.NatGateway.NatGatewayId), nil
}

func (o *CreateInfraOptions) CreateWorkerSecurityGroup(client ec2iface.EC2API, vpcID string) (string, error) {
	groupName := fmt.Sprintf("%s-worker-sg", o.InfraID)
	result, err := client.CreateSecurityGroup(&ec2.CreateSecurityGroupInput{
		GroupName:   aws.String(groupName),
		Description: aws.String("worker security group"),
		VpcId:       aws.String(vpcID),
		TagSpecifications: []*ec2.TagSpecification{
			{
				ResourceType: aws.String("security-group"),
				Tags:         ec2Tags(o.InfraID, groupName),
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("cannot create worker security group: %w", err)
	}
	securityGroupID := aws.StringValue(result.GroupId)
	ingressRules := []*ec2.AuthorizeSecurityGroupIngressInput{
		{
			GroupId:    aws.String(securityGroupID),
			IpProtocol: aws.String("icmp"),
			CidrIp:     aws.String(DefaultCIDRBlock),
			FromPort:   aws.Int64(-1),
			ToPort:     aws.Int64(-1),
		},
		{
			GroupId:    aws.String(securityGroupID),
			IpProtocol: aws.String("tcp"),
			CidrIp:     aws.String(DefaultCIDRBlock),
			FromPort:   aws.Int64(22),
			ToPort:     aws.Int64(22),
		},
		{
			GroupId: aws.String(securityGroupID),
			IpPermissions: []*ec2.IpPermission{
				{
					FromPort:   aws.Int64(4789),
					ToPort:     aws.Int64(4789),
					IpProtocol: aws.String("udp"),
					UserIdGroupPairs: []*ec2.UserIdGroupPair{
						{
							GroupId: aws.String(securityGroupID),
							VpcId:   aws.String(vpcID),
						},
					},
				},
			},
		},
		{
			GroupId: aws.String(securityGroupID),
			IpPermissions: []*ec2.IpPermission{
				{
					FromPort:   aws.Int64(6081),
					ToPort:     aws.Int64(6081),
					IpProtocol: aws.String("udp"),
					UserIdGroupPairs: []*ec2.UserIdGroupPair{
						{
							GroupId: aws.String(securityGroupID),
							VpcId:   aws.String(vpcID),
						},
					},
				},
			},
		},
		{
			GroupId: aws.String(securityGroupID),
			IpPermissions: []*ec2.IpPermission{
				{
					FromPort:   aws.Int64(500),
					ToPort:     aws.Int64(500),
					IpProtocol: aws.String("udp"),
					UserIdGroupPairs: []*ec2.UserIdGroupPair{
						{
							GroupId: aws.String(securityGroupID),
							VpcId:   aws.String(vpcID),
						},
					},
				},
			},
		},
		{
			GroupId: aws.String(securityGroupID),
			IpPermissions: []*ec2.IpPermission{
				{
					FromPort:   aws.Int64(4500),
					ToPort:     aws.Int64(4500),
					IpProtocol: aws.String("udp"),
					UserIdGroupPairs: []*ec2.UserIdGroupPair{
						{
							GroupId: aws.String(securityGroupID),
							VpcId:   aws.String(vpcID),
						},
					},
				},
			},
		},
		{
			GroupId: aws.String(securityGroupID),
			IpPermissions: []*ec2.IpPermission{
				{
					FromPort:   aws.Int64(0),
					ToPort:     aws.Int64(0),
					IpProtocol: aws.String("50"),
					UserIdGroupPairs: []*ec2.UserIdGroupPair{
						{
							GroupId: aws.String(securityGroupID),
							VpcId:   aws.String(vpcID),
						},
					},
				},
			},
		},
		{
			GroupId: aws.String(securityGroupID),
			IpPermissions: []*ec2.IpPermission{
				{
					FromPort:   aws.Int64(9000),
					ToPort:     aws.Int64(9999),
					IpProtocol: aws.String("tcp"),
					UserIdGroupPairs: []*ec2.UserIdGroupPair{
						{
							GroupId: aws.String(securityGroupID),
							VpcId:   aws.String(vpcID),
						},
					},
				},
			},
		},
		{
			GroupId: aws.String(securityGroupID),
			IpPermissions: []*ec2.IpPermission{
				{
					FromPort:   aws.Int64(9000),
					ToPort:     aws.Int64(9999),
					IpProtocol: aws.String("udp"),
					UserIdGroupPairs: []*ec2.UserIdGroupPair{
						{
							GroupId: aws.String(securityGroupID),
							VpcId:   aws.String(vpcID),
						},
					},
				},
			},
		},
		{
			GroupId: aws.String(securityGroupID),
			IpPermissions: []*ec2.IpPermission{
				{
					FromPort:   aws.Int64(10250),
					ToPort:     aws.Int64(10250),
					IpProtocol: aws.String("tcp"),
					UserIdGroupPairs: []*ec2.UserIdGroupPair{
						{
							GroupId: aws.String(securityGroupID),
							VpcId:   aws.String(vpcID),
						},
					},
				},
			},
		},
		{
			GroupId: aws.String(securityGroupID),
			IpPermissions: []*ec2.IpPermission{
				{
					FromPort:   aws.Int64(30000),
					ToPort:     aws.Int64(32767),
					IpProtocol: aws.String("tcp"),
					UserIdGroupPairs: []*ec2.UserIdGroupPair{
						{
							GroupId: aws.String(securityGroupID),
							VpcId:   aws.String(vpcID),
						},
					},
				},
			},
		},
		{
			GroupId: aws.String(securityGroupID),
			IpPermissions: []*ec2.IpPermission{
				{
					FromPort:   aws.Int64(30000),
					ToPort:     aws.Int64(32767),
					IpProtocol: aws.String("udp"),
					UserIdGroupPairs: []*ec2.UserIdGroupPair{
						{
							GroupId: aws.String(securityGroupID),
							VpcId:   aws.String(vpcID),
						},
					},
				},
			},
		},
	}
	for _, ingress := range ingressRules {
		_, err := client.AuthorizeSecurityGroupIngress(ingress)
		if err != nil {
			return "", fmt.Errorf("cannot apply security group ingress rule: %w", err)
		}
	}
	return securityGroupID, nil
}

func (o *CreateInfraOptions) CreatePrivateRouteTable(client ec2iface.EC2API, vpcID, natGatewayID, subnetID, zone string) (string, error) {
	result, err := client.CreateRouteTable(&ec2.CreateRouteTableInput{
		VpcId: aws.String(vpcID),
		TagSpecifications: []*ec2.TagSpecification{
			{
				ResourceType: aws.String("route-table"),
				Tags:         ec2Tags(o.InfraID, fmt.Sprintf("%s-private-%s", o.InfraID, zone)),
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("cannot create private route table: %w", err)
	}
	tableID := aws.StringValue(result.RouteTable.RouteTableId)
	_, err = client.CreateRoute(&ec2.CreateRouteInput{
		RouteTableId:         aws.String(tableID),
		NatGatewayId:         aws.String(natGatewayID),
		DestinationCidrBlock: aws.String("0.0.0.0/0"),
	})
	if err != nil {
		return "", fmt.Errorf("cannot create nat gateway route in private route table: %w", err)
	}
	_, err = client.AssociateRouteTable(&ec2.AssociateRouteTableInput{
		RouteTableId: aws.String(tableID),
		SubnetId:     aws.String(subnetID),
	})
	if err != nil {
		return "", fmt.Errorf("cannot associate private route table with subnet: %w", err)
	}
	return tableID, nil
}

func (o *CreateInfraOptions) CreatePublicRouteTable(client ec2iface.EC2API, vpcID, igwID, subnetID, zone string) (string, error) {
	result, err := client.CreateRouteTable(&ec2.CreateRouteTableInput{
		VpcId: aws.String(vpcID),
		TagSpecifications: []*ec2.TagSpecification{
			{
				ResourceType: aws.String("route-table"),
				Tags:         ec2Tags(o.InfraID, fmt.Sprintf("%s-public-%s", o.InfraID, zone)),
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("cannot create public route table: %w", err)
	}
	tableID := aws.StringValue(result.RouteTable.RouteTableId)

	// Replace the VPC's main route table
	routeTableInfo, err := client.DescribeRouteTables(&ec2.DescribeRouteTablesInput{
		Filters: []*ec2.Filter{
			{
				Name:   aws.String("vpc-id"),
				Values: []*string{aws.String(vpcID)},
			},
			{
				Name:   aws.String("association.main"),
				Values: []*string{aws.String("true")},
			},
		},
	})
	if len(routeTableInfo.RouteTables) != 1 {
		return "", fmt.Errorf("unexpected number of route tables associated with vpc: %d", len(routeTableInfo.RouteTables))
	}
	associationID := ""
	for _, assoc := range routeTableInfo.RouteTables[0].Associations {
		if aws.BoolValue(assoc.Main) {
			associationID = aws.StringValue(assoc.RouteTableAssociationId)
			break
		}
	}
	_, err = client.ReplaceRouteTableAssociation(&ec2.ReplaceRouteTableAssociationInput{
		RouteTableId:  aws.String(tableID),
		AssociationId: aws.String(associationID),
	})
	if err != nil {
		return "", fmt.Errorf("cannot set vpc main route table: %w", err)
	}

	// Associate the route table with the public subnet ID
	_, err = client.AssociateRouteTable(&ec2.AssociateRouteTableInput{
		RouteTableId: aws.String(tableID),
		SubnetId:     aws.String(subnetID),
	})
	if err != nil {
		return "", fmt.Errorf("cannot associate private route table with subnet: %w", err)
	}

	// Create route to internet gateway
	_, err = client.CreateRoute(&ec2.CreateRouteInput{
		DestinationCidrBlock: aws.String("0.0.0.0/0"),
		RouteTableId:         aws.String(tableID),
		GatewayId:            aws.String(igwID),
	})
	if err != nil {
		return "", fmt.Errorf("cannot create route to internet gateway: %w", err)
	}
	return tableID, nil
}

func (o *CreateInfraOptions) AWSClient() (ec2iface.EC2API, error) {
	awsConfig := &aws.Config{
		Region: aws.String(o.Region),
	}
	awsConfig.Credentials = credentials.NewSharedCredentials(o.AWSCredentialsFile, "default")
	s, err := session.NewSession(awsConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create client session: %w", err)
	}
	return ec2.New(s), nil
}

func ec2Tags(infraID, name string) []*ec2.Tag {
	tags := []*ec2.Tag{
		{
			Key:   aws.String(fmt.Sprintf("kubernetes.io/cluster/%s", infraID)),
			Value: aws.String("owned"),
		},
	}
	if len(name) > 0 {
		tags = append(tags, &ec2.Tag{
			Key:   aws.String("Name"),
			Value: aws.String(name),
		})
	}
	return tags

}
