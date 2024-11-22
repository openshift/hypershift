package aws

import (
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"
	"github.com/go-logr/logr"
	"github.com/openshift/hypershift/cmd/util"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
)

const (
	invalidNATGatewayError   = "InvalidNatGatewayID.NotFound"
	invalidRouteTableID      = "InvalidRouteTableId.NotFound"
	invalidElasticIPNotFound = "InvalidElasticIpID.NotFound"
	invalidSubnet            = "InvalidSubnet"

	// tagNameSubnetInternalELB is the tag name used on a subnet to designate that
	// it should be used for internal ELBs
	tagNameSubnetInternalELB = "kubernetes.io/role/internal-elb"

	// tagNameSubnetPublicELB is the tag name used on a subnet to designate that
	// it should be used for internet ELBs
	tagNameSubnetPublicELB = "kubernetes.io/role/elb"
)

var (
	retryBackoff = wait.Backoff{
		Steps:    5,
		Duration: 3 * time.Second,
		Factor:   3.0,
		Jitter:   0.1,
	}
)

func (o *CreateInfraOptions) firstZone(l logr.Logger, client ec2iface.EC2API) (string, error) {
	result, err := client.DescribeAvailabilityZones(&ec2.DescribeAvailabilityZonesInput{})
	if err != nil {
		return "", fmt.Errorf("failed to list availability zones: %w", err)
	}
	if len(result.AvailabilityZones) == 0 {
		return "", fmt.Errorf("no availability zones found")

	}
	zone := aws.StringValue(result.AvailabilityZones[0].ZoneName)
	l.Info("Using zone", "zone", zone)
	return zone, nil
}

func (o *CreateInfraOptions) createVPC(l logr.Logger, client ec2iface.EC2API) (string, error) {
	vpcName := fmt.Sprintf("%s-vpc", o.InfraID)
	vpcID, err := o.existingVPC(client, vpcName)
	if err != nil {
		return "", err
	}
	if len(vpcID) == 0 {
		createResult, err := client.CreateVpc(&ec2.CreateVpcInput{
			CidrBlock:         aws.String(o.VPCCIDR),
			TagSpecifications: o.ec2TagSpecifications("vpc", vpcName),
		})
		if err != nil {
			return "", fmt.Errorf("failed to create VPC: %w", err)
		}
		vpcID = aws.StringValue(createResult.Vpc.VpcId)
		l.Info("Created VPC", "id", vpcID)
	} else {
		l.Info("Found existing VPC", "id", vpcID)
	}
	_, err = client.ModifyVpcAttribute(&ec2.ModifyVpcAttributeInput{
		VpcId:            aws.String(vpcID),
		EnableDnsSupport: &ec2.AttributeBooleanValue{Value: aws.Bool(true)},
	})
	if err != nil {
		return "", fmt.Errorf("failed to modify VPC attributes: %w", err)
	}
	l.Info("Enabled DNS support on VPC", "id", vpcID)
	_, err = client.ModifyVpcAttribute(&ec2.ModifyVpcAttributeInput{
		VpcId:              aws.String(vpcID),
		EnableDnsHostnames: &ec2.AttributeBooleanValue{Value: aws.Bool(true)},
	})
	if err != nil {
		return "", fmt.Errorf("failed to modify VPC attributes: %w", err)
	}
	l.Info("Enabled DNS hostnames on VPC", "id", vpcID)
	return vpcID, nil
}

func (o *CreateInfraOptions) deleteVPC(l logr.Logger, client ec2iface.EC2API, vpcID string) error {
	if _, err := client.DeleteVpc(&ec2.DeleteVpcInput{
		VpcId: aws.String(vpcID),
	}); err != nil {
		return fmt.Errorf("failed to delete VPC %s: %w", vpcID, err)
	}
	l.Info("deleted VPC", "id", vpcID)
	return nil
}

func (o *CreateInfraOptions) existingVPC(client ec2iface.EC2API, vpcName string) (string, error) {
	var vpcID string
	result, err := client.DescribeVpcs(&ec2.DescribeVpcsInput{Filters: o.ec2Filters(vpcName)})
	if err != nil {
		return "", fmt.Errorf("cannot list vpcs: %w", err)
	}
	for _, vpc := range result.Vpcs {
		vpcID = aws.StringValue(vpc.VpcId)
		break
	}
	return vpcID, nil
}

func (o *CreateInfraOptions) CreateVPCS3Endpoint(l logr.Logger, client ec2iface.EC2API, vpcID string, routeTableIds []*string) error {
	existingEndpoint, err := o.existingVPCS3Endpoint(client)
	if err != nil {
		return err
	}
	if len(existingEndpoint) > 0 {
		l.Info("Found existing s3 VPC endpoint", "id", existingEndpoint)
		return nil
	}
	isRetriable := func(err error) bool {
		if awsErr, ok := err.(awserr.Error); ok {
			return strings.EqualFold(awsErr.Code(), invalidRouteTableID)
		}
		return false
	}
	if err = retry.OnError(retryBackoff, isRetriable, func() error {
		result, err := client.CreateVpcEndpoint(&ec2.CreateVpcEndpointInput{
			VpcId:             aws.String(vpcID),
			ServiceName:       aws.String(fmt.Sprintf("com.amazonaws.%s.s3", o.Region)),
			RouteTableIds:     routeTableIds,
			TagSpecifications: o.ec2TagSpecifications("vpc-endpoint", ""),
		})
		if err == nil {
			l.Info("Created s3 VPC endpoint", "id", aws.StringValue(result.VpcEndpoint.VpcEndpointId))
		}
		return err
	}); err != nil {
		return fmt.Errorf("cannot create VPC S3 endpoint: %w", err)
	}
	return nil
}

func (o *CreateInfraOptions) existingVPCS3Endpoint(client ec2iface.EC2API) (string, error) {
	var endpointID string
	result, err := client.DescribeVpcEndpoints(&ec2.DescribeVpcEndpointsInput{Filters: o.ec2Filters("")})
	if err != nil {
		return "", fmt.Errorf("cannot list vpc endpoints: %w", err)
	}
	for _, endpoint := range result.VpcEndpoints {
		endpointID = aws.StringValue(endpoint.VpcEndpointId)
	}
	return endpointID, nil
}

func (o *CreateInfraOptions) CreateDHCPOptions(l logr.Logger, client ec2iface.EC2API, vpcID string) error {
	domainName := "ec2.internal"
	if o.Region != "us-east-1" {
		domainName = fmt.Sprintf("%s.compute.internal", o.Region)
	}
	optID, err := o.existingDHCPOptions(client)
	if err != nil {
		return err
	}
	if len(optID) == 0 {
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
			TagSpecifications: o.ec2TagSpecifications("dhcp-options", ""),
		})
		if err != nil {
			return fmt.Errorf("cannot create dhcp-options: %w", err)
		}
		optID = aws.StringValue(result.DhcpOptions.DhcpOptionsId)
		l.Info("Created DHCP options", "id", optID)
	} else {
		l.Info("Found existing DHCP options", "id", optID)
	}
	_, err = client.AssociateDhcpOptions(&ec2.AssociateDhcpOptionsInput{
		DhcpOptionsId: aws.String(optID),
		VpcId:         aws.String(vpcID),
	})
	if err != nil {
		return fmt.Errorf("cannot associate dhcp-options to VPC: %w", err)
	}
	l.Info("Associated DHCP options with VPC", "vpc", vpcID, "dhcp options", optID)
	return nil
}

func (o *CreateInfraOptions) existingDHCPOptions(client ec2iface.EC2API) (string, error) {
	var optID string
	result, err := client.DescribeDhcpOptions(&ec2.DescribeDhcpOptionsInput{Filters: o.ec2Filters("")})
	if err != nil {
		return "", fmt.Errorf("cannot list dhcp options: %w", err)
	}
	for _, opt := range result.DhcpOptions {
		optID = aws.StringValue(opt.DhcpOptionsId)
		break
	}
	return optID, nil
}

func (o *CreateInfraOptions) CreatePrivateSubnet(l logr.Logger, client ec2iface.EC2API, vpcID string, zone string, cidr string) (string, error) {
	return o.CreateSubnet(l, client, vpcID, zone, cidr, fmt.Sprintf("%s-private-%s", o.InfraID, zone), tagNameSubnetInternalELB)
}

func (o *CreateInfraOptions) CreatePublicSubnet(l logr.Logger, client ec2iface.EC2API, vpcID string, zone string, cidr string) (string, error) {
	return o.CreateSubnet(l, client, vpcID, zone, cidr, fmt.Sprintf("%s-public-%s", o.InfraID, zone), tagNameSubnetPublicELB)
}

func (o *CreateInfraOptions) CreateSubnet(l logr.Logger, client ec2iface.EC2API, vpcID, zone, cidr, name, scopeTag string) (string, error) {
	subnetID, err := o.existingSubnet(client, name)
	if err != nil {
		return "", err
	}
	if len(subnetID) > 0 {
		l.Info("Found existing subnet", "name", name, "id", subnetID)
		return subnetID, nil
	}
	tagSpec := o.ec2TagSpecifications("subnet", name)
	tagSpec[0].Tags = append(tagSpec[0].Tags, &ec2.Tag{
		Key:   aws.String(scopeTag),
		Value: aws.String("1"),
	})

	result, err := client.CreateSubnet(&ec2.CreateSubnetInput{
		AvailabilityZone:  aws.String(zone),
		VpcId:             aws.String(vpcID),
		CidrBlock:         aws.String(cidr),
		TagSpecifications: tagSpec,
	})
	if err != nil {
		return "", fmt.Errorf("cannot create public subnet: %w", err)
	}
	backoff := wait.Backoff{
		Steps:    10,
		Duration: 3 * time.Second,
		Factor:   1.0,
		Jitter:   0.1,
	}
	var subnetResult *ec2.DescribeSubnetsOutput
	err = retry.OnError(backoff, func(error) bool { return true }, func() error {
		var err error
		subnetResult, err = client.DescribeSubnets(&ec2.DescribeSubnetsInput{
			SubnetIds: []*string{result.Subnet.SubnetId},
		})
		if err != nil || len(subnetResult.Subnets) == 0 {
			return fmt.Errorf("not found yet")
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("cannot find subnet that was just created (%s)", aws.StringValue(result.Subnet.SubnetId))
	}
	subnetID = aws.StringValue(result.Subnet.SubnetId)
	l.Info("Created subnet", "name", name, "id", subnetID)
	return subnetID, nil
}

func (o *CreateInfraOptions) existingSubnet(client ec2iface.EC2API, name string) (string, error) {
	var subnetID string
	result, err := client.DescribeSubnets(&ec2.DescribeSubnetsInput{Filters: o.ec2Filters(name)})
	if err != nil {
		return "", fmt.Errorf("cannot list subnets: %w", err)
	}
	for _, subnet := range result.Subnets {
		subnetID = aws.StringValue(subnet.SubnetId)
		break
	}
	return subnetID, nil
}

func (o *CreateInfraOptions) CreateInternetGateway(l logr.Logger, client ec2iface.EC2API, vpcID string) (string, error) {
	gatewayName := fmt.Sprintf("%s-igw", o.InfraID)
	igw, err := o.existingInternetGateway(client, gatewayName)
	if err != nil {
		return "", err
	}
	if igw == nil {
		result, err := client.CreateInternetGateway(&ec2.CreateInternetGatewayInput{
			TagSpecifications: o.ec2TagSpecifications("internet-gateway", fmt.Sprintf("%s-igw", o.InfraID)),
		})
		if err != nil {
			return "", fmt.Errorf("cannot create internet gateway: %w", err)
		}
		igw = result.InternetGateway
		l.Info("Created internet gateway", "id", aws.StringValue(igw.InternetGatewayId))
	} else {
		l.Info("Found existing internet gateway", "id", aws.StringValue(igw.InternetGatewayId))
	}
	attached := false
	for _, attachment := range igw.Attachments {
		if aws.StringValue(attachment.VpcId) == vpcID {
			attached = true
			break
		}
	}
	if !attached {
		_, err = client.AttachInternetGateway(&ec2.AttachInternetGatewayInput{
			InternetGatewayId: igw.InternetGatewayId,
			VpcId:             aws.String(vpcID),
		})
		if err != nil {
			return "", fmt.Errorf("cannot attach internet gateway to vpc: %w", err)
		}
		l.Info("Attached internet gateway to VPC", "internet gateway", aws.StringValue(igw.InternetGatewayId), "vpc", vpcID)
	}
	return aws.StringValue(igw.InternetGatewayId), nil
}

func (o *CreateInfraOptions) existingInternetGateway(client ec2iface.EC2API, name string) (*ec2.InternetGateway, error) {
	result, err := client.DescribeInternetGateways(&ec2.DescribeInternetGatewaysInput{Filters: o.ec2Filters(name)})
	if err != nil {
		return nil, fmt.Errorf("cannot list internet gateways: %w", err)
	}
	for _, igw := range result.InternetGateways {
		return igw, nil
	}
	return nil, nil
}

func (o *CreateInfraOptions) CreateNATGateway(l logr.Logger, client ec2iface.EC2API, publicSubnetID, availabilityZone string) (string, error) {
	natGatewayName := fmt.Sprintf("%s-nat-%s", o.InfraID, availabilityZone)
	natGateway, _ := o.existingNATGateway(client, natGatewayName)
	if natGateway != nil {
		l.Info("Found existing NAT gateway", "id", aws.StringValue(natGateway.NatGatewayId))
		return *natGateway.NatGatewayId, nil
	}

	eipResult, err := client.AllocateAddress(&ec2.AllocateAddressInput{
		Domain: aws.String("vpc"),
	})
	if err != nil {
		return "", fmt.Errorf("cannot allocate EIP for NAT gateway: %w", err)
	}
	allocationID := aws.StringValue(eipResult.AllocationId)
	l.Info("Created elastic IP for NAT gateway", "id", allocationID)

	// NOTE: there's a potential to leak EIP addresses if the following tag operation fails, since we have no way of
	// recognizing the EIP as belonging to the cluster
	isRetriable := func(err error) bool {
		if awsErr, ok := err.(awserr.Error); ok {
			return strings.EqualFold(awsErr.Code(), invalidElasticIPNotFound)
		}
		return false
	}
	err = retry.OnError(retryBackoff, isRetriable, func() error {
		_, err = client.CreateTags(&ec2.CreateTagsInput{
			Resources: []*string{aws.String(allocationID)},
			Tags:      append(ec2Tags(o.InfraID, fmt.Sprintf("%s-eip-%s", o.InfraID, availabilityZone)), o.additionalEC2Tags...),
		})
		return err
	})
	if err != nil {
		return "", fmt.Errorf("cannot tag NAT gateway EIP: %w", err)
	}

	isNATGatewayRetriable := func(err error) bool {
		if awsErr, ok := err.(awserr.Error); ok {
			return strings.EqualFold(awsErr.Code(), invalidSubnet) ||
				strings.EqualFold(awsErr.Code(), invalidElasticIPNotFound)
		}
		return false
	}
	err = retry.OnError(retryBackoff, isNATGatewayRetriable, func() error {
		gatewayResult, err := client.CreateNatGateway(&ec2.CreateNatGatewayInput{
			AllocationId:      aws.String(allocationID),
			SubnetId:          aws.String(publicSubnetID),
			TagSpecifications: o.ec2TagSpecifications("natgateway", natGatewayName),
		})
		if err != nil {
			return err
		}
		natGateway = gatewayResult.NatGateway
		l.Info("Created NAT gateway", "id", aws.StringValue(natGateway.NatGatewayId))
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("cannot create NAT gateway: %w", err)
	}

	natGatewayID := aws.StringValue(natGateway.NatGatewayId)
	return natGatewayID, nil
}

func (o *CreateInfraOptions) existingNATGateway(client ec2iface.EC2API, name string) (*ec2.NatGateway, error) {
	result, err := client.DescribeNatGateways(&ec2.DescribeNatGatewaysInput{Filter: o.ec2Filters(name)})
	if err != nil {
		return nil, fmt.Errorf("cannot list NAT gateways: %w", err)
	}
	for _, gateway := range result.NatGateways {
		state := aws.StringValue(gateway.State)
		if state == "deleted" || state == "deleting" || state == "failed" {
			continue
		}
		return gateway, nil
	}
	return nil, nil
}

func (o *CreateInfraOptions) CreatePrivateRouteTable(l logr.Logger, client ec2iface.EC2API, vpcID, natGatewayID, subnetID, zone string) (string, error) {
	tableName := fmt.Sprintf("%s-private-%s", o.InfraID, zone)
	routeTable, err := o.existingRouteTable(l, client, tableName)
	if err != nil {
		return "", err
	}
	if routeTable == nil {
		routeTable, err = o.createRouteTable(l, client, vpcID, tableName)
		if err != nil {
			return "", err
		}
	}

	// Everything below this is only needed if direct internet access is used
	if o.EnableProxy {
		return aws.StringValue(routeTable.RouteTableId), nil
	}

	if !o.hasNATGatewayRoute(routeTable, natGatewayID) {
		isRetriable := func(err error) bool {
			if awsErr, ok := err.(awserr.Error); ok {
				return strings.EqualFold(awsErr.Code(), invalidNATGatewayError)
			}
			return false
		}
		err = retry.OnError(retryBackoff, isRetriable, func() error {
			_, err = client.CreateRoute(&ec2.CreateRouteInput{
				RouteTableId:         routeTable.RouteTableId,
				NatGatewayId:         aws.String(natGatewayID),
				DestinationCidrBlock: aws.String("0.0.0.0/0"),
			})
			return err
		})
		if err != nil {
			return "", fmt.Errorf("cannot create nat gateway route in private route table: %w", err)
		}
		l.Info("Created route to NAT gateway", "route table", aws.StringValue(routeTable.RouteTableId), "nat gateway", natGatewayID)
	} else {
		l.Info("Found existing route to NAT gateway", "route table", aws.StringValue(routeTable.RouteTableId), "nat gateway", natGatewayID)
	}
	if !o.hasAssociatedSubnet(routeTable, subnetID) {
		_, err = client.AssociateRouteTable(&ec2.AssociateRouteTableInput{
			RouteTableId: routeTable.RouteTableId,
			SubnetId:     aws.String(subnetID),
		})
		if err != nil {
			return "", fmt.Errorf("cannot associate private route table with subnet: %w", err)
		}
		l.Info("Associated subnet with route table", "route table", aws.StringValue(routeTable.RouteTableId), "subnet", subnetID)
	} else {
		l.Info("Subnet already associated with route table", "route table", aws.StringValue(routeTable.RouteTableId), "subnet", subnetID)
	}
	return aws.StringValue(routeTable.RouteTableId), nil
}

func (o *CreateInfraOptions) CreatePublicRouteTable(l logr.Logger, client ec2iface.EC2API, vpcID, igwID string, subnetIDs []string) (string, error) {
	tableName := fmt.Sprintf("%s-public", o.InfraID)
	routeTable, err := o.existingRouteTable(l, client, tableName)
	if err != nil {
		return "", err
	}
	if routeTable == nil {
		routeTable, err = o.createRouteTable(l, client, vpcID, tableName)
		if err != nil {
			return "", err
		}
	}
	tableID := aws.StringValue(routeTable.RouteTableId)
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
	if err != nil {
		return "", err
	}
	if len(routeTableInfo.RouteTables) == 0 {
		return "", fmt.Errorf("no route tables associated with the vpc")
	}
	// Replace route table association only if it's not the associated route table already
	if aws.StringValue(routeTableInfo.RouteTables[0].RouteTableId) != tableID {
		var associationID string
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
		l.Info("Set main VPC route table", "route table", tableID, "vpc", vpcID)
	}

	// Create route to internet gateway
	if !o.hasInternetGatewayRoute(routeTable, igwID) {
		_, err = client.CreateRoute(&ec2.CreateRouteInput{
			DestinationCidrBlock: aws.String("0.0.0.0/0"),
			RouteTableId:         aws.String(tableID),
			GatewayId:            aws.String(igwID),
		})
		if err != nil {
			return "", fmt.Errorf("cannot create route to internet gateway: %w", err)
		}
		l.Info("Created route to internet gateway", "route table", tableID, "internet gateway", igwID)
	} else {
		l.Info("Found existing route to internet gateway", "route table", tableID, "internet gateway", igwID)
	}

	// Associate the route table with the public subnet ID
	for _, subnetID := range subnetIDs {
		if !o.hasAssociatedSubnet(routeTable, subnetID) {
			_, err = client.AssociateRouteTable(&ec2.AssociateRouteTableInput{
				RouteTableId: aws.String(tableID),
				SubnetId:     aws.String(subnetID),
			})
			if err != nil {
				return "", fmt.Errorf("cannot associate private route table with subnet: %w", err)
			}
			l.Info("Associated route table with subnet", "route table", tableID, "subnet", subnetID)
		} else {
			l.Info("Found existing association between route table and subnet", "route table", tableID, "subnet", subnetID)
		}
	}
	return tableID, nil
}

func (o *CreateInfraOptions) createRouteTable(l logr.Logger, client ec2iface.EC2API, vpcID, name string) (*ec2.RouteTable, error) {
	result, err := client.CreateRouteTable(&ec2.CreateRouteTableInput{
		VpcId:             aws.String(vpcID),
		TagSpecifications: o.ec2TagSpecifications("route-table", name),
	})
	if err != nil {
		return nil, fmt.Errorf("cannot create route table: %w", err)
	}
	l.Info("Created route table", "name", name, "id", aws.StringValue(result.RouteTable.RouteTableId))
	return result.RouteTable, nil
}

func (o *CreateInfraOptions) existingRouteTable(l logr.Logger, client ec2iface.EC2API, name string) (*ec2.RouteTable, error) {
	result, err := client.DescribeRouteTables(&ec2.DescribeRouteTablesInput{Filters: o.ec2Filters(name)})
	if err != nil {
		return nil, fmt.Errorf("cannot list route tables: %w", err)
	}
	if len(result.RouteTables) > 0 {
		l.Info("Found existing route table", "name", name, "id", aws.StringValue(result.RouteTables[0].RouteTableId))
		return result.RouteTables[0], nil
	}
	return nil, nil
}

func (o *CreateInfraOptions) hasNATGatewayRoute(table *ec2.RouteTable, natGatewayID string) bool {
	for _, route := range table.Routes {
		if aws.StringValue(route.NatGatewayId) == natGatewayID &&
			aws.StringValue(route.DestinationCidrBlock) == "0.0.0.0/0" {
			return true
		}
	}
	return false
}

func (o *CreateInfraOptions) hasInternetGatewayRoute(table *ec2.RouteTable, igwID string) bool {
	for _, route := range table.Routes {
		if aws.StringValue(route.GatewayId) == igwID &&
			aws.StringValue(route.DestinationCidrBlock) == "0.0.0.0/0" {
			return true
		}
	}
	return false
}

func (o *CreateInfraOptions) hasAssociatedSubnet(table *ec2.RouteTable, subnetID string) bool {
	for _, assoc := range table.Associations {
		if aws.StringValue(assoc.RouteTableId) == subnetID {
			return true
		}
	}
	return false
}

func (o *CreateInfraOptions) ec2TagSpecifications(resourceType, name string) []*ec2.TagSpecification {
	return []*ec2.TagSpecification{
		{
			ResourceType: aws.String(resourceType),
			Tags:         append(ec2Tags(o.InfraID, name), o.additionalEC2Tags...),
		},
	}
}

func (o *CreateInfraOptions) parseAdditionalTags() error {
	parsed, err := util.ParseAWSTags(o.AdditionalTags)
	if err != nil {
		return err
	}
	for k, v := range parsed {
		o.additionalEC2Tags = append(o.additionalEC2Tags, &ec2.Tag{
			Key:   aws.String(k),
			Value: aws.String(v),
		})
	}
	return nil
}

func (o *CreateInfraOptions) ec2Filters(name string) []*ec2.Filter {
	filters := []*ec2.Filter{
		{
			Name:   aws.String(fmt.Sprintf("tag:%s", clusterTag(o.InfraID))),
			Values: []*string{aws.String(clusterTagValue)},
		},
	}
	if len(name) > 0 {
		filters = append(filters, &ec2.Filter{
			Name:   aws.String("tag:Name"),
			Values: []*string{aws.String(name)},
		})
	}
	return filters
}

func clusterTag(infraID string) string {
	return fmt.Sprintf("kubernetes.io/cluster/%s", infraID)
}

func ec2Tags(infraID, name string) []*ec2.Tag {
	tags := []*ec2.Tag{
		{
			Key:   aws.String(clusterTag(infraID)),
			Value: aws.String(clusterTagValue),
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
