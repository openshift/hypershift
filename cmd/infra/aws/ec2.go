package aws

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/support/awsapi"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"

	"github.com/go-logr/logr"
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

func (o *CreateInfraOptions) firstZone(ctx context.Context, l logr.Logger, client awsapi.EC2API) (string, error) {
	result, err := client.DescribeAvailabilityZones(ctx, &ec2.DescribeAvailabilityZonesInput{})
	if err != nil {
		return "", fmt.Errorf("failed to list availability zones: %w", err)
	}
	if len(result.AvailabilityZones) == 0 {
		return "", fmt.Errorf("no availability zones found")

	}
	zone := aws.ToString(result.AvailabilityZones[0].ZoneName)
	l.Info("Using zone", "zone", zone)
	return zone, nil
}

func (o *CreateInfraOptions) createVPC(ctx context.Context, l logr.Logger, client awsapi.EC2API) (string, error) {
	vpcName := fmt.Sprintf("%s-vpc", o.InfraID)
	vpcID, err := o.existingVPC(ctx, client, vpcName)
	if err != nil {
		return "", err
	}
	if len(vpcID) == 0 {
		createResult, err := client.CreateVpc(ctx, &ec2.CreateVpcInput{
			CidrBlock:         aws.String(o.VPCCIDR),
			TagSpecifications: o.ec2TagSpecifications("vpc", vpcName),
		})
		if err != nil {
			return "", fmt.Errorf("failed to create VPC: %w", err)
		}
		vpcID = aws.ToString(createResult.Vpc.VpcId)
		l.Info("Created VPC", "id", vpcID)
	} else {
		l.Info("Found existing VPC", "id", vpcID)
	}
	_, err = client.ModifyVpcAttribute(ctx, &ec2.ModifyVpcAttributeInput{
		VpcId:            aws.String(vpcID),
		EnableDnsSupport: &ec2types.AttributeBooleanValue{Value: aws.Bool(true)},
	})
	if err != nil {
		return "", fmt.Errorf("failed to modify VPC attributes: %w", err)
	}
	l.Info("Enabled DNS support on VPC", "id", vpcID)
	_, err = client.ModifyVpcAttribute(ctx, &ec2.ModifyVpcAttributeInput{
		VpcId:              aws.String(vpcID),
		EnableDnsHostnames: &ec2types.AttributeBooleanValue{Value: aws.Bool(true)},
	})
	if err != nil {
		return "", fmt.Errorf("failed to modify VPC attributes: %w", err)
	}
	l.Info("Enabled DNS hostnames on VPC", "id", vpcID)
	return vpcID, nil
}

func (o *CreateInfraOptions) deleteVPC(ctx context.Context, l logr.Logger, client awsapi.EC2API, vpcID string) error {
	if _, err := client.DeleteVpc(ctx, &ec2.DeleteVpcInput{
		VpcId: aws.String(vpcID),
	}); err != nil {
		return fmt.Errorf("failed to delete VPC %s: %w", vpcID, err)
	}
	l.Info("deleted VPC", "id", vpcID)
	return nil
}

func (o *CreateInfraOptions) existingVPC(ctx context.Context, client awsapi.EC2API, vpcName string) (string, error) {
	var vpcID string
	result, err := client.DescribeVpcs(ctx, &ec2.DescribeVpcsInput{Filters: o.ec2Filters(vpcName)})
	if err != nil {
		return "", fmt.Errorf("cannot list vpcs: %w", err)
	}
	for _, vpc := range result.Vpcs {
		vpcID = aws.ToString(vpc.VpcId)
		break
	}
	return vpcID, nil
}

func (o *CreateInfraOptions) CreateVPCS3Endpoint(ctx context.Context, l logr.Logger, client awsapi.EC2API, vpcID string, routeTableIds []string) error {
	existingEndpoint, err := o.existingVPCS3Endpoint(ctx, client)
	if err != nil {
		return err
	}
	if len(existingEndpoint) > 0 {
		l.Info("Found existing s3 VPC endpoint", "id", existingEndpoint)
		return nil
	}
	isRetriable := func(err error) bool {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			return strings.EqualFold(apiErr.ErrorCode(), invalidRouteTableID)
		}
		return false
	}
	if err = retry.OnError(retryBackoff, isRetriable, func() error {
		result, err := client.CreateVpcEndpoint(ctx, &ec2.CreateVpcEndpointInput{
			VpcId:             aws.String(vpcID),
			ServiceName:       aws.String(fmt.Sprintf("com.amazonaws.%s.s3", o.Region)),
			RouteTableIds:     routeTableIds,
			TagSpecifications: o.ec2TagSpecifications("vpc-endpoint", ""),
		})
		if err == nil {
			l.Info("Created s3 VPC endpoint", "id", aws.ToString(result.VpcEndpoint.VpcEndpointId))
		}
		return err
	}); err != nil {
		return fmt.Errorf("cannot create VPC S3 endpoint: %w", err)
	}
	return nil
}

func (o *CreateInfraOptions) existingVPCS3Endpoint(ctx context.Context, client awsapi.EC2API) (string, error) {
	result, err := client.DescribeVpcEndpoints(ctx, &ec2.DescribeVpcEndpointsInput{Filters: o.ec2Filters("")})
	if err != nil {
		return "", fmt.Errorf("cannot list vpc endpoints: %w", err)
	}
	for _, endpoint := range result.VpcEndpoints {
		if aws.ToString(endpoint.ServiceName) == fmt.Sprintf("com.amazonaws.%s.s3", o.Region) {
			return aws.ToString(endpoint.VpcEndpointId), nil
		}
	}
	return "", nil
}

func (o *CreateInfraOptions) CreateDHCPOptions(ctx context.Context, l logr.Logger, client awsapi.EC2API, vpcID string) error {
	domainName := "ec2.internal"
	if o.Region != "us-east-1" {
		domainName = fmt.Sprintf("%s.compute.internal", o.Region)
	}
	optID, err := o.existingDHCPOptions(ctx, client)
	if err != nil {
		return err
	}
	if len(optID) == 0 {
		result, err := client.CreateDhcpOptions(ctx, &ec2.CreateDhcpOptionsInput{
			DhcpConfigurations: []ec2types.NewDhcpConfiguration{
				{
					Key:    aws.String("domain-name"),
					Values: []string{domainName},
				},
				{
					Key:    aws.String("domain-name-servers"),
					Values: []string{"AmazonProvidedDNS"},
				},
			},
			TagSpecifications: o.ec2TagSpecifications("dhcp-options", ""),
		})
		if err != nil {
			return fmt.Errorf("cannot create dhcp-options: %w", err)
		}
		optID = aws.ToString(result.DhcpOptions.DhcpOptionsId)
		l.Info("Created DHCP options", "id", optID)
	} else {
		l.Info("Found existing DHCP options", "id", optID)
	}
	_, err = client.AssociateDhcpOptions(ctx, &ec2.AssociateDhcpOptionsInput{
		DhcpOptionsId: aws.String(optID),
		VpcId:         aws.String(vpcID),
	})
	if err != nil {
		return fmt.Errorf("cannot associate dhcp-options to VPC: %w", err)
	}
	l.Info("Associated DHCP options with VPC", "vpc", vpcID, "dhcp options", optID)
	return nil
}

func (o *CreateInfraOptions) existingDHCPOptions(ctx context.Context, client awsapi.EC2API) (string, error) {
	var optID string
	result, err := client.DescribeDhcpOptions(ctx, &ec2.DescribeDhcpOptionsInput{Filters: o.ec2Filters("")})
	if err != nil {
		return "", fmt.Errorf("cannot list dhcp options: %w", err)
	}
	for _, opt := range result.DhcpOptions {
		optID = aws.ToString(opt.DhcpOptionsId)
		break
	}
	return optID, nil
}

func (o *CreateInfraOptions) CreatePrivateSubnet(ctx context.Context, l logr.Logger, client awsapi.EC2API, vpcID string, zone string, cidr string) (string, error) {
	karpenterDiscoveryTag := []ec2types.Tag{
		{
			Key:   ptr.To("karpenter.sh/discovery"),
			Value: ptr.To(o.InfraID),
		},
	}
	return o.CreateSubnet(ctx, l, client, vpcID, zone, cidr, fmt.Sprintf("%s-private-%s", o.InfraID, zone), tagNameSubnetInternalELB, karpenterDiscoveryTag)
}

func (o *CreateInfraOptions) CreatePublicSubnet(ctx context.Context, l logr.Logger, client awsapi.EC2API, vpcID string, zone string, cidr string) (string, error) {
	karpenterDiscoveryTag := []ec2types.Tag{}
	if o.PublicOnly {
		karpenterDiscoveryTag = []ec2types.Tag{
			{
				Key:   ptr.To("karpenter.sh/discovery"),
				Value: ptr.To(o.InfraID),
			},
		}
	}
	return o.CreateSubnet(ctx, l, client, vpcID, zone, cidr, fmt.Sprintf("%s-public-%s", o.InfraID, zone), tagNameSubnetPublicELB, karpenterDiscoveryTag)
}

func (o *CreateInfraOptions) CreateSubnet(ctx context.Context, l logr.Logger, client awsapi.EC2API, vpcID, zone, cidr, name, scopeTag string, additionalTags []ec2types.Tag) (string, error) {
	subnetID, err := o.existingSubnet(ctx, client, name)
	if err != nil {
		return "", err
	}
	if len(subnetID) > 0 {
		l.Info("Found existing subnet", "name", name, "id", subnetID)
		return subnetID, nil
	}

	tagSpec := o.ec2TagSpecifications("subnet", name)
	tagSpec[0].Tags = append(tagSpec[0].Tags, ec2types.Tag{
		Key:   aws.String(scopeTag),
		Value: aws.String("1"),
	})
	if additionalTags != nil {
		tagSpec[0].Tags = append(tagSpec[0].Tags, additionalTags...)
	}

	result, err := client.CreateSubnet(ctx, &ec2.CreateSubnetInput{
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
		subnetResult, err = client.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{
			SubnetIds: []string{aws.ToString(result.Subnet.SubnetId)},
		})
		if err != nil || len(subnetResult.Subnets) == 0 {
			return fmt.Errorf("not found yet")
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("cannot find subnet that was just created (%s)", aws.ToString(result.Subnet.SubnetId))
	}
	subnetID = aws.ToString(result.Subnet.SubnetId)
	l.Info("Created subnet", "name", name, "id", subnetID)
	return subnetID, nil
}

func (o *CreateInfraOptions) existingSubnet(ctx context.Context, client awsapi.EC2API, name string) (string, error) {
	var subnetID string
	result, err := client.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{Filters: o.ec2Filters(name)})
	if err != nil {
		return "", fmt.Errorf("cannot list subnets: %w", err)
	}
	for _, subnet := range result.Subnets {
		subnetID = aws.ToString(subnet.SubnetId)
		break
	}
	return subnetID, nil
}

func (o *CreateInfraOptions) CreateInternetGateway(ctx context.Context, l logr.Logger, client awsapi.EC2API, vpcID string) (string, error) {
	gatewayName := fmt.Sprintf("%s-igw", o.InfraID)
	igw, err := o.existingInternetGateway(ctx, client, gatewayName)
	if err != nil {
		return "", err
	}
	if igw == nil {
		result, err := client.CreateInternetGateway(ctx, &ec2.CreateInternetGatewayInput{
			TagSpecifications: o.ec2TagSpecifications("internet-gateway", fmt.Sprintf("%s-igw", o.InfraID)),
		})
		if err != nil {
			return "", fmt.Errorf("cannot create internet gateway: %w", err)
		}
		igw = result.InternetGateway
		l.Info("Created internet gateway", "id", aws.ToString(igw.InternetGatewayId))
	} else {
		l.Info("Found existing internet gateway", "id", aws.ToString(igw.InternetGatewayId))
	}
	attached := false
	for _, attachment := range igw.Attachments {
		if aws.ToString(attachment.VpcId) == vpcID {
			attached = true
			break
		}
	}
	if !attached {
		_, err = client.AttachInternetGateway(ctx, &ec2.AttachInternetGatewayInput{
			InternetGatewayId: igw.InternetGatewayId,
			VpcId:             aws.String(vpcID),
		})
		if err != nil {
			return "", fmt.Errorf("cannot attach internet gateway to vpc: %w", err)
		}
		l.Info("Attached internet gateway to VPC", "internet gateway", aws.ToString(igw.InternetGatewayId), "vpc", vpcID)
	}
	return aws.ToString(igw.InternetGatewayId), nil
}

func (o *CreateInfraOptions) existingInternetGateway(ctx context.Context, client awsapi.EC2API, name string) (*ec2types.InternetGateway, error) {
	result, err := client.DescribeInternetGateways(ctx, &ec2.DescribeInternetGatewaysInput{Filters: o.ec2Filters(name)})
	if err != nil {
		return nil, fmt.Errorf("cannot list internet gateways: %w", err)
	}
	for i := range result.InternetGateways {
		return &result.InternetGateways[i], nil
	}
	return nil, nil
}

func (o *CreateInfraOptions) CreateNATGateway(ctx context.Context, l logr.Logger, client awsapi.EC2API, publicSubnetID, availabilityZone string) (string, error) {
	natGatewayName := fmt.Sprintf("%s-nat-%s", o.InfraID, availabilityZone)
	natGateway, err := o.existingNATGateway(ctx, client, natGatewayName)
	if err != nil {
		return "", fmt.Errorf("cannot check for existing NAT gateway: %w", err)
	}
	if natGateway != nil {
		l.Info("Found existing NAT gateway", "id", aws.ToString(natGateway.NatGatewayId))
		return aws.ToString(natGateway.NatGatewayId), nil
	}

	eipResult, err := client.AllocateAddress(ctx, &ec2.AllocateAddressInput{
		Domain: ec2types.DomainTypeVpc,
	})
	if err != nil {
		return "", fmt.Errorf("cannot allocate EIP for NAT gateway: %w", err)
	}
	allocationID := aws.ToString(eipResult.AllocationId)
	l.Info("Created elastic IP for NAT gateway", "id", allocationID)

	// NOTE: there's a potential to leak EIP addresses if the following tag operation fails, since we have no way of
	// recognizing the EIP as belonging to the cluster
	isRetriable := func(err error) bool {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			return strings.EqualFold(apiErr.ErrorCode(), invalidElasticIPNotFound)
		}
		return false
	}
	err = retry.OnError(retryBackoff, isRetriable, func() error {
		_, err = client.CreateTags(ctx, &ec2.CreateTagsInput{
			Resources: []string{allocationID},
			Tags:      append(ec2Tags(o.InfraID, fmt.Sprintf("%s-eip-%s", o.InfraID, availabilityZone)), o.additionalEC2Tags...),
		})
		return err
	})
	if err != nil {
		return "", fmt.Errorf("cannot tag NAT gateway EIP: %w", err)
	}

	isNATGatewayRetriable := func(err error) bool {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			return strings.EqualFold(apiErr.ErrorCode(), invalidSubnet) ||
				strings.EqualFold(apiErr.ErrorCode(), invalidElasticIPNotFound)
		}
		return false
	}
	err = retry.OnError(retryBackoff, isNATGatewayRetriable, func() error {
		gatewayResult, err := client.CreateNatGateway(ctx, &ec2.CreateNatGatewayInput{
			AllocationId:      aws.String(allocationID),
			SubnetId:          aws.String(publicSubnetID),
			TagSpecifications: o.ec2TagSpecifications("natgateway", natGatewayName),
		})
		if err != nil {
			return err
		}
		natGateway = gatewayResult.NatGateway
		l.Info("Created NAT gateway", "id", aws.ToString(natGateway.NatGatewayId))
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("cannot create NAT gateway: %w", err)
	}

	natGatewayID := aws.ToString(natGateway.NatGatewayId)
	return natGatewayID, nil
}

func (o *CreateInfraOptions) existingNATGateway(ctx context.Context, client awsapi.EC2API, name string) (*ec2types.NatGateway, error) {
	result, err := client.DescribeNatGateways(ctx, &ec2.DescribeNatGatewaysInput{Filter: o.ec2Filters(name)})
	if err != nil {
		return nil, fmt.Errorf("cannot list NAT gateways: %w", err)
	}
	for i, gateway := range result.NatGateways {
		state := string(gateway.State)
		if state == "deleted" || state == "deleting" || state == "failed" {
			continue
		}
		return &result.NatGateways[i], nil
	}
	return nil, nil
}

func (o *CreateInfraOptions) CreatePrivateRouteTable(ctx context.Context, l logr.Logger, client awsapi.EC2API, vpcID, natGatewayID, subnetID, zone string) (string, error) {
	tableName := fmt.Sprintf("%s-private-%s", o.InfraID, zone)
	routeTable, err := o.existingRouteTable(ctx, l, client, tableName)
	if err != nil {
		return "", err
	}
	if routeTable == nil {
		routeTable, err = o.createRouteTable(ctx, l, client, vpcID, tableName)
		if err != nil {
			return "", err
		}
	}

	// Everything below this is only needed if direct internet access is used
	if o.EnableProxy || o.EnableSecureProxy {
		return aws.ToString(routeTable.RouteTableId), nil
	}

	if !o.hasNATGatewayRoute(routeTable, natGatewayID) {
		isRetriable := func(err error) bool {
			var apiErr smithy.APIError
			if errors.As(err, &apiErr) {
				return strings.EqualFold(apiErr.ErrorCode(), invalidNATGatewayError)
			}
			return false
		}
		err = retry.OnError(retryBackoff, isRetriable, func() error {
			_, err = client.CreateRoute(ctx, &ec2.CreateRouteInput{
				RouteTableId:         routeTable.RouteTableId,
				NatGatewayId:         aws.String(natGatewayID),
				DestinationCidrBlock: aws.String("0.0.0.0/0"),
			})
			return err
		})
		if err != nil {
			return "", fmt.Errorf("cannot create nat gateway route in private route table: %w", err)
		}
		l.Info("Created route to NAT gateway", "route table", aws.ToString(routeTable.RouteTableId), "nat gateway", natGatewayID)
	} else {
		l.Info("Found existing route to NAT gateway", "route table", aws.ToString(routeTable.RouteTableId), "nat gateway", natGatewayID)
	}
	if !o.hasAssociatedSubnet(routeTable, subnetID) {
		_, err = client.AssociateRouteTable(ctx, &ec2.AssociateRouteTableInput{
			RouteTableId: routeTable.RouteTableId,
			SubnetId:     aws.String(subnetID),
		})
		if err != nil {
			return "", fmt.Errorf("cannot associate private route table with subnet: %w", err)
		}
		l.Info("Associated subnet with route table", "route table", aws.ToString(routeTable.RouteTableId), "subnet", subnetID)
	} else {
		l.Info("Subnet already associated with route table", "route table", aws.ToString(routeTable.RouteTableId), "subnet", subnetID)
	}
	return aws.ToString(routeTable.RouteTableId), nil
}

func (o *CreateInfraOptions) CreatePublicRouteTable(ctx context.Context, l logr.Logger, client awsapi.EC2API, vpcID, igwID string, subnetIDs []string) (string, error) {
	tableName := fmt.Sprintf("%s-public", o.InfraID)
	routeTable, err := o.existingRouteTable(ctx, l, client, tableName)
	if err != nil {
		return "", err
	}
	if routeTable == nil {
		routeTable, err = o.createRouteTable(ctx, l, client, vpcID, tableName)
		if err != nil {
			return "", err
		}
	}
	tableID := aws.ToString(routeTable.RouteTableId)
	// Replace the VPC's main route table
	routeTableInfo, err := client.DescribeRouteTables(ctx, &ec2.DescribeRouteTablesInput{
		Filters: []ec2types.Filter{
			{
				Name:   aws.String("vpc-id"),
				Values: []string{vpcID},
			},
			{
				Name:   aws.String("association.main"),
				Values: []string{"true"},
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
	if aws.ToString(routeTableInfo.RouteTables[0].RouteTableId) != tableID {
		var associationID string
		for _, assoc := range routeTableInfo.RouteTables[0].Associations {
			if aws.ToBool(assoc.Main) {
				associationID = aws.ToString(assoc.RouteTableAssociationId)
				break
			}
		}
		_, err = client.ReplaceRouteTableAssociation(ctx, &ec2.ReplaceRouteTableAssociationInput{
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
		_, err = client.CreateRoute(ctx, &ec2.CreateRouteInput{
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
			_, err = client.AssociateRouteTable(ctx, &ec2.AssociateRouteTableInput{
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

func (o *CreateInfraOptions) createRouteTable(ctx context.Context, l logr.Logger, client awsapi.EC2API, vpcID, name string) (*ec2types.RouteTable, error) {
	result, err := client.CreateRouteTable(ctx, &ec2.CreateRouteTableInput{
		VpcId:             aws.String(vpcID),
		TagSpecifications: o.ec2TagSpecifications("route-table", name),
	})
	if err != nil {
		return nil, fmt.Errorf("cannot create route table: %w", err)
	}
	l.Info("Created route table", "name", name, "id", aws.ToString(result.RouteTable.RouteTableId))
	return result.RouteTable, nil
}

func (o *CreateInfraOptions) existingRouteTable(ctx context.Context, l logr.Logger, client awsapi.EC2API, name string) (*ec2types.RouteTable, error) {
	result, err := client.DescribeRouteTables(ctx, &ec2.DescribeRouteTablesInput{Filters: o.ec2Filters(name)})
	if err != nil {
		return nil, fmt.Errorf("cannot list route tables: %w", err)
	}
	if len(result.RouteTables) > 0 {
		l.Info("Found existing route table", "name", name, "id", aws.ToString(result.RouteTables[0].RouteTableId))
		return &result.RouteTables[0], nil
	}
	return nil, nil
}

func (o *CreateInfraOptions) hasNATGatewayRoute(table *ec2types.RouteTable, natGatewayID string) bool {
	for _, route := range table.Routes {
		if aws.ToString(route.NatGatewayId) == natGatewayID &&
			aws.ToString(route.DestinationCidrBlock) == "0.0.0.0/0" {
			return true
		}
	}
	return false
}

func (o *CreateInfraOptions) hasInternetGatewayRoute(table *ec2types.RouteTable, igwID string) bool {
	for _, route := range table.Routes {
		if aws.ToString(route.GatewayId) == igwID &&
			aws.ToString(route.DestinationCidrBlock) == "0.0.0.0/0" {
			return true
		}
	}
	return false
}

func (o *CreateInfraOptions) hasAssociatedSubnet(table *ec2types.RouteTable, subnetID string) bool {
	for _, assoc := range table.Associations {
		if aws.ToString(assoc.SubnetId) == subnetID {
			return true
		}
	}
	return false
}

func (o *CreateInfraOptions) ec2TagSpecifications(resourceType, name string) []ec2types.TagSpecification {
	return []ec2types.TagSpecification{
		{
			ResourceType: ec2types.ResourceType(resourceType),
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
		o.additionalEC2Tags = append(o.additionalEC2Tags, ec2types.Tag{
			Key:   aws.String(k),
			Value: aws.String(v),
		})
	}
	return nil
}

func (o *CreateInfraOptions) ec2Filters(name string) []ec2types.Filter {
	filters := []ec2types.Filter{
		{
			Name:   aws.String(fmt.Sprintf("tag:%s", clusterTag(o.InfraID))),
			Values: []string{clusterTagValue},
		},
	}
	if len(name) > 0 {
		filters = append(filters, ec2types.Filter{
			Name:   aws.String("tag:Name"),
			Values: []string{name},
		})
	}
	return filters
}

func clusterTag(infraID string) string {
	return fmt.Sprintf("kubernetes.io/cluster/%s", infraID)
}

func ec2Tags(infraID, name string) []ec2types.Tag {
	tags := []ec2types.Tag{
		{
			Key:   aws.String(clusterTag(infraID)),
			Value: aws.String(clusterTagValue),
		},
	}
	if len(name) > 0 {
		tags = append(tags, ec2types.Tag{
			Key:   aws.String("Name"),
			Value: aws.String(name),
		})
	}
	return tags

}
