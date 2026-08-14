package main

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	elbv2 "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbv2types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	route53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
)

// trackingEC2 wraps mockEC2 and records which resources are deleted.
type trackingEC2 struct {
	natGateways []ec2types.NatGateway
	addresses   []ec2types.Address
	sgGroups    []ec2types.SecurityGroup
	subnets     []ec2types.Subnet
	endpoints   []ec2types.VpcEndpoint
	igws        []ec2types.InternetGateway
	rtbs        []ec2types.RouteTable
	enis        []ec2types.NetworkInterface

	releasedEIPs    []string
	deletedNATs     []string
	deletedSGs      []string
	deletedSubnets  []string
	deletedVPCEs    []string
	deletedIGWs     []string
	deletedRTBs     []string
	deletedENIs     []string
	deletedVPCs     []string
	deletedVPCESvcs []string
}

func (t *trackingEC2) DescribeVpcs(_ context.Context, _ *ec2.DescribeVpcsInput, _ ...func(*ec2.Options)) (*ec2.DescribeVpcsOutput, error) {
	return &ec2.DescribeVpcsOutput{}, nil
}
func (t *trackingEC2) DescribeInstances(_ context.Context, _ *ec2.DescribeInstancesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	return &ec2.DescribeInstancesOutput{}, nil
}
func (t *trackingEC2) DescribeVpcEndpoints(_ context.Context, _ *ec2.DescribeVpcEndpointsInput, _ ...func(*ec2.Options)) (*ec2.DescribeVpcEndpointsOutput, error) {
	return &ec2.DescribeVpcEndpointsOutput{VpcEndpoints: t.endpoints}, nil
}
func (t *trackingEC2) DescribeNatGateways(_ context.Context, _ *ec2.DescribeNatGatewaysInput, _ ...func(*ec2.Options)) (*ec2.DescribeNatGatewaysOutput, error) {
	return &ec2.DescribeNatGatewaysOutput{NatGateways: t.natGateways}, nil
}
func (t *trackingEC2) DescribeInternetGateways(_ context.Context, _ *ec2.DescribeInternetGatewaysInput, _ ...func(*ec2.Options)) (*ec2.DescribeInternetGatewaysOutput, error) {
	return &ec2.DescribeInternetGatewaysOutput{InternetGateways: t.igws}, nil
}
func (t *trackingEC2) DescribeSubnets(_ context.Context, _ *ec2.DescribeSubnetsInput, _ ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error) {
	return &ec2.DescribeSubnetsOutput{Subnets: t.subnets}, nil
}
func (t *trackingEC2) DescribeSecurityGroups(_ context.Context, _ *ec2.DescribeSecurityGroupsInput, _ ...func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error) {
	return &ec2.DescribeSecurityGroupsOutput{SecurityGroups: t.sgGroups}, nil
}
func (t *trackingEC2) DescribeRouteTables(_ context.Context, _ *ec2.DescribeRouteTablesInput, _ ...func(*ec2.Options)) (*ec2.DescribeRouteTablesOutput, error) {
	return &ec2.DescribeRouteTablesOutput{RouteTables: t.rtbs}, nil
}
func (t *trackingEC2) DescribeNetworkInterfaces(_ context.Context, _ *ec2.DescribeNetworkInterfacesInput, _ ...func(*ec2.Options)) (*ec2.DescribeNetworkInterfacesOutput, error) {
	return &ec2.DescribeNetworkInterfacesOutput{NetworkInterfaces: t.enis}, nil
}
func (t *trackingEC2) DescribeAddresses(_ context.Context, input *ec2.DescribeAddressesInput, _ ...func(*ec2.Options)) (*ec2.DescribeAddressesOutput, error) {
	var matched []ec2types.Address
	if len(input.AllocationIds) > 0 {
		for _, addr := range t.addresses {
			for _, id := range input.AllocationIds {
				if aws.ToString(addr.AllocationId) == id {
					matched = append(matched, addr)
				}
			}
		}
	} else {
		matched = t.addresses
	}
	return &ec2.DescribeAddressesOutput{Addresses: matched}, nil
}
func (t *trackingEC2) DescribeVpcEndpointServiceConfigurations(_ context.Context, _ *ec2.DescribeVpcEndpointServiceConfigurationsInput, _ ...func(*ec2.Options)) (*ec2.DescribeVpcEndpointServiceConfigurationsOutput, error) {
	return &ec2.DescribeVpcEndpointServiceConfigurationsOutput{}, nil
}

func (t *trackingEC2) ReleaseAddress(_ context.Context, input *ec2.ReleaseAddressInput, _ ...func(*ec2.Options)) (*ec2.ReleaseAddressOutput, error) {
	t.releasedEIPs = append(t.releasedEIPs, aws.ToString(input.AllocationId))
	return &ec2.ReleaseAddressOutput{}, nil
}
func (t *trackingEC2) DeleteNatGateway(_ context.Context, input *ec2.DeleteNatGatewayInput, _ ...func(*ec2.Options)) (*ec2.DeleteNatGatewayOutput, error) {
	t.deletedNATs = append(t.deletedNATs, aws.ToString(input.NatGatewayId))
	return &ec2.DeleteNatGatewayOutput{}, nil
}
func (t *trackingEC2) DeleteSecurityGroup(_ context.Context, input *ec2.DeleteSecurityGroupInput, _ ...func(*ec2.Options)) (*ec2.DeleteSecurityGroupOutput, error) {
	t.deletedSGs = append(t.deletedSGs, aws.ToString(input.GroupId))
	return &ec2.DeleteSecurityGroupOutput{}, nil
}
func (t *trackingEC2) DeleteSubnet(_ context.Context, input *ec2.DeleteSubnetInput, _ ...func(*ec2.Options)) (*ec2.DeleteSubnetOutput, error) {
	t.deletedSubnets = append(t.deletedSubnets, aws.ToString(input.SubnetId))
	return &ec2.DeleteSubnetOutput{}, nil
}
func (t *trackingEC2) DeleteVpcEndpoints(_ context.Context, input *ec2.DeleteVpcEndpointsInput, _ ...func(*ec2.Options)) (*ec2.DeleteVpcEndpointsOutput, error) {
	t.deletedVPCEs = append(t.deletedVPCEs, input.VpcEndpointIds...)
	return &ec2.DeleteVpcEndpointsOutput{}, nil
}
func (t *trackingEC2) DeleteVpcEndpointServiceConfigurations(_ context.Context, input *ec2.DeleteVpcEndpointServiceConfigurationsInput, _ ...func(*ec2.Options)) (*ec2.DeleteVpcEndpointServiceConfigurationsOutput, error) {
	t.deletedVPCESvcs = append(t.deletedVPCESvcs, input.ServiceIds...)
	return &ec2.DeleteVpcEndpointServiceConfigurationsOutput{}, nil
}
func (t *trackingEC2) DetachInternetGateway(_ context.Context, _ *ec2.DetachInternetGatewayInput, _ ...func(*ec2.Options)) (*ec2.DetachInternetGatewayOutput, error) {
	return &ec2.DetachInternetGatewayOutput{}, nil
}
func (t *trackingEC2) DeleteInternetGateway(_ context.Context, input *ec2.DeleteInternetGatewayInput, _ ...func(*ec2.Options)) (*ec2.DeleteInternetGatewayOutput, error) {
	t.deletedIGWs = append(t.deletedIGWs, aws.ToString(input.InternetGatewayId))
	return &ec2.DeleteInternetGatewayOutput{}, nil
}
func (t *trackingEC2) DisassociateRouteTable(_ context.Context, _ *ec2.DisassociateRouteTableInput, _ ...func(*ec2.Options)) (*ec2.DisassociateRouteTableOutput, error) {
	return &ec2.DisassociateRouteTableOutput{}, nil
}
func (t *trackingEC2) DeleteRouteTable(_ context.Context, input *ec2.DeleteRouteTableInput, _ ...func(*ec2.Options)) (*ec2.DeleteRouteTableOutput, error) {
	t.deletedRTBs = append(t.deletedRTBs, aws.ToString(input.RouteTableId))
	return &ec2.DeleteRouteTableOutput{}, nil
}
func (t *trackingEC2) DetachNetworkInterface(_ context.Context, _ *ec2.DetachNetworkInterfaceInput, _ ...func(*ec2.Options)) (*ec2.DetachNetworkInterfaceOutput, error) {
	return &ec2.DetachNetworkInterfaceOutput{}, nil
}
func (t *trackingEC2) DeleteNetworkInterface(_ context.Context, input *ec2.DeleteNetworkInterfaceInput, _ ...func(*ec2.Options)) (*ec2.DeleteNetworkInterfaceOutput, error) {
	t.deletedENIs = append(t.deletedENIs, aws.ToString(input.NetworkInterfaceId))
	return &ec2.DeleteNetworkInterfaceOutput{}, nil
}
func (t *trackingEC2) DeleteVpc(_ context.Context, input *ec2.DeleteVpcInput, _ ...func(*ec2.Options)) (*ec2.DeleteVpcOutput, error) {
	t.deletedVPCs = append(t.deletedVPCs, aws.ToString(input.VpcId))
	return &ec2.DeleteVpcOutput{}, nil
}
func (t *trackingEC2) RevokeSecurityGroupIngress(_ context.Context, _ *ec2.RevokeSecurityGroupIngressInput, _ ...func(*ec2.Options)) (*ec2.RevokeSecurityGroupIngressOutput, error) {
	return &ec2.RevokeSecurityGroupIngressOutput{}, nil
}
func (t *trackingEC2) RevokeSecurityGroupEgress(_ context.Context, _ *ec2.RevokeSecurityGroupEgressInput, _ ...func(*ec2.Options)) (*ec2.RevokeSecurityGroupEgressOutput, error) {
	return &ec2.RevokeSecurityGroupEgressOutput{}, nil
}

// trackingELBv2 records deleted load balancers.
type trackingELBv2 struct {
	loadBalancers []elbv2types.LoadBalancer
	deletedLBs    []string
}

func (t *trackingELBv2) DescribeLoadBalancers(_ context.Context, _ *elbv2.DescribeLoadBalancersInput, _ ...func(*elbv2.Options)) (*elbv2.DescribeLoadBalancersOutput, error) {
	return &elbv2.DescribeLoadBalancersOutput{LoadBalancers: t.loadBalancers}, nil
}
func (t *trackingELBv2) DeleteLoadBalancer(_ context.Context, input *elbv2.DeleteLoadBalancerInput, _ ...func(*elbv2.Options)) (*elbv2.DeleteLoadBalancerOutput, error) {
	t.deletedLBs = append(t.deletedLBs, aws.ToString(input.LoadBalancerArn))
	return &elbv2.DeleteLoadBalancerOutput{}, nil
}

// trackingRoute53 records deleted zones.
type trackingRoute53 struct {
	deletedZones []string
}

func (t *trackingRoute53) ListHostedZones(_ context.Context, _ *route53.ListHostedZonesInput, _ ...func(*route53.Options)) (*route53.ListHostedZonesOutput, error) {
	return &route53.ListHostedZonesOutput{}, nil
}
func (t *trackingRoute53) GetHostedZone(_ context.Context, _ *route53.GetHostedZoneInput, _ ...func(*route53.Options)) (*route53.GetHostedZoneOutput, error) {
	return &route53.GetHostedZoneOutput{}, nil
}
func (t *trackingRoute53) ListResourceRecordSets(_ context.Context, _ *route53.ListResourceRecordSetsInput, _ ...func(*route53.Options)) (*route53.ListResourceRecordSetsOutput, error) {
	return &route53.ListResourceRecordSetsOutput{}, nil
}
func (t *trackingRoute53) ChangeResourceRecordSets(_ context.Context, _ *route53.ChangeResourceRecordSetsInput, _ ...func(*route53.Options)) (*route53.ChangeResourceRecordSetsOutput, error) {
	return &route53.ChangeResourceRecordSetsOutput{}, nil
}
func (t *trackingRoute53) DeleteHostedZone(_ context.Context, input *route53.DeleteHostedZoneInput, _ ...func(*route53.Options)) (*route53.DeleteHostedZoneOutput, error) {
	t.deletedZones = append(t.deletedZones, aws.ToString(input.Id))
	return &route53.DeleteHostedZoneOutput{}, nil
}

// trackingIAM records deleted OIDC providers.
type trackingIAM struct {
	deletedOIDC []string
}

func (t *trackingIAM) ListOpenIDConnectProviders(_ context.Context, _ *iam.ListOpenIDConnectProvidersInput, _ ...func(*iam.Options)) (*iam.ListOpenIDConnectProvidersOutput, error) {
	return &iam.ListOpenIDConnectProvidersOutput{}, nil
}
func (t *trackingIAM) GetOpenIDConnectProvider(_ context.Context, _ *iam.GetOpenIDConnectProviderInput, _ ...func(*iam.Options)) (*iam.GetOpenIDConnectProviderOutput, error) {
	return &iam.GetOpenIDConnectProviderOutput{}, nil
}
func (t *trackingIAM) ListRoles(_ context.Context, _ *iam.ListRolesInput, _ ...func(*iam.Options)) (*iam.ListRolesOutput, error) {
	return &iam.ListRolesOutput{}, nil
}
func (t *trackingIAM) DeleteOpenIDConnectProvider(_ context.Context, input *iam.DeleteOpenIDConnectProviderInput, _ ...func(*iam.Options)) (*iam.DeleteOpenIDConnectProviderOutput, error) {
	t.deletedOIDC = append(t.deletedOIDC, aws.ToString(input.OpenIDConnectProviderArn))
	return &iam.DeleteOpenIDConnectProviderOutput{}, nil
}
func (t *trackingIAM) ListAttachedRolePolicies(_ context.Context, _ *iam.ListAttachedRolePoliciesInput, _ ...func(*iam.Options)) (*iam.ListAttachedRolePoliciesOutput, error) {
	return &iam.ListAttachedRolePoliciesOutput{}, nil
}
func (t *trackingIAM) DetachRolePolicy(_ context.Context, _ *iam.DetachRolePolicyInput, _ ...func(*iam.Options)) (*iam.DetachRolePolicyOutput, error) {
	return &iam.DetachRolePolicyOutput{}, nil
}
func (t *trackingIAM) ListRolePolicies(_ context.Context, _ *iam.ListRolePoliciesInput, _ ...func(*iam.Options)) (*iam.ListRolePoliciesOutput, error) {
	return &iam.ListRolePoliciesOutput{}, nil
}
func (t *trackingIAM) DeleteRolePolicy(_ context.Context, _ *iam.DeleteRolePolicyInput, _ ...func(*iam.Options)) (*iam.DeleteRolePolicyOutput, error) {
	return &iam.DeleteRolePolicyOutput{}, nil
}
func (t *trackingIAM) ListInstanceProfilesForRole(_ context.Context, _ *iam.ListInstanceProfilesForRoleInput, _ ...func(*iam.Options)) (*iam.ListInstanceProfilesForRoleOutput, error) {
	return &iam.ListInstanceProfilesForRoleOutput{}, nil
}
func (t *trackingIAM) RemoveRoleFromInstanceProfile(_ context.Context, _ *iam.RemoveRoleFromInstanceProfileInput, _ ...func(*iam.Options)) (*iam.RemoveRoleFromInstanceProfileOutput, error) {
	return &iam.RemoveRoleFromInstanceProfileOutput{}, nil
}
func (t *trackingIAM) DeleteInstanceProfile(_ context.Context, _ *iam.DeleteInstanceProfileInput, _ ...func(*iam.Options)) (*iam.DeleteInstanceProfileOutput, error) {
	return &iam.DeleteInstanceProfileOutput{}, nil
}
func (t *trackingIAM) DeleteRole(_ context.Context, _ *iam.DeleteRoleInput, _ ...func(*iam.Options)) (*iam.DeleteRoleOutput, error) {
	return &iam.DeleteRoleOutput{}, nil
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func TestDeleter_EIPRelease(t *testing.T) {
	tests := []struct {
		name            string
		natGateways     []ec2types.NatGateway
		allEIPs         []ec2types.Address
		wantReleased    []string
		wantNotReleased []string
	}{
		{
			name: "When VPC has a NAT with EIP and account has other orphaned EIPs, it should only release the NAT EIP",
			natGateways: []ec2types.NatGateway{
				{
					NatGatewayId: aws.String("nat-leaked"),
					VpcId:        aws.String("vpc-leaked"),
					NatGatewayAddresses: []ec2types.NatGatewayAddress{
						{AllocationId: aws.String("eip-nat-leaked"), PublicIp: aws.String("1.2.3.4")},
					},
				},
			},
			allEIPs: []ec2types.Address{
				{AllocationId: aws.String("eip-nat-leaked"), PublicIp: aws.String("1.2.3.4")},
				{AllocationId: aws.String("eip-ci2-nat"), PublicIp: aws.String("18.233.217.161")},
				{AllocationId: aws.String("eip-ci3-nat"), PublicIp: aws.String("100.25.75.227")},
				{AllocationId: aws.String("eip-orphan-1"), PublicIp: aws.String("100.27.126.159")},
				{AllocationId: aws.String("eip-orphan-2"), PublicIp: aws.String("100.29.246.172")},
			},
			wantReleased:    []string{"eip-nat-leaked"},
			wantNotReleased: []string{"eip-ci2-nat", "eip-ci3-nat", "eip-orphan-1", "eip-orphan-2"},
		},
		{
			name:        "When VPC has no NAT gateways, it should release zero EIPs regardless of orphaned EIPs in account",
			natGateways: []ec2types.NatGateway{},
			allEIPs: []ec2types.Address{
				{AllocationId: aws.String("eip-orphan-1"), PublicIp: aws.String("100.27.126.159")},
				{AllocationId: aws.String("eip-orphan-2"), PublicIp: aws.String("100.29.246.172")},
				{AllocationId: aws.String("eip-ci2"), PublicIp: aws.String("18.233.217.161")},
			},
			wantReleased:    nil,
			wantNotReleased: []string{"eip-orphan-1", "eip-orphan-2", "eip-ci2"},
		},
		{
			name: "When VPC has multiple NATs with EIPs, it should release only those EIPs",
			natGateways: []ec2types.NatGateway{
				{
					NatGatewayId: aws.String("nat-1"),
					VpcId:        aws.String("vpc-leaked"),
					NatGatewayAddresses: []ec2types.NatGatewayAddress{
						{AllocationId: aws.String("eip-nat-1"), PublicIp: aws.String("1.1.1.1")},
					},
				},
				{
					NatGatewayId: aws.String("nat-2"),
					VpcId:        aws.String("vpc-leaked"),
					NatGatewayAddresses: []ec2types.NatGatewayAddress{
						{AllocationId: aws.String("eip-nat-2"), PublicIp: aws.String("2.2.2.2")},
					},
				},
			},
			allEIPs: []ec2types.Address{
				{AllocationId: aws.String("eip-nat-1"), PublicIp: aws.String("1.1.1.1")},
				{AllocationId: aws.String("eip-nat-2"), PublicIp: aws.String("2.2.2.2")},
				{AllocationId: aws.String("eip-other"), PublicIp: aws.String("3.3.3.3")},
			},
			wantReleased:    []string{"eip-nat-1", "eip-nat-2"},
			wantNotReleased: []string{"eip-other"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ec2Client := &trackingEC2{
				natGateways: tt.natGateways,
				addresses:   tt.allEIPs,
			}

			deleter := &Deleter{
				EC2:     ec2Client,
				ELBv2:   &trackingELBv2{},
				Route53: &trackingRoute53{},
				IAM:     &trackingIAM{},
			}

			eips := deleter.collectNATGatewayEIPs(context.Background(), "vpc-leaked")
			deleter.releaseEIPs(context.Background(), eips)

			for _, want := range tt.wantReleased {
				if !contains(ec2Client.releasedEIPs, want) {
					t.Errorf("expected EIP %s to be released, but it was not", want)
				}
			}
			for _, notWant := range tt.wantNotReleased {
				if contains(ec2Client.releasedEIPs, notWant) {
					t.Errorf("EIP %s should NOT have been released, but it was", notWant)
				}
			}
		})
	}
}

func TestDeleter_DefaultSGNeverDeleted(t *testing.T) {
	tests := []struct {
		name       string
		sgs        []ec2types.SecurityGroup
		wantDelete []string
		wantSkip   []string
	}{
		{
			name: "When VPC has default and custom SGs, it should only delete the custom ones",
			sgs: []ec2types.SecurityGroup{
				{GroupId: aws.String("sg-default"), GroupName: aws.String("default")},
				{GroupId: aws.String("sg-custom-1"), GroupName: aws.String("my-cluster-sg")},
				{GroupId: aws.String("sg-custom-2"), GroupName: aws.String("my-cluster-vpce-sg")},
			},
			wantDelete: []string{"sg-custom-1", "sg-custom-2"},
			wantSkip:   []string{"sg-default"},
		},
		{
			name: "When VPC only has default SG, it should delete nothing",
			sgs: []ec2types.SecurityGroup{
				{GroupId: aws.String("sg-default"), GroupName: aws.String("default")},
			},
			wantDelete: nil,
			wantSkip:   []string{"sg-default"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ec2Client := &trackingEC2{sgGroups: tt.sgs}
			deleter := &Deleter{
				EC2:     ec2Client,
				ELBv2:   &trackingELBv2{},
				Route53: &trackingRoute53{},
				IAM:     &trackingIAM{},
			}

			deleter.deleteSecurityGroups(context.Background(), "vpc-test")

			for _, want := range tt.wantDelete {
				if !contains(ec2Client.deletedSGs, want) {
					t.Errorf("expected SG %s to be deleted, but it was not", want)
				}
			}
			for _, notWant := range tt.wantSkip {
				if contains(ec2Client.deletedSGs, notWant) {
					t.Errorf("default SG %s should NEVER be deleted, but it was", notWant)
				}
			}
		})
	}
}

func TestDeleter_ELBOnlyDeletesTargetVPC(t *testing.T) {
	t.Run("When account has ELBs in multiple VPCs, it should only delete ELBs in the target VPC", func(t *testing.T) {
		elbClient := &trackingELBv2{
			loadBalancers: []elbv2types.LoadBalancer{
				{LoadBalancerArn: aws.String("arn:elb:leaked-1"), LoadBalancerName: aws.String("leaked-elb"), VpcId: aws.String("vpc-leaked")},
				{LoadBalancerArn: aws.String("arn:elb:ci2-1"), LoadBalancerName: aws.String("ci2-elb"), VpcId: aws.String("vpc-ci2")},
				{LoadBalancerArn: aws.String("arn:elb:ci3-1"), LoadBalancerName: aws.String("ci3-elb"), VpcId: aws.String("vpc-ci3")},
			},
		}

		deleter := &Deleter{
			EC2:     &trackingEC2{},
			ELBv2:   elbClient,
			Route53: &trackingRoute53{},
			IAM:     &trackingIAM{},
		}

		deleter.deleteLoadBalancers(context.Background(), "vpc-leaked")

		if !contains(elbClient.deletedLBs, "arn:elb:leaked-1") {
			t.Error("expected leaked-elb to be deleted")
		}
		if contains(elbClient.deletedLBs, "arn:elb:ci2-1") {
			t.Error("ci2-elb should NOT have been deleted")
		}
		if contains(elbClient.deletedLBs, "arn:elb:ci3-1") {
			t.Error("ci3-elb should NOT have been deleted")
		}
	})
}

func TestDeleter_MainRTBNeverDeleted(t *testing.T) {
	t.Run("When VPC has main and non-main route tables, it should only delete non-main ones", func(t *testing.T) {
		ec2Client := &trackingEC2{
			rtbs: []ec2types.RouteTable{
				{
					RouteTableId: aws.String("rtb-main"),
					Associations: []ec2types.RouteTableAssociation{
						{Main: aws.Bool(true), RouteTableAssociationId: aws.String("rtbassoc-main")},
					},
				},
				{
					RouteTableId: aws.String("rtb-custom"),
					Associations: []ec2types.RouteTableAssociation{
						{Main: aws.Bool(false), RouteTableAssociationId: aws.String("rtbassoc-custom")},
					},
				},
			},
		}

		deleter := &Deleter{EC2: ec2Client, ELBv2: &trackingELBv2{}, Route53: &trackingRoute53{}, IAM: &trackingIAM{}}
		deleter.deleteRouteTables(context.Background(), "vpc-test")

		if contains(ec2Client.deletedRTBs, "rtb-main") {
			t.Error("main route table should NEVER be deleted")
		}
		if !contains(ec2Client.deletedRTBs, "rtb-custom") {
			t.Error("expected custom route table to be deleted")
		}
	})
}

func TestDeleter_FullCascadeOnlyTargetsOwnResources(t *testing.T) {
	t.Run("When deleting a leaked VPC, it should not touch resources from other VPCs", func(t *testing.T) {
		ec2Client := &trackingEC2{
			natGateways: []ec2types.NatGateway{
				{NatGatewayId: aws.String("nat-leaked"), VpcId: aws.String("vpc-leaked"),
					NatGatewayAddresses: []ec2types.NatGatewayAddress{
						{AllocationId: aws.String("eip-leaked"), PublicIp: aws.String("1.2.3.4")},
					}},
			},
			addresses: []ec2types.Address{
				{AllocationId: aws.String("eip-leaked"), PublicIp: aws.String("1.2.3.4")},
				{AllocationId: aws.String("eip-ci2"), PublicIp: aws.String("18.233.217.161")},
			},
			sgGroups: []ec2types.SecurityGroup{
				{GroupId: aws.String("sg-default"), GroupName: aws.String("default")},
				{GroupId: aws.String("sg-leaked"), GroupName: aws.String("leaked-sg")},
			},
			subnets: []ec2types.Subnet{
				{SubnetId: aws.String("subnet-leaked")},
			},
		}

		elbClient := &trackingELBv2{
			loadBalancers: []elbv2types.LoadBalancer{
				{LoadBalancerArn: aws.String("arn:elb:leaked"), LoadBalancerName: aws.String("leaked-elb"), VpcId: aws.String("vpc-leaked")},
				{LoadBalancerArn: aws.String("arn:elb:ci2"), LoadBalancerName: aws.String("ci2-elb"), VpcId: aws.String("vpc-ci2")},
			},
		}

		r53Client := &trackingRoute53{}
		iamClient := &trackingIAM{}

		deleter := &Deleter{EC2: ec2Client, ELBv2: elbClient, Route53: r53Client, IAM: iamClient}

		leaked := []InfraSet{{
			InfraID: "leaked-infra",
			Verdict: VerdictLeaked,
			VPCs:    []VPCInfo{{VPCID: "vpc-leaked", Name: "leaked-infra-vpc"}},
		}}

		if err := deleter.DeleteAll(context.Background(), leaked, ConfirmAll); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !contains(ec2Client.deletedVPCs, "vpc-leaked") {
			t.Error("expected vpc-leaked to be deleted")
		}
		if !contains(ec2Client.releasedEIPs, "eip-leaked") {
			t.Error("expected eip-leaked to be released")
		}
		if contains(ec2Client.releasedEIPs, "eip-ci2") {
			t.Error("eip-ci2 should NOT have been released")
		}
		if contains(elbClient.deletedLBs, "arn:elb:ci2") {
			t.Error("ci2 ELB should NOT have been deleted")
		}
		if contains(ec2Client.deletedSGs, "sg-default") {
			t.Error("default SG should NEVER be deleted")
		}
		if !contains(ec2Client.deletedSGs, "sg-leaked") {
			t.Error("expected sg-leaked to be deleted")
		}
		if !contains(ec2Client.deletedSubnets, "subnet-leaked") {
			t.Error("expected subnet-leaked to be deleted")
		}
	})
}

func TestDeleter_ProtectedInfraSetNeverDeleted(t *testing.T) {
	t.Run("When DeleteAll receives a PROTECTED infra set, it should skip it entirely", func(t *testing.T) {
		ec2Client := &trackingEC2{}
		deleter := &Deleter{EC2: ec2Client, ELBv2: &trackingELBv2{}, Route53: &trackingRoute53{}, IAM: &trackingIAM{}}

		sets := []InfraSet{
			{InfraID: "ci2", Verdict: VerdictProtected, VPCs: []VPCInfo{{VPCID: "vpc-ci2"}}},
			{InfraID: "ci3", Verdict: VerdictProtected, VPCs: []VPCInfo{{VPCID: "vpc-ci3"}}},
			{InfraID: "leaked", Verdict: VerdictLeaked, VPCs: []VPCInfo{{VPCID: "vpc-leaked"}}},
		}

		if err := deleter.DeleteAll(context.Background(), sets, ConfirmAll); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if contains(ec2Client.deletedVPCs, "vpc-ci2") {
			t.Error("PROTECTED vpc-ci2 should NEVER be deleted")
		}
		if contains(ec2Client.deletedVPCs, "vpc-ci3") {
			t.Error("PROTECTED vpc-ci3 should NEVER be deleted")
		}
		if !contains(ec2Client.deletedVPCs, "vpc-leaked") {
			t.Error("expected vpc-leaked to be deleted")
		}
	})
}

func TestDeleter_CollectNATGatewayEIPsReturnsCorrectIDs(t *testing.T) {
	tests := []struct {
		name     string
		nats     []ec2types.NatGateway
		wantEIPs []string
	}{
		{
			name: "When NAT has one EIP, it should return that AllocationID",
			nats: []ec2types.NatGateway{
				{NatGatewayId: aws.String("nat-1"), NatGatewayAddresses: []ec2types.NatGatewayAddress{
					{AllocationId: aws.String("eip-1")},
				}},
			},
			wantEIPs: []string{"eip-1"},
		},
		{
			name: "When multiple NATs have EIPs, it should return all AllocationIDs",
			nats: []ec2types.NatGateway{
				{NatGatewayId: aws.String("nat-1"), NatGatewayAddresses: []ec2types.NatGatewayAddress{{AllocationId: aws.String("eip-1")}}},
				{NatGatewayId: aws.String("nat-2"), NatGatewayAddresses: []ec2types.NatGatewayAddress{{AllocationId: aws.String("eip-2")}}},
			},
			wantEIPs: []string{"eip-1", "eip-2"},
		},
		{
			name:     "When no NATs exist, it should return empty",
			nats:     nil,
			wantEIPs: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ec2Client := &trackingEC2{natGateways: tt.nats}
			deleter := &Deleter{EC2: ec2Client}

			got := deleter.collectNATGatewayEIPs(context.Background(), "vpc-test")

			if len(got) != len(tt.wantEIPs) {
				t.Fatalf("got %d EIPs, want %d", len(got), len(tt.wantEIPs))
			}
			for i, want := range tt.wantEIPs {
				if got[i] != want {
					t.Errorf("EIP[%d] = %q, want %q", i, got[i], want)
				}
			}
		})
	}
}

func TestDeleter_ActiveInfraSetNeverDeleted(t *testing.T) {
	t.Run("When DeleteAll receives ACTIVE and TOO_YOUNG infra sets mixed with LEAKED, it should only delete LEAKED", func(t *testing.T) {
		ec2Client := &trackingEC2{}
		deleter := &Deleter{EC2: ec2Client, ELBv2: &trackingELBv2{}, Route53: &trackingRoute53{}, IAM: &trackingIAM{}}

		sets := []InfraSet{
			{InfraID: "active", Verdict: VerdictActive, VPCs: []VPCInfo{{VPCID: "vpc-active"}}},
			{InfraID: "young", Verdict: VerdictTooYoung, VPCs: []VPCInfo{{VPCID: "vpc-young"}}},
			{InfraID: "leaked", Verdict: VerdictLeaked, VPCs: []VPCInfo{{VPCID: "vpc-leaked"}}},
		}

		if err := deleter.DeleteAll(context.Background(), sets, ConfirmAll); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		for _, v := range []string{"vpc-active", "vpc-young"} {
			if contains(ec2Client.deletedVPCs, v) {
				t.Errorf("%s should NOT have been deleted", v)
			}
		}
		if !contains(ec2Client.deletedVPCs, "vpc-leaked") {
			t.Error("expected vpc-leaked to be deleted")
		}
	})
}

func TestDeleter_RealWorldScenario_CI2EIPSurvives(t *testing.T) {
	t.Run("When deleting leaked VPC while ci-2 NAT has an orphaned EIP in account, ci-2 EIP should not be touched", func(t *testing.T) {
		ec2Client := &trackingEC2{
			natGateways: []ec2types.NatGateway{},
			addresses: []ec2types.Address{
				{AllocationId: aws.String("eip-ci2-nat"), PublicIp: aws.String("18.233.217.161")},
				{AllocationId: aws.String("eip-ci3-nat"), PublicIp: aws.String("100.25.75.227")},
				{AllocationId: aws.String("eip-orphan"), PublicIp: aws.String("100.27.126.159")},
			},
		}

		deleter := &Deleter{EC2: ec2Client, ELBv2: &trackingELBv2{}, Route53: &trackingRoute53{}, IAM: &trackingIAM{}}

		leaked := []InfraSet{{
			InfraID: "leaked-no-nat",
			Verdict: VerdictLeaked,
			VPCs:    []VPCInfo{{VPCID: "vpc-leaked-no-nat", Name: "leaked-vpc"}},
		}}

		if err := deleter.DeleteAll(context.Background(), leaked, ConfirmAll); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(ec2Client.releasedEIPs) != 0 {
			t.Errorf("expected 0 EIPs released, got %d: %v", len(ec2Client.releasedEIPs), ec2Client.releasedEIPs)
		}

		for _, protected := range []string{"eip-ci2-nat", "eip-ci3-nat", "eip-orphan"} {
			if contains(ec2Client.releasedEIPs, protected) {
				t.Errorf("EIP %s should NEVER have been released — this is the bug that hit production", protected)
			}
		}
	})

	t.Run("When deleting leaked VPC that has its own NAT EIP while ci-2 NAT EIP exists, only the leaked NAT EIP should be released", func(t *testing.T) {
		ec2Client := &trackingEC2{
			natGateways: []ec2types.NatGateway{
				{
					NatGatewayId: aws.String("nat-leaked"),
					VpcId:        aws.String("vpc-leaked"),
					NatGatewayAddresses: []ec2types.NatGatewayAddress{
						{AllocationId: aws.String("eip-leaked-nat"), PublicIp: aws.String("5.5.5.5")},
					},
				},
			},
			addresses: []ec2types.Address{
				{AllocationId: aws.String("eip-leaked-nat"), PublicIp: aws.String("5.5.5.5")},
				{AllocationId: aws.String("eip-ci2-nat"), PublicIp: aws.String("18.233.217.161")},
				{AllocationId: aws.String("eip-ci3-nat"), PublicIp: aws.String("100.25.75.227")},
			},
		}

		deleter := &Deleter{EC2: ec2Client, ELBv2: &trackingELBv2{}, Route53: &trackingRoute53{}, IAM: &trackingIAM{}}

		leaked := []InfraSet{{
			InfraID: "leaked-with-nat",
			Verdict: VerdictLeaked,
			VPCs:    []VPCInfo{{VPCID: "vpc-leaked", Name: "leaked-vpc"}},
		}}

		if err := deleter.DeleteAll(context.Background(), leaked, ConfirmAll); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !contains(ec2Client.releasedEIPs, "eip-leaked-nat") {
			t.Error("expected eip-leaked-nat to be released")
		}
		if contains(ec2Client.releasedEIPs, "eip-ci2-nat") {
			t.Error("ci-2 NAT EIP should NEVER be released — this would break the management cluster")
		}
		if contains(ec2Client.releasedEIPs, "eip-ci3-nat") {
			t.Error("ci-3 NAT EIP should NEVER be released — this would break the management cluster")
		}
	})
}

func TestDeleter_DeleteIAMRole(t *testing.T) {
	t.Run("When IAM role has attached policies, inline policies, and instance profiles, it should clean all before deleting", func(t *testing.T) {
		iamClient := &trackingIAM{
			deletedOIDC: nil,
		}

		deleter := &Deleter{
			EC2:     &trackingEC2{},
			ELBv2:   &trackingELBv2{},
			Route53: &trackingRoute53{},
			IAM:     iamClient,
		}

		err := deleter.deleteIAMRole(context.Background(), "test-infra-cloud-controller")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestDeleter_DeleteHostedZone(t *testing.T) {
	t.Run("When hosted zone has NS, SOA, and A records, it should only delete the A record before deleting the zone", func(t *testing.T) {
		r53Client := &trackingRoute53WithRecords{
			records: []route53types.ResourceRecordSet{
				{Name: aws.String("zone.example.com."), Type: route53types.RRTypeNs},
				{Name: aws.String("zone.example.com."), Type: route53types.RRTypeSoa},
				{Name: aws.String("*.apps.zone.example.com."), Type: route53types.RRTypeA, ResourceRecords: []route53types.ResourceRecord{{Value: aws.String("1.2.3.4")}}},
			},
		}

		deleter := &Deleter{
			EC2:     &trackingEC2{},
			ELBv2:   &trackingELBv2{},
			Route53: r53Client,
			IAM:     &trackingIAM{},
		}

		err := deleter.deleteHostedZone(context.Background(), "Z1234", "zone.example.com")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !contains(r53Client.deletedZones, "Z1234") {
			t.Error("expected zone to be deleted")
		}
		if r53Client.changedRecordCount != 1 {
			t.Errorf("expected 1 record change (the A record), got %d", r53Client.changedRecordCount)
		}
	})
}

type trackingRoute53WithRecords struct {
	records            []route53types.ResourceRecordSet
	deletedZones       []string
	changedRecordCount int
}

func (t *trackingRoute53WithRecords) ListHostedZones(_ context.Context, _ *route53.ListHostedZonesInput, _ ...func(*route53.Options)) (*route53.ListHostedZonesOutput, error) {
	return &route53.ListHostedZonesOutput{}, nil
}
func (t *trackingRoute53WithRecords) GetHostedZone(_ context.Context, _ *route53.GetHostedZoneInput, _ ...func(*route53.Options)) (*route53.GetHostedZoneOutput, error) {
	return &route53.GetHostedZoneOutput{}, nil
}
func (t *trackingRoute53WithRecords) ListResourceRecordSets(_ context.Context, _ *route53.ListResourceRecordSetsInput, _ ...func(*route53.Options)) (*route53.ListResourceRecordSetsOutput, error) {
	return &route53.ListResourceRecordSetsOutput{ResourceRecordSets: t.records}, nil
}
func (t *trackingRoute53WithRecords) ChangeResourceRecordSets(_ context.Context, input *route53.ChangeResourceRecordSetsInput, _ ...func(*route53.Options)) (*route53.ChangeResourceRecordSetsOutput, error) {
	t.changedRecordCount = len(input.ChangeBatch.Changes)
	return &route53.ChangeResourceRecordSetsOutput{}, nil
}
func (t *trackingRoute53WithRecords) DeleteHostedZone(_ context.Context, input *route53.DeleteHostedZoneInput, _ ...func(*route53.Options)) (*route53.DeleteHostedZoneOutput, error) {
	t.deletedZones = append(t.deletedZones, aws.ToString(input.Id))
	return &route53.DeleteHostedZoneOutput{}, nil
}

func TestDeleter_VPCEndpointServicesOnlyDeletesMatchingInfraID(t *testing.T) {
	t.Run("When account has VPCE services from multiple infra sets, it should only delete services tagged with the target infraID", func(t *testing.T) {
		ec2Client := &trackingEC2WithVPCESvcs{
			trackingEC2: trackingEC2{},
			svcConfigs: []ec2types.ServiceConfiguration{
				{
					ServiceId: aws.String("vpce-svc-leaked"),
					Tags: []ec2types.Tag{
						{Key: aws.String("kubernetes.io/cluster/leaked-infra"), Value: aws.String("owned")},
					},
				},
				{
					ServiceId: aws.String("vpce-svc-ci2"),
					Tags: []ec2types.Tag{
						{Key: aws.String("kubernetes.io/cluster/ci2-infra"), Value: aws.String("owned")},
					},
				},
				{
					ServiceId: aws.String("vpce-svc-notag"),
					Tags:      []ec2types.Tag{},
				},
			},
		}

		deleter := &Deleter{EC2: ec2Client, ELBv2: &trackingELBv2{}, Route53: &trackingRoute53{}, IAM: &trackingIAM{}}
		deleter.deleteVPCEndpointServices(context.Background(), "leaked-infra")

		if !contains(ec2Client.deletedVPCESvcs, "vpce-svc-leaked") {
			t.Error("expected vpce-svc-leaked to be deleted")
		}
		if contains(ec2Client.deletedVPCESvcs, "vpce-svc-ci2") {
			t.Error("vpce-svc-ci2 should NOT have been deleted")
		}
		if contains(ec2Client.deletedVPCESvcs, "vpce-svc-notag") {
			t.Error("vpce-svc-notag should NOT have been deleted")
		}
	})
}

type trackingEC2WithVPCESvcs struct {
	trackingEC2
	svcConfigs []ec2types.ServiceConfiguration
}

func (t *trackingEC2WithVPCESvcs) DescribeVpcEndpointServiceConfigurations(_ context.Context, _ *ec2.DescribeVpcEndpointServiceConfigurationsInput, _ ...func(*ec2.Options)) (*ec2.DescribeVpcEndpointServiceConfigurationsOutput, error) {
	return &ec2.DescribeVpcEndpointServiceConfigurationsOutput{ServiceConfigurations: t.svcConfigs}, nil
}
