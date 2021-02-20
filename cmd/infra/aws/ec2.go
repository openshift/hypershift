package aws

import (
	"fmt"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"
)

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
