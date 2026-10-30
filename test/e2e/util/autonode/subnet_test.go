package autonode

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"
)

type subnetEC2Stub struct {
	failure string
	created *ec2.CreateSubnetInput
	deleted bool
	events  []string
}

func (stub *subnetEC2Stub) DescribeVpcs(context.Context, *ec2.DescribeVpcsInput, ...func(*ec2.Options)) (*ec2.DescribeVpcsOutput, error) {
	return &ec2.DescribeVpcsOutput{Vpcs: []ec2types.Vpc{{VpcId: aws.String("vpc-test"), CidrBlockAssociationSet: []ec2types.VpcCidrBlockAssociation{{CidrBlock: aws.String("10.42.0.0/24"), CidrBlockState: &ec2types.VpcCidrBlockState{State: ec2types.VpcCidrBlockStateCodeAssociated}}}}}}, nil
}

func (stub *subnetEC2Stub) DescribeSubnets(ctx context.Context, input *ec2.DescribeSubnetsInput, _ ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(input.SubnetIds) > 0 {
		if stub.deleted {
			return nil, &smithy.GenericAPIError{Code: "InvalidSubnetID.NotFound"}
		}
		return &ec2.DescribeSubnetsOutput{Subnets: []ec2types.Subnet{{SubnetId: aws.String("subnet-local"), AvailabilityZone: aws.String("us-west-2-lax-1a"), VpcId: aws.String("vpc-test"), State: ec2types.SubnetStateAvailable}}}, nil
	}
	if input.NextToken == nil {
		return &ec2.DescribeSubnetsOutput{NextToken: aws.String("page2"), Subnets: []ec2types.Subnet{{CidrBlock: aws.String("10.42.0.0/28")}}}, nil
	}
	return &ec2.DescribeSubnetsOutput{Subnets: []ec2types.Subnet{{CidrBlock: aws.String("10.42.0.16/28")}}}, nil
}

func (stub *subnetEC2Stub) DescribeRouteTables(context.Context, *ec2.DescribeRouteTablesInput, ...func(*ec2.Options)) (*ec2.DescribeRouteTablesOutput, error) {
	if stub.failure == "routes" {
		return nil, fmt.Errorf("injected route describe failure")
	}
	state := ec2types.RouteStateActive
	if stub.failure == "nat" {
		state = ec2types.RouteStateBlackhole
	}
	return &ec2.DescribeRouteTablesOutput{RouteTables: []ec2types.RouteTable{{VpcId: aws.String("vpc-test"), RouteTableId: aws.String("rtb-private"), Routes: []ec2types.Route{{DestinationCidrBlock: aws.String("0.0.0.0/0"), NatGatewayId: aws.String("nat-test"), State: state}}}}}, nil
}

func (stub *subnetEC2Stub) CreateSubnet(_ context.Context, input *ec2.CreateSubnetInput, _ ...func(*ec2.Options)) (*ec2.CreateSubnetOutput, error) {
	stub.created = input
	stub.events = append(stub.events, "create subnet")
	if stub.failure == "conflict" {
		return nil, &smithy.GenericAPIError{Code: "InvalidSubnet.Conflict"}
	}
	return &ec2.CreateSubnetOutput{Subnet: &ec2types.Subnet{SubnetId: aws.String("subnet-local")}}, nil
}

func (stub *subnetEC2Stub) AssociateRouteTable(_ context.Context, input *ec2.AssociateRouteTableInput, _ ...func(*ec2.Options)) (*ec2.AssociateRouteTableOutput, error) {
	stub.events = append(stub.events, "associate")
	if stub.failure == "associate" {
		return nil, fmt.Errorf("injected association failure")
	}
	if aws.ToString(input.SubnetId) != "subnet-local" || aws.ToString(input.RouteTableId) != "rtb-private" {
		return nil, fmt.Errorf("unexpected association")
	}
	return &ec2.AssociateRouteTableOutput{AssociationId: aws.String("rtbassoc-owned")}, nil
}

func (stub *subnetEC2Stub) DisassociateRouteTable(ctx context.Context, input *ec2.DisassociateRouteTableInput, _ ...func(*ec2.Options)) (*ec2.DisassociateRouteTableOutput, error) {
	stub.events = append(stub.events, "disassociate")
	if aws.ToString(input.AssociationId) != "rtbassoc-owned" {
		return nil, fmt.Errorf("must only remove owned association")
	}
	return &ec2.DisassociateRouteTableOutput{}, ctx.Err()
}

func (stub *subnetEC2Stub) DeleteSubnet(ctx context.Context, input *ec2.DeleteSubnetInput, _ ...func(*ec2.Options)) (*ec2.DeleteSubnetOutput, error) {
	stub.events = append(stub.events, "delete subnet")
	if aws.ToString(input.SubnetId) != "subnet-local" {
		return nil, fmt.Errorf("must only remove owned subnet")
	}
	if stub.failure == "delete" {
		return nil, fmt.Errorf("injected delete failure")
	}
	stub.deleted = true
	return &ec2.DeleteSubnetOutput{}, ctx.Err()
}

func TestCreateLocalZoneSubnet(t *testing.T) {
	for _, testCase := range []struct {
		name, failure                    string
		wantSetupError, wantCleanupError bool
	}{
		{name: "When a subnet is needed, it should allocate across all subnet pages and associate NAT"},
		{name: "When no active NAT route exists, it should fail before creation", failure: "nat", wantSetupError: true},
		{name: "When route lookup fails, it should fail before creation", failure: "routes", wantSetupError: true},
		{name: "When a concurrent allocation wins, it should fail without touching the other subnet", failure: "conflict", wantSetupError: true},
		{name: "When association fails, it should return cleanup for the new subnet", failure: "associate", wantSetupError: true},
		{name: "When deletion fails, it should surface the cleanup failure", failure: "delete", wantCleanupError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			client := &subnetEC2Stub{failure: testCase.failure}
			ctx, cancel := context.WithCancel(context.Background())
			subnet, cleanup, err := createLocalZoneSubnet(ctx, client, "vpc-test", "us-west-2-lax-1a", "local-test", "infra-test", "cluster-test")
			cancel()
			if (err != nil) != testCase.wantSetupError {
				t.Fatalf("unexpected setup error: %v", err)
			}
			if err := cleanup(context.Background()); (err != nil) != testCase.wantCleanupError {
				t.Fatalf("unexpected cleanup error: %v", err)
			}
			if client.created != nil && (aws.ToString(client.created.CidrBlock) != "10.42.0.32/28" || aws.ToString(client.created.AvailabilityZone) != "us-west-2-lax-1a" || aws.ToString(client.created.VpcId) != "vpc-test") {
				t.Fatalf("wrong subnet request: %+v", client.created)
			}
			if testCase.failure == "conflict" && strings.Contains(strings.Join(client.events, ","), "delete") {
				t.Fatal("must not delete a subnet we did not create")
			}
			if (testCase.failure == "nat" || testCase.failure == "routes") && client.created != nil {
				t.Fatal("preflight failure must not create a subnet")
			}
			if !testCase.wantSetupError && subnet != "subnet-local" {
				t.Fatal("must return owned subnet")
			}
		})
	}
}

func TestAwsErrorCode(t *testing.T) {
	if !awsErrorCode(fmt.Errorf("wrapped: %w", &smithy.GenericAPIError{Code: "InvalidSubnetID.NotFound"}), "InvalidSubnetID.NotFound") || awsErrorCode(nil, "InvalidSubnetID.NotFound") {
		t.Fatal("must match wrapped AWS errors but not nil")
	}
}

func TestCleanupLocalZoneSubnet(t *testing.T) {
	for _, failure := range []string{"", "delete"} {
		t.Run("When cleanup encounters "+failure+", it should report the result", func(t *testing.T) {
			client := &subnetEC2Stub{failure: failure}
			err := cleanupLocalZoneSubnet(context.Background(), client, "subnet-local", "rtbassoc-owned")
			if (err != nil) != (failure != "") {
				t.Fatalf("unexpected cleanup result: %v", err)
			}
		})
	}
}

func TestLocalZoneNATRouteTable(t *testing.T) {
	for _, failure := range []string{"", "nat", "routes"} {
		t.Run("When route discovery encounters "+failure+", it should validate NAT availability", func(t *testing.T) {
			table, err := localZoneNATRouteTable(context.Background(), &subnetEC2Stub{failure: failure}, "vpc-test")
			if (err != nil) != (failure != "") || (err == nil && table != "rtb-private") {
				t.Fatalf("unexpected route selection: %s, %v", table, err)
			}
		})
	}
}
