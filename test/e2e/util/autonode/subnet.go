package autonode

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	supportawsutil "github.com/openshift/hypershift/support/awsutil"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"

	"k8s.io/apimachinery/pkg/util/wait"
)

type subnetEC2 interface {
	DescribeVpcs(context.Context, *ec2.DescribeVpcsInput, ...func(*ec2.Options)) (*ec2.DescribeVpcsOutput, error)
	DescribeSubnets(context.Context, *ec2.DescribeSubnetsInput, ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error)
	DescribeRouteTables(context.Context, *ec2.DescribeRouteTablesInput, ...func(*ec2.Options)) (*ec2.DescribeRouteTablesOutput, error)
	CreateSubnet(context.Context, *ec2.CreateSubnetInput, ...func(*ec2.Options)) (*ec2.CreateSubnetOutput, error)
	AssociateRouteTable(context.Context, *ec2.AssociateRouteTableInput, ...func(*ec2.Options)) (*ec2.AssociateRouteTableOutput, error)
	DisassociateRouteTable(context.Context, *ec2.DisassociateRouteTableInput, ...func(*ec2.Options)) (*ec2.DisassociateRouteTableOutput, error)
	DeleteSubnet(context.Context, *ec2.DeleteSubnetInput, ...func(*ec2.Options)) (*ec2.DeleteSubnetOutput, error)
}

func createLocalZoneSubnet(ctx context.Context, client subnetEC2, vpc, zone, name, infraID, clusterName string) (string, func(context.Context) error, error) {
	noop := func(context.Context) error { return nil }
	vpcs, err := client.DescribeVpcs(ctx, &ec2.DescribeVpcsInput{VpcIds: []string{vpc}})
	if err != nil {
		return "", noop, fmt.Errorf("describe hosted cluster VPC: %w", err)
	}
	if len(vpcs.Vpcs) != 1 || aws.ToString(vpcs.Vpcs[0].VpcId) != vpc {
		return "", noop, fmt.Errorf("expected exactly the hosted cluster VPC")
	}
	var cidrs []string
	for _, association := range vpcs.Vpcs[0].CidrBlockAssociationSet {
		if association.CidrBlockState != nil && association.CidrBlockState.State == ec2types.VpcCidrBlockStateCodeAssociated {
			cidrs = append(cidrs, aws.ToString(association.CidrBlock))
		}
	}
	var occupied []string
	var token *string
	for {
		subnets, err := client.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{NextToken: token, Filters: []ec2types.Filter{{Name: aws.String("vpc-id"), Values: []string{vpc}}}})
		if err != nil {
			return "", noop, fmt.Errorf("list existing VPC subnets: %w", err)
		}
		for _, subnet := range subnets.Subnets {
			if aws.ToString(subnet.CidrBlock) != "" {
				occupied = append(occupied, aws.ToString(subnet.CidrBlock))
			}
		}
		token = subnets.NextToken
		if aws.ToString(token) == "" {
			break
		}
	}
	cidr, err := e2eutil.FindFreeSubnetCIDR(cidrs, occupied)
	if err != nil {
		return "", noop, err
	}
	routeTable, err := localZoneNATRouteTable(ctx, client, vpc)
	if err != nil {
		return "", noop, err
	}
	tags := map[string]string{"Name": name, supportawsutil.HypershiftInfraIDTagKey: infraID, supportawsutil.HypershiftClusterNameTagKey: clusterName}
	for key, value := range e2eutil.E2ETagsFromEnvironment() {
		tags[key] = value
	}
	var subnetTags []ec2types.Tag
	for key, value := range tags {
		subnetTags = append(subnetTags, ec2types.Tag{Key: aws.String(key), Value: aws.String(value)})
	}
	created, err := client.CreateSubnet(ctx, &ec2.CreateSubnetInput{VpcId: aws.String(vpc), AvailabilityZone: aws.String(zone), CidrBlock: aws.String(cidr), TagSpecifications: []ec2types.TagSpecification{{ResourceType: ec2types.ResourceTypeSubnet, Tags: subnetTags}}})
	if err != nil {
		return "", noop, fmt.Errorf("create Local Zone subnet (concurrent allocation conflicts fail rather than reuse another subnet): %w", err)
	}
	if created.Subnet == nil || aws.ToString(created.Subnet.SubnetId) == "" {
		return "", noop, fmt.Errorf("create Local Zone subnet returned no subnet ID")
	}
	subnetID := aws.ToString(created.Subnet.SubnetId)
	associationID := ""
	cleanup := func(ctx context.Context) error {
		return cleanupLocalZoneSubnet(ctx, client, subnetID, associationID)
	}
	if err := ec2.NewSubnetAvailableWaiter(client).Wait(ctx, &ec2.DescribeSubnetsInput{SubnetIds: []string{subnetID}}, 2*time.Minute); err != nil {
		return subnetID, cleanup, fmt.Errorf("wait for owned Local Zone subnet availability: %w", err)
	}
	association, err := client.AssociateRouteTable(ctx, &ec2.AssociateRouteTableInput{SubnetId: aws.String(subnetID), RouteTableId: aws.String(routeTable)})
	if err != nil {
		return subnetID, cleanup, fmt.Errorf("associate Local Zone subnet with existing NAT route table: %w", err)
	}
	associationID = aws.ToString(association.AssociationId)
	if associationID == "" {
		return subnetID, cleanup, fmt.Errorf("route table association returned no association ID")
	}
	return subnetID, cleanup, nil
}

func localZoneNATRouteTable(ctx context.Context, client subnetEC2, vpc string) (string, error) {
	var routeTables []string
	var token *string
	for {
		tables, err := client.DescribeRouteTables(ctx, &ec2.DescribeRouteTablesInput{NextToken: token, Filters: []ec2types.Filter{{Name: aws.String("vpc-id"), Values: []string{vpc}}}})
		if err != nil {
			return "", fmt.Errorf("describe VPC NAT route tables: %w", err)
		}
		for _, table := range tables.RouteTables {
			for _, route := range table.Routes {
				if aws.ToString(table.VpcId) == vpc && aws.ToString(route.DestinationCidrBlock) == "0.0.0.0/0" && aws.ToString(route.NatGatewayId) != "" && route.State == ec2types.RouteStateActive {
					routeTables = append(routeTables, aws.ToString(table.RouteTableId))
				}
			}
		}
		token = tables.NextToken
		if aws.ToString(token) == "" {
			break
		}
	}
	if len(routeTables) == 0 {
		return "", fmt.Errorf("hosted cluster VPC requires an active IPv4 default NAT route; no subnet created")
	}
	sort.Strings(routeTables)
	return routeTables[0], nil
}

func cleanupLocalZoneSubnet(ctx context.Context, client subnetEC2, subnetID, associationID string) error {
	if associationID != "" {
		if _, err := client.DisassociateRouteTable(ctx, &ec2.DisassociateRouteTableInput{AssociationId: aws.String(associationID)}); err != nil && !awsErrorCode(err, "InvalidAssociationID.NotFound") {
			return fmt.Errorf("disassociate owned Local Zone subnet route table: %w", err)
		}
	}
	return wait.PollUntilContextTimeout(ctx, 5*time.Second, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
		_, err := client.DeleteSubnet(ctx, &ec2.DeleteSubnetInput{SubnetId: aws.String(subnetID)})
		if awsErrorCode(err, "InvalidSubnetID.NotFound") {
			return true, nil
		}
		if err != nil {
			if awsErrorCode(err, "DependencyViolation") {
				return false, nil
			}
			return false, err
		}
		_, err = client.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{SubnetIds: []string{subnetID}})
		if awsErrorCode(err, "InvalidSubnetID.NotFound") {
			return true, nil
		}
		return false, err
	})
}

func awsErrorCode(err error, code string) bool {
	var apiErr smithy.APIError
	return errors.As(err, &apiErr) && apiErr.ErrorCode() == code
}
