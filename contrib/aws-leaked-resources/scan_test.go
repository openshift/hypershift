package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	elbv2 "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	route53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// mockEC2 implements EC2API for testing.
type mockEC2 struct {
	vpcs      []ec2types.Vpc
	instances []ec2types.Reservation

	vpcEndpoints     []ec2types.VpcEndpoint
	natGateways      []ec2types.NatGateway
	internetGateways []ec2types.InternetGateway
	subnets          []ec2types.Subnet
	securityGroups   []ec2types.SecurityGroup
	routeTables      []ec2types.RouteTable
	networkIfaces    []ec2types.NetworkInterface
}

func (m *mockEC2) DescribeVpcs(_ context.Context, _ *ec2.DescribeVpcsInput, _ ...func(*ec2.Options)) (*ec2.DescribeVpcsOutput, error) {
	return &ec2.DescribeVpcsOutput{Vpcs: m.vpcs}, nil
}

func (m *mockEC2) DescribeInstances(_ context.Context, input *ec2.DescribeInstancesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	return &ec2.DescribeInstancesOutput{Reservations: m.instances}, nil
}

func (m *mockEC2) DescribeVpcEndpoints(_ context.Context, _ *ec2.DescribeVpcEndpointsInput, _ ...func(*ec2.Options)) (*ec2.DescribeVpcEndpointsOutput, error) {
	return &ec2.DescribeVpcEndpointsOutput{VpcEndpoints: m.vpcEndpoints}, nil
}

func (m *mockEC2) DescribeNatGateways(_ context.Context, _ *ec2.DescribeNatGatewaysInput, _ ...func(*ec2.Options)) (*ec2.DescribeNatGatewaysOutput, error) {
	return &ec2.DescribeNatGatewaysOutput{NatGateways: m.natGateways}, nil
}

func (m *mockEC2) DescribeInternetGateways(_ context.Context, _ *ec2.DescribeInternetGatewaysInput, _ ...func(*ec2.Options)) (*ec2.DescribeInternetGatewaysOutput, error) {
	return &ec2.DescribeInternetGatewaysOutput{InternetGateways: m.internetGateways}, nil
}

func (m *mockEC2) DescribeSubnets(_ context.Context, _ *ec2.DescribeSubnetsInput, _ ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error) {
	return &ec2.DescribeSubnetsOutput{Subnets: m.subnets}, nil
}

func (m *mockEC2) DescribeSecurityGroups(_ context.Context, _ *ec2.DescribeSecurityGroupsInput, _ ...func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error) {
	return &ec2.DescribeSecurityGroupsOutput{SecurityGroups: m.securityGroups}, nil
}

func (m *mockEC2) DescribeRouteTables(_ context.Context, _ *ec2.DescribeRouteTablesInput, _ ...func(*ec2.Options)) (*ec2.DescribeRouteTablesOutput, error) {
	return &ec2.DescribeRouteTablesOutput{RouteTables: m.routeTables}, nil
}

func (m *mockEC2) DescribeNetworkInterfaces(_ context.Context, _ *ec2.DescribeNetworkInterfacesInput, _ ...func(*ec2.Options)) (*ec2.DescribeNetworkInterfacesOutput, error) {
	return &ec2.DescribeNetworkInterfacesOutput{NetworkInterfaces: m.networkIfaces}, nil
}

func (m *mockEC2) DeleteVpcEndpoints(_ context.Context, _ *ec2.DeleteVpcEndpointsInput, _ ...func(*ec2.Options)) (*ec2.DeleteVpcEndpointsOutput, error) {
	return &ec2.DeleteVpcEndpointsOutput{}, nil
}
func (m *mockEC2) DeleteNatGateway(_ context.Context, _ *ec2.DeleteNatGatewayInput, _ ...func(*ec2.Options)) (*ec2.DeleteNatGatewayOutput, error) {
	return &ec2.DeleteNatGatewayOutput{}, nil
}
func (m *mockEC2) DetachInternetGateway(_ context.Context, _ *ec2.DetachInternetGatewayInput, _ ...func(*ec2.Options)) (*ec2.DetachInternetGatewayOutput, error) {
	return &ec2.DetachInternetGatewayOutput{}, nil
}
func (m *mockEC2) DeleteInternetGateway(_ context.Context, _ *ec2.DeleteInternetGatewayInput, _ ...func(*ec2.Options)) (*ec2.DeleteInternetGatewayOutput, error) {
	return &ec2.DeleteInternetGatewayOutput{}, nil
}
func (m *mockEC2) DeleteSubnet(_ context.Context, _ *ec2.DeleteSubnetInput, _ ...func(*ec2.Options)) (*ec2.DeleteSubnetOutput, error) {
	return &ec2.DeleteSubnetOutput{}, nil
}
func (m *mockEC2) DeleteSecurityGroup(_ context.Context, _ *ec2.DeleteSecurityGroupInput, _ ...func(*ec2.Options)) (*ec2.DeleteSecurityGroupOutput, error) {
	return &ec2.DeleteSecurityGroupOutput{}, nil
}
func (m *mockEC2) RevokeSecurityGroupIngress(_ context.Context, _ *ec2.RevokeSecurityGroupIngressInput, _ ...func(*ec2.Options)) (*ec2.RevokeSecurityGroupIngressOutput, error) {
	return &ec2.RevokeSecurityGroupIngressOutput{}, nil
}
func (m *mockEC2) RevokeSecurityGroupEgress(_ context.Context, _ *ec2.RevokeSecurityGroupEgressInput, _ ...func(*ec2.Options)) (*ec2.RevokeSecurityGroupEgressOutput, error) {
	return &ec2.RevokeSecurityGroupEgressOutput{}, nil
}
func (m *mockEC2) DisassociateRouteTable(_ context.Context, _ *ec2.DisassociateRouteTableInput, _ ...func(*ec2.Options)) (*ec2.DisassociateRouteTableOutput, error) {
	return &ec2.DisassociateRouteTableOutput{}, nil
}
func (m *mockEC2) DeleteRouteTable(_ context.Context, _ *ec2.DeleteRouteTableInput, _ ...func(*ec2.Options)) (*ec2.DeleteRouteTableOutput, error) {
	return &ec2.DeleteRouteTableOutput{}, nil
}
func (m *mockEC2) DetachNetworkInterface(_ context.Context, _ *ec2.DetachNetworkInterfaceInput, _ ...func(*ec2.Options)) (*ec2.DetachNetworkInterfaceOutput, error) {
	return &ec2.DetachNetworkInterfaceOutput{}, nil
}
func (m *mockEC2) DeleteNetworkInterface(_ context.Context, _ *ec2.DeleteNetworkInterfaceInput, _ ...func(*ec2.Options)) (*ec2.DeleteNetworkInterfaceOutput, error) {
	return &ec2.DeleteNetworkInterfaceOutput{}, nil
}
func (m *mockEC2) DeleteVpc(_ context.Context, _ *ec2.DeleteVpcInput, _ ...func(*ec2.Options)) (*ec2.DeleteVpcOutput, error) {
	return &ec2.DeleteVpcOutput{}, nil
}
func (m *mockEC2) DescribeVpcEndpointServiceConfigurations(_ context.Context, _ *ec2.DescribeVpcEndpointServiceConfigurationsInput, _ ...func(*ec2.Options)) (*ec2.DescribeVpcEndpointServiceConfigurationsOutput, error) {
	return &ec2.DescribeVpcEndpointServiceConfigurationsOutput{}, nil
}
func (m *mockEC2) DeleteVpcEndpointServiceConfigurations(_ context.Context, _ *ec2.DeleteVpcEndpointServiceConfigurationsInput, _ ...func(*ec2.Options)) (*ec2.DeleteVpcEndpointServiceConfigurationsOutput, error) {
	return &ec2.DeleteVpcEndpointServiceConfigurationsOutput{}, nil
}
func (m *mockEC2) DescribeAddresses(_ context.Context, _ *ec2.DescribeAddressesInput, _ ...func(*ec2.Options)) (*ec2.DescribeAddressesOutput, error) {
	return &ec2.DescribeAddressesOutput{}, nil
}
func (m *mockEC2) ReleaseAddress(_ context.Context, _ *ec2.ReleaseAddressInput, _ ...func(*ec2.Options)) (*ec2.ReleaseAddressOutput, error) {
	return &ec2.ReleaseAddressOutput{}, nil
}

// mockELBv2 implements ELBv2API for testing.
type mockELBv2 struct{}

func (m *mockELBv2) DescribeLoadBalancers(_ context.Context, _ *elbv2.DescribeLoadBalancersInput, _ ...func(*elbv2.Options)) (*elbv2.DescribeLoadBalancersOutput, error) {
	return &elbv2.DescribeLoadBalancersOutput{}, nil
}
func (m *mockELBv2) DeleteLoadBalancer(_ context.Context, _ *elbv2.DeleteLoadBalancerInput, _ ...func(*elbv2.Options)) (*elbv2.DeleteLoadBalancerOutput, error) {
	return &elbv2.DeleteLoadBalancerOutput{}, nil
}

// mockRoute53 implements Route53API for testing.
type mockRoute53 struct {
	zones []route53types.HostedZone
}

func (m *mockRoute53) ListHostedZones(_ context.Context, _ *route53.ListHostedZonesInput, _ ...func(*route53.Options)) (*route53.ListHostedZonesOutput, error) {
	return &route53.ListHostedZonesOutput{HostedZones: m.zones}, nil
}
func (m *mockRoute53) GetHostedZone(_ context.Context, _ *route53.GetHostedZoneInput, _ ...func(*route53.Options)) (*route53.GetHostedZoneOutput, error) {
	return &route53.GetHostedZoneOutput{}, nil
}
func (m *mockRoute53) ListResourceRecordSets(_ context.Context, _ *route53.ListResourceRecordSetsInput, _ ...func(*route53.Options)) (*route53.ListResourceRecordSetsOutput, error) {
	return &route53.ListResourceRecordSetsOutput{}, nil
}
func (m *mockRoute53) ChangeResourceRecordSets(_ context.Context, _ *route53.ChangeResourceRecordSetsInput, _ ...func(*route53.Options)) (*route53.ChangeResourceRecordSetsOutput, error) {
	return &route53.ChangeResourceRecordSetsOutput{}, nil
}
func (m *mockRoute53) DeleteHostedZone(_ context.Context, _ *route53.DeleteHostedZoneInput, _ ...func(*route53.Options)) (*route53.DeleteHostedZoneOutput, error) {
	return &route53.DeleteHostedZoneOutput{}, nil
}

// mockIAM implements IAMAPI for testing.
type mockIAM struct {
	providers []iamtypes.OpenIDConnectProviderListEntry
}

func (m *mockIAM) ListOpenIDConnectProviders(_ context.Context, _ *iam.ListOpenIDConnectProvidersInput, _ ...func(*iam.Options)) (*iam.ListOpenIDConnectProvidersOutput, error) {
	return &iam.ListOpenIDConnectProvidersOutput{OpenIDConnectProviderList: m.providers}, nil
}
func (m *mockIAM) GetOpenIDConnectProvider(_ context.Context, _ *iam.GetOpenIDConnectProviderInput, _ ...func(*iam.Options)) (*iam.GetOpenIDConnectProviderOutput, error) {
	return &iam.GetOpenIDConnectProviderOutput{}, nil
}
func (m *mockIAM) ListRoles(_ context.Context, _ *iam.ListRolesInput, _ ...func(*iam.Options)) (*iam.ListRolesOutput, error) {
	return &iam.ListRolesOutput{}, nil
}
func (m *mockIAM) DeleteOpenIDConnectProvider(_ context.Context, _ *iam.DeleteOpenIDConnectProviderInput, _ ...func(*iam.Options)) (*iam.DeleteOpenIDConnectProviderOutput, error) {
	return &iam.DeleteOpenIDConnectProviderOutput{}, nil
}
func (m *mockIAM) ListAttachedRolePolicies(_ context.Context, _ *iam.ListAttachedRolePoliciesInput, _ ...func(*iam.Options)) (*iam.ListAttachedRolePoliciesOutput, error) {
	return &iam.ListAttachedRolePoliciesOutput{}, nil
}
func (m *mockIAM) DetachRolePolicy(_ context.Context, _ *iam.DetachRolePolicyInput, _ ...func(*iam.Options)) (*iam.DetachRolePolicyOutput, error) {
	return &iam.DetachRolePolicyOutput{}, nil
}
func (m *mockIAM) ListRolePolicies(_ context.Context, _ *iam.ListRolePoliciesInput, _ ...func(*iam.Options)) (*iam.ListRolePoliciesOutput, error) {
	return &iam.ListRolePoliciesOutput{}, nil
}
func (m *mockIAM) DeleteRolePolicy(_ context.Context, _ *iam.DeleteRolePolicyInput, _ ...func(*iam.Options)) (*iam.DeleteRolePolicyOutput, error) {
	return &iam.DeleteRolePolicyOutput{}, nil
}
func (m *mockIAM) ListInstanceProfilesForRole(_ context.Context, _ *iam.ListInstanceProfilesForRoleInput, _ ...func(*iam.Options)) (*iam.ListInstanceProfilesForRoleOutput, error) {
	return &iam.ListInstanceProfilesForRoleOutput{}, nil
}
func (m *mockIAM) RemoveRoleFromInstanceProfile(_ context.Context, _ *iam.RemoveRoleFromInstanceProfileInput, _ ...func(*iam.Options)) (*iam.RemoveRoleFromInstanceProfileOutput, error) {
	return &iam.RemoveRoleFromInstanceProfileOutput{}, nil
}
func (m *mockIAM) DeleteInstanceProfile(_ context.Context, _ *iam.DeleteInstanceProfileInput, _ ...func(*iam.Options)) (*iam.DeleteInstanceProfileOutput, error) {
	return &iam.DeleteInstanceProfileOutput{}, nil
}
func (m *mockIAM) DeleteRole(_ context.Context, _ *iam.DeleteRoleInput, _ ...func(*iam.Options)) (*iam.DeleteRoleOutput, error) {
	return &iam.DeleteRoleOutput{}, nil
}

// mockS3 implements S3API for testing.
type mockS3 struct {
	existingKeys map[string]bool // "bucket/key" → exists
}

func (m *mockS3) HeadObject(_ context.Context, input *s3.HeadObjectInput, _ ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	key := aws.ToString(input.Bucket) + "/" + aws.ToString(input.Key)
	if m.existingKeys != nil && m.existingKeys[key] {
		return &s3.HeadObjectOutput{}, nil
	}
	return nil, fmt.Errorf("NoSuchKey")
}

func makeVPC(vpcID, name, infraID string, isDefault bool) ec2types.Vpc {
	tags := []ec2types.Tag{
		{Key: aws.String("Name"), Value: aws.String(name)},
	}
	if infraID != "" {
		tags = append(tags, ec2types.Tag{
			Key:   aws.String("kubernetes.io/cluster/" + infraID),
			Value: aws.String("owned"),
		})
	}
	return ec2types.Vpc{
		VpcId:     aws.String(vpcID),
		CidrBlock: aws.String("10.0.0.0/16"),
		State:     ec2types.VpcStateAvailable,
		IsDefault: aws.Bool(isDefault),
		Tags:      tags,
	}
}

func makeVPCWithDoNotDelete(vpcID, name, infraID, ciCluster string) ec2types.Vpc {
	tags := []ec2types.Tag{
		{Key: aws.String("Name"), Value: aws.String(name)},
		{Key: aws.String("hypershift.openshift.io/do-not-delete"), Value: aws.String("true")},
		{Key: aws.String("hypershift.openshift.io/ci-cluster"), Value: aws.String(ciCluster)},
	}
	if infraID != "" {
		tags = append(tags, ec2types.Tag{
			Key:   aws.String("kubernetes.io/cluster/" + infraID),
			Value: aws.String("owned"),
		})
	}
	return ec2types.Vpc{
		VpcId:     aws.String(vpcID),
		CidrBlock: aws.String("10.0.0.0/16"),
		State:     ec2types.VpcStateAvailable,
		IsDefault: aws.Bool(false),
		Tags:      tags,
	}
}

func makeVPCWithExpiration(vpcID, name, infraID string, createdAt, expirationDate time.Time) ec2types.Vpc {
	tags := []ec2types.Tag{
		{Key: aws.String("Name"), Value: aws.String(name)},
		{Key: aws.String("creation_date"), Value: aws.String(createdAt.Format(time.RFC3339))},
	}
	if !expirationDate.IsZero() {
		tags = append(tags, ec2types.Tag{
			Key:   aws.String("expirationDate"),
			Value: aws.String(expirationDate.Format("2006-01-02T15:04+00:00")),
		})
	}
	if infraID != "" {
		tags = append(tags, ec2types.Tag{
			Key:   aws.String("kubernetes.io/cluster/" + infraID),
			Value: aws.String("owned"),
		})
	}
	return ec2types.Vpc{
		VpcId:     aws.String(vpcID),
		CidrBlock: aws.String("10.0.0.0/16"),
		State:     ec2types.VpcStateAvailable,
		IsDefault: aws.Bool(false),
		Tags:      tags,
	}
}

func makeVPCWithAge(vpcID, name, infraID string, createdAt time.Time) ec2types.Vpc {
	tags := []ec2types.Tag{
		{Key: aws.String("Name"), Value: aws.String(name)},
		{Key: aws.String("creation_date"), Value: aws.String(createdAt.Format(time.RFC3339))},
	}
	if infraID != "" {
		tags = append(tags, ec2types.Tag{
			Key:   aws.String("kubernetes.io/cluster/" + infraID),
			Value: aws.String("owned"),
		})
	}
	return ec2types.Vpc{
		VpcId:     aws.String(vpcID),
		CidrBlock: aws.String("10.0.0.0/16"),
		State:     ec2types.VpcStateAvailable,
		IsDefault: aws.Bool(false),
		Tags:      tags,
	}
}

func newTestScanner(ec2Client *mockEC2, s3Client *mockS3) *Scanner {
	return &Scanner{
		EC2:     ec2Client,
		ELBv2:   &mockELBv2{},
		Route53: &mockRoute53{},
		IAM:     &mockIAM{},
		S3:      s3Client,
		Config:  DefaultConfig(),
		Now:     func() time.Time { return time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC) },
	}
}

func TestGate0_ProtectedVPCs(t *testing.T) {
	tests := []struct {
		name    string
		vpcName string
		infraID string
		want    Verdict
	}{
		{
			name:    "When VPC belongs to ci-2, it should be classified as PROTECTED",
			vpcName: "hypershift-ci-2-vpc",
			infraID: "298afphbsd2sqa34kjfuo87as2hjhbrn",
			want:    VerdictProtected,
		},
		{
			name:    "When VPC belongs to ci-3, it should be classified as PROTECTED",
			vpcName: "hypershift-ci-3-vpc",
			infraID: "some-ci3-infra-id",
			want:    VerdictProtected,
		},
		{
			name:    "When VPC belongs to ci-metrics, it should be classified as PROTECTED",
			vpcName: "hypershift-ci-metrics-vpc",
			infraID: "metrics-infra-id",
			want:    VerdictProtected,
		},
		{
			name:    "When VPC name is a random CI job, it should NOT be protected by VPC name",
			vpcName: "00ab3695c5f73d4354b9-vpc",
			infraID: "00ab3695c5f73d4354b9",
			want:    VerdictLeaked,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
			created := now.Add(-48 * time.Hour)

			scanner := newTestScanner(
				&mockEC2{
					vpcs: []ec2types.Vpc{
						makeVPCWithAge("vpc-test", tt.vpcName, tt.infraID, created),
					},
				},
				&mockS3{existingKeys: map[string]bool{}},
			)

			results, err := scanner.Scan(context.Background())
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(results) != 1 {
				t.Fatalf("expected 1 result, got %d", len(results))
			}
			if results[0].Verdict != tt.want {
				t.Errorf("got verdict %q, want %q (reason: %s)", results[0].Verdict, tt.want, results[0].VerdictReason)
			}
		})
	}
}

func TestGate0_ProtectedUsers(t *testing.T) {
	tests := []struct {
		name    string
		infraID string
		want    Verdict
	}{
		{
			name:    "When infraID contains a protected user name, it should be PROTECTED",
			infraID: "brcox-mgmt-4tw4x",
			want:    VerdictProtected,
		},
		{
			name:    "When infraID contains 'sjenning', it should be PROTECTED",
			infraID: "sjenning-mgmt-2",
			want:    VerdictProtected,
		},
		{
			name:    "When infraID is a hex string with no user match, it should NOT be protected by user",
			infraID: "00ab3695c5f73d4354b9",
			want:    VerdictLeaked,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
			created := now.Add(-48 * time.Hour)

			scanner := newTestScanner(
				&mockEC2{
					vpcs: []ec2types.Vpc{
						makeVPCWithAge("vpc-test", tt.infraID+"-vpc", tt.infraID, created),
					},
				},
				&mockS3{existingKeys: map[string]bool{}},
			)

			results, err := scanner.Scan(context.Background())
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(results) != 1 {
				t.Fatalf("expected 1 result, got %d", len(results))
			}
			if results[0].Verdict != tt.want {
				t.Errorf("got verdict %q, want %q (reason: %s)", results[0].Verdict, tt.want, results[0].VerdictReason)
			}
		})
	}
}

func TestGate1_Age(t *testing.T) {
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		createdAt time.Time
		want      Verdict
	}{
		{
			name:      "When VPC was created 2 hours ago, it should be TOO_YOUNG",
			createdAt: now.Add(-2 * time.Hour),
			want:      VerdictTooYoung,
		},
		{
			name:      "When VPC was created 48 hours ago, it should pass age check",
			createdAt: now.Add(-48 * time.Hour),
			want:      VerdictLeaked,
		},
		{
			name:      "When VPC was created exactly 24 hours ago, it should pass age check",
			createdAt: now.Add(-24 * time.Hour),
			want:      VerdictLeaked,
		},
		{
			name:      "When VPC was created 23h59m ago, it should be TOO_YOUNG",
			createdAt: now.Add(-23*time.Hour - 59*time.Minute),
			want:      VerdictTooYoung,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scanner := newTestScanner(
				&mockEC2{
					vpcs: []ec2types.Vpc{
						makeVPCWithAge("vpc-test", "abcdef1234567890abcd-vpc", "abcdef1234567890abcd", tt.createdAt),
					},
				},
				&mockS3{existingKeys: map[string]bool{}},
			)

			results, err := scanner.Scan(context.Background())
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(results) != 1 {
				t.Fatalf("expected 1 result, got %d", len(results))
			}
			if results[0].Verdict != tt.want {
				t.Errorf("got verdict %q, want %q (reason: %s)", results[0].Verdict, tt.want, results[0].VerdictReason)
			}
		})
	}
}

func TestGate1_AgeUnknown(t *testing.T) {
	t.Run("When VPC has no creation_date tag and no sub-resources to infer age, it should be classified as UNCERTAIN", func(t *testing.T) {
		scanner := newTestScanner(
			&mockEC2{
				vpcs: []ec2types.Vpc{
					makeVPC("vpc-test", "abcdef1234567890abcd-vpc", "abcdef1234567890abcd", false),
				},
			},
			&mockS3{existingKeys: map[string]bool{}},
		)

		results, err := scanner.Scan(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(results) != 1 {
			t.Fatalf("expected 1 result, got %d", len(results))
		}
		if results[0].Verdict != VerdictUncertain {
			t.Errorf("got verdict %q, want UNCERTAIN when age is undetermined (reason: %s)", results[0].Verdict, results[0].VerdictReason)
		}
	})
}

func TestGate3_OIDC(t *testing.T) {
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
	created := now.Add(-48 * time.Hour)

	tests := []struct {
		name    string
		infraID string
		s3Keys  map[string]bool
		want    Verdict
	}{
		{
			name:    "When S3 OIDC doc exists, it should be ACTIVE",
			infraID: "abcdef1234567890abcd",
			s3Keys: map[string]bool{
				"hypershift-ci-2-oidc/abcdef1234567890abcd/.well-known/openid-configuration": true,
			},
			want: VerdictActive,
		},
		{
			name:    "When S3 OIDC doc does not exist, it should pass OIDC check",
			infraID: "abcdef1234567890abcd",
			s3Keys:  map[string]bool{},
			want:    VerdictLeaked,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scanner := newTestScanner(
				&mockEC2{
					vpcs: []ec2types.Vpc{
						makeVPCWithAge("vpc-test", tt.infraID+"-vpc", tt.infraID, created),
					},
				},
				&mockS3{existingKeys: tt.s3Keys},
			)

			results, err := scanner.Scan(context.Background())
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(results) != 1 {
				t.Fatalf("expected 1 result, got %d", len(results))
			}
			if results[0].Verdict != tt.want {
				t.Errorf("got verdict %q, want %q (reason: %s)", results[0].Verdict, tt.want, results[0].VerdictReason)
			}
		})
	}
}

func TestGate4_EC2Instances(t *testing.T) {
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
	created := now.Add(-48 * time.Hour)

	tests := []struct {
		name      string
		infraID   string
		instances []ec2types.Reservation
		want      Verdict
	}{
		{
			name:    "When EC2 instances are running, it should be ACTIVE",
			infraID: "abcdef1234567890abcd",
			instances: []ec2types.Reservation{
				{Instances: []ec2types.Instance{{InstanceId: aws.String("i-123")}}},
			},
			want: VerdictActive,
		},
		{
			name:      "When no EC2 instances are running, it should be LEAKED",
			infraID:   "abcdef1234567890abcd",
			instances: []ec2types.Reservation{},
			want:      VerdictLeaked,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scanner := newTestScanner(
				&mockEC2{
					vpcs: []ec2types.Vpc{
						makeVPCWithAge("vpc-test", tt.infraID+"-vpc", tt.infraID, created),
					},
					instances: tt.instances,
				},
				&mockS3{existingKeys: map[string]bool{}},
			)

			results, err := scanner.Scan(context.Background())
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(results) != 1 {
				t.Fatalf("expected 1 result, got %d", len(results))
			}
			if results[0].Verdict != tt.want {
				t.Errorf("got verdict %q, want %q (reason: %s)", results[0].Verdict, tt.want, results[0].VerdictReason)
			}
		})
	}
}

func TestFullScan_MixedVPCs(t *testing.T) {
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
	old := now.Add(-48 * time.Hour)
	young := now.Add(-2 * time.Hour)

	ec2Client := &mockEC2{
		vpcs: []ec2types.Vpc{
			makeVPC("vpc-default", "", "", true),
			makeVPCWithAge("vpc-ci2", "hypershift-ci-2-vpc", "ci2-infra", old),
			makeVPCWithAge("vpc-ci3", "hypershift-ci-3-vpc", "ci3-infra", old),
			makeVPCWithAge("vpc-leaked", "abcdef1234567890abcd-vpc", "abcdef1234567890abcd", old),
			makeVPCWithAge("vpc-young", "fedcba0987654321fedc-vpc", "fedcba0987654321fedc", young),
			makeVPCWithAge("vpc-brcox", "brcox-mgmt-4tw4x-vpc", "brcox-mgmt-4tw4x", old),
			makeVPC("vpc-notag", "some-random-vpc", "", false),
		},
	}

	scanner := newTestScanner(ec2Client, &mockS3{existingKeys: map[string]bool{}})

	results, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	verdicts := make(map[string]Verdict)
	for _, r := range results {
		verdicts[r.InfraID] = r.Verdict
	}

	// ci-2 and ci-3 should be PROTECTED
	if v, ok := verdicts["ci2-infra"]; !ok || v != VerdictProtected {
		t.Errorf("ci2-infra: got %v, want PROTECTED", v)
	}
	if v, ok := verdicts["ci3-infra"]; !ok || v != VerdictProtected {
		t.Errorf("ci3-infra: got %v, want PROTECTED", v)
	}

	// Leaked hex VPC should be LEAKED
	if v, ok := verdicts["abcdef1234567890abcd"]; !ok || v != VerdictLeaked {
		t.Errorf("abcdef1234567890abcd: got %v, want LEAKED", v)
	}

	// Young VPC should be TOO_YOUNG
	if v, ok := verdicts["fedcba0987654321fedc"]; !ok || v != VerdictTooYoung {
		t.Errorf("fedcba0987654321fedc: got %v, want TOO_YOUNG", v)
	}

	// brcox should be PROTECTED (protected user)
	if v, ok := verdicts["brcox-mgmt-4tw4x"]; !ok || v != VerdictProtected {
		t.Errorf("brcox-mgmt-4tw4x: got %v, want PROTECTED", v)
	}

	// Untagged VPC should not appear (no infraID → filtered out during grouping)
	if _, ok := verdicts[""]; ok {
		t.Error("untagged VPC should not appear in results")
	}
}

func TestZoneMatchesInfra(t *testing.T) {
	tests := []struct {
		name     string
		zoneName string
		infraID  string
		want     bool
	}{
		{
			name:     "When zone is CI zone for infraID, it should match",
			zoneName: "abc123.ci.hypershift.devcluster.openshift.com.",
			infraID:  "abc123",
			want:     true,
		},
		{
			name:     "When zone is hypershift.local for infraID, it should match",
			zoneName: "abc123.hypershift.local.",
			infraID:  "abc123",
			want:     true,
		},
		{
			name:     "When zone belongs to different infraID, it should NOT match",
			zoneName: "xyz999.ci.hypershift.devcluster.openshift.com.",
			infraID:  "abc123",
			want:     false,
		},
		{
			name:     "When zone is a dev user zone, it should NOT match CI infraID",
			zoneName: "brcox.hypershift.devcluster.openshift.com.",
			infraID:  "abc123",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ZoneMatchesInfra(tt.zoneName, tt.infraID)
			if got != tt.want {
				t.Errorf("ZoneMatchesInfra(%q, %q) = %v, want %v", tt.zoneName, tt.infraID, got, tt.want)
			}
		})
	}
}

func TestIsProtectedVPC(t *testing.T) {
	protected := []string{"hypershift-ci-2-vpc", "hypershift-ci-3-vpc", "hypershift-ci-metrics-vpc"}

	tests := []struct {
		name    string
		vpcName string
		want    bool
	}{
		{"ci-2 is protected", "hypershift-ci-2-vpc", true},
		{"ci-3 is protected", "hypershift-ci-3-vpc", true},
		{"ci-metrics is protected", "hypershift-ci-metrics-vpc", true},
		{"random VPC is not protected", "abc123-vpc", false},
		{"partial match is not protected", "hypershift-ci-2", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsProtectedVPC(tt.vpcName, protected)
			if got != tt.want {
				t.Errorf("IsProtectedVPC(%q) = %v, want %v", tt.vpcName, got, tt.want)
			}
		})
	}
}

func TestIsProtectedUser(t *testing.T) {
	users := DefaultConfig().ProtectedUsers

	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{"brcox in infraID", "brcox-mgmt-4tw4x", true},
		{"sjenning in infraID", "sjenning-mgmt-2", true},
		{"hex ID has no user", "00ab3695c5f73d4354b9", false},
		{"partial user name that does not match", "brc-something", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := IsProtectedUser(tt.value, users)
			if got != tt.want {
				t.Errorf("IsProtectedUser(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

func TestFilterLeaked(t *testing.T) {
	sets := []InfraSet{
		{InfraID: "a", Verdict: VerdictProtected},
		{InfraID: "b", Verdict: VerdictLeaked},
		{InfraID: "c", Verdict: VerdictActive},
		{InfraID: "d", Verdict: VerdictLeaked},
	}

	leaked := FilterLeaked(sets)
	if len(leaked) != 2 {
		t.Fatalf("expected 2 leaked, got %d", len(leaked))
	}
	if leaked[0].InfraID != "b" || leaked[1].InfraID != "d" {
		t.Errorf("unexpected leaked IDs: %v, %v", leaked[0].InfraID, leaked[1].InfraID)
	}
}

func TestCountByVerdict(t *testing.T) {
	sets := []InfraSet{
		{Verdict: VerdictProtected},
		{Verdict: VerdictLeaked},
		{Verdict: VerdictLeaked},
		{Verdict: VerdictActive},
		{Verdict: VerdictTooYoung},
	}

	counts := CountByVerdict(sets)
	if counts[VerdictProtected] != 1 {
		t.Errorf("expected 1 PROTECTED, got %d", counts[VerdictProtected])
	}
	if counts[VerdictLeaked] != 2 {
		t.Errorf("expected 2 LEAKED, got %d", counts[VerdictLeaked])
	}
	if counts[VerdictActive] != 1 {
		t.Errorf("expected 1 ACTIVE, got %d", counts[VerdictActive])
	}
}

func TestGate0_DoNotDeleteTag(t *testing.T) {
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
	old := now.Add(-72 * time.Hour)

	oldVPC := func(vpcID, name, infraID, ciCluster string) ec2types.Vpc {
		vpc := makeVPCWithDoNotDelete(vpcID, name, infraID, ciCluster)
		vpc.Tags = append(vpc.Tags, ec2types.Tag{
			Key:   aws.String("creation_date"),
			Value: aws.String(old.Format(time.RFC3339)),
		})
		return vpc
	}

	tests := []struct {
		name        string
		vpcs        []ec2types.Vpc
		wantVerdict map[string]Verdict
	}{
		{
			name: "When VPC has do-not-delete tag with k8s cluster tag, it should be classified as PROTECTED",
			vpcs: []ec2types.Vpc{
				makeVPCWithDoNotDelete("vpc-ci2", "hypershift-ci-2-vpc", "298afphbsd2sqa34kjfuo87as2hjhbrn", "hypershift-ci-2"),
			},
			wantVerdict: map[string]Verdict{"vpc-ci2": VerdictProtected},
		},
		{
			name: "When VPC has do-not-delete tag without k8s cluster tag, it should be classified as PROTECTED",
			vpcs: []ec2types.Vpc{
				makeVPCWithDoNotDelete("vpc-ci3", "hypershift-ci-3-vpc", "", "hypershift-ci-3"),
			},
			wantVerdict: map[string]Verdict{"vpc-ci3": VerdictProtected},
		},
		{
			name: "When VPC has do-not-delete tag for metrics server, it should be classified as PROTECTED",
			vpcs: []ec2types.Vpc{
				makeVPCWithDoNotDelete("vpc-met", "hypershift-ci-metrics-vpc", "", "hypershift-ci-metrics"),
			},
			wantVerdict: map[string]Verdict{"vpc-met": VerdictProtected},
		},
		{
			name: "When VPC has do-not-delete tag and is old with no OIDC and no instances, it should still be classified as PROTECTED",
			vpcs: []ec2types.Vpc{
				oldVPC("vpc-ci2", "hypershift-ci-2-vpc", "some-infra", "hypershift-ci-2"),
			},
			wantVerdict: map[string]Verdict{"vpc-ci2": VerdictProtected},
		},
		{
			name: "When scanning a mix of do-not-delete and leaked VPCs, it should classify each correctly",
			vpcs: []ec2types.Vpc{
				makeVPCWithDoNotDelete("vpc-ci2", "hypershift-ci-2-vpc", "ci2-infra", "hypershift-ci-2"),
				makeVPCWithDoNotDelete("vpc-ci3", "hypershift-ci-3-vpc", "", "hypershift-ci-3"),
				makeVPCWithDoNotDelete("vpc-met", "hypershift-ci-metrics-vpc", "", "hypershift-ci-metrics"),
				makeVPCWithAge("vpc-leaked", "abcdef1234567890abcd-vpc", "abcdef1234567890abcd", old),
			},
			wantVerdict: map[string]Verdict{
				"vpc-ci2":    VerdictProtected,
				"vpc-ci3":    VerdictProtected,
				"vpc-met":    VerdictProtected,
				"vpc-leaked": VerdictLeaked,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scanner := newTestScanner(
				&mockEC2{vpcs: tt.vpcs},
				&mockS3{existingKeys: map[string]bool{}},
			)

			results, err := scanner.Scan(context.Background())
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			gotVerdicts := make(map[string]Verdict)
			for _, r := range results {
				for _, vpc := range r.VPCs {
					gotVerdicts[vpc.VPCID] = r.Verdict
				}
			}

			for vpcID, wantV := range tt.wantVerdict {
				gotV, ok := gotVerdicts[vpcID]
				if !ok {
					t.Errorf("VPC %s not found in results", vpcID)
					continue
				}
				if gotV != wantV {
					t.Errorf("VPC %s: got verdict %q, want %q", vpcID, gotV, wantV)
				}
			}
		})
	}
}

func TestGate1_ExpirationDate(t *testing.T) {
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
	old := now.Add(-48 * time.Hour)

	tests := []struct {
		name           string
		createdAt      time.Time
		expirationDate time.Time
		want           Verdict
	}{
		{
			name:           "When VPC has expirationDate in the future, it should be classified as TOO_YOUNG",
			createdAt:      now.Add(-2 * time.Hour),
			expirationDate: now.Add(4 * time.Hour),
			want:           VerdictTooYoung,
		},
		{
			name:           "When VPC has expirationDate in the past, it should pass the age gate",
			createdAt:      old,
			expirationDate: now.Add(-5 * 24 * time.Hour),
			want:           VerdictLeaked,
		},
		{
			name:           "When VPC has expirationDate exactly now, it should pass the age gate",
			createdAt:      old,
			expirationDate: now,
			want:           VerdictLeaked,
		},
		{
			name:           "When VPC has no expirationDate but is old enough, it should pass the age gate",
			createdAt:      old,
			expirationDate: time.Time{},
			want:           VerdictLeaked,
		},
		{
			name:           "When VPC has no expirationDate and is too young, it should be classified as TOO_YOUNG",
			createdAt:      now.Add(-2 * time.Hour),
			expirationDate: time.Time{},
			want:           VerdictTooYoung,
		},
		{
			name:           "When VPC was created 48h ago but expirationDate is still in the future, it should be classified as TOO_YOUNG",
			createdAt:      old,
			expirationDate: now.Add(1 * time.Hour),
			want:           VerdictTooYoung,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scanner := newTestScanner(
				&mockEC2{
					vpcs: []ec2types.Vpc{
						makeVPCWithExpiration("vpc-test", "create-cluster-abc12-vpc", "create-cluster-abc12", tt.createdAt, tt.expirationDate),
					},
				},
				&mockS3{existingKeys: map[string]bool{}},
			)

			results, err := scanner.Scan(context.Background())
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(results) != 1 {
				t.Fatalf("expected 1 result, got %d", len(results))
			}
			if results[0].Verdict != tt.want {
				t.Errorf("got verdict %q, want %q (reason: %s)", results[0].Verdict, tt.want, results[0].VerdictReason)
			}
		})
	}
}

func TestParseExpirationDate(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"When format is RFC3339, it should parse", "2026-07-03T17:01:00Z", true},
		{"When format is short ISO with offset, it should parse", "2026-07-03T17:01+00:00", true},
		{"When format is date only, it should parse", "2026-07-03", true},
		{"When input is empty, it should return zero", "", false},
		{"When input is garbage, it should return zero", "not-a-date", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseExpirationDate(tt.input)
			if tt.want && got.IsZero() {
				t.Errorf("parseExpirationDate(%q) returned zero, expected a valid time", tt.input)
			}
			if !tt.want && !got.IsZero() {
				t.Errorf("parseExpirationDate(%q) returned %v, expected zero", tt.input, got)
			}
		})
	}
}

func TestUncertainVerdict(t *testing.T) {
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
	old := now.Add(-48 * time.Hour)

	tests := []struct {
		name    string
		infraID string
		vpcName string
		want    Verdict
	}{
		{
			name:    "When infraID is a dev cluster name like hc1-xxxxx, it should be classified as UNCERTAIN",
			infraID: "hc1-nbh66",
			vpcName: "hc1-nbh66-vpc",
			want:    VerdictUncertain,
		},
		{
			name:    "When infraID is a dev cluster name like clust-xxxxx, it should be classified as UNCERTAIN",
			infraID: "clust-dms2d",
			vpcName: "clust-dms2d-vpc",
			want:    VerdictUncertain,
		},
		{
			name:    "When infraID is a dev cluster name like dev-cluster-1-xxxxx, it should be classified as UNCERTAIN",
			infraID: "dev-cluster-1-wwfbv",
			vpcName: "dev-cluster-1-wwfbv-vpc",
			want:    VerdictUncertain,
		},
		{
			name:    "When infraID is a hex CI infra ID, it should be classified as LEAKED",
			infraID: "00ab3695c5f73d4354b9",
			vpcName: "00ab3695c5f73d4354b9-vpc",
			want:    VerdictLeaked,
		},
		{
			name:    "When infraID is a CI test pattern like node-pool-xxxxx, it should be classified as LEAKED",
			infraID: "node-pool-78vcg",
			vpcName: "node-pool-78vcg-vpc",
			want:    VerdictLeaked,
		},
		{
			name:    "When infraID is a CI test pattern like create-cluster-xxxxx, it should be classified as LEAKED",
			infraID: "create-cluster-hg78d",
			vpcName: "create-cluster-hg78d-vpc",
			want:    VerdictLeaked,
		},
		{
			name:    "When infraID is a QE test like qe-cpv-test-xxxxx, it should be classified as UNCERTAIN",
			infraID: "qe-cpv-test-jxk7s",
			vpcName: "qe-cpv-test-jxk7s-vpc",
			want:    VerdictUncertain,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scanner := newTestScanner(
				&mockEC2{
					vpcs: []ec2types.Vpc{
						makeVPCWithAge("vpc-test", tt.vpcName, tt.infraID, old),
					},
				},
				&mockS3{existingKeys: map[string]bool{}},
			)

			results, err := scanner.Scan(context.Background())
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(results) != 1 {
				t.Fatalf("expected 1 result, got %d", len(results))
			}
			if results[0].Verdict != tt.want {
				t.Errorf("got verdict %q, want %q (reason: %s)", results[0].Verdict, tt.want, results[0].VerdictReason)
			}
		})
	}
}

func TestCheckEC2Instances_APIError(t *testing.T) {
	t.Run("When DescribeInstances returns an error, it should classify as UNCERTAIN not LEAKED", func(t *testing.T) {
		now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
		old := now.Add(-48 * time.Hour)

		ec2Client := &mockEC2ErrorInstances{
			mockEC2: mockEC2{
				vpcs: []ec2types.Vpc{
					makeVPCWithAge("vpc-test", "abcdef1234567890abcd-vpc", "abcdef1234567890abcd", old),
				},
			},
		}

		scanner := &Scanner{
			EC2:     ec2Client,
			ELBv2:   &mockELBv2{},
			Route53: &mockRoute53{},
			IAM:     &mockIAM{},
			S3:      &mockS3{existingKeys: map[string]bool{}},
			Config:  DefaultConfig(),
			Now:     func() time.Time { return now },
		}

		results, err := scanner.Scan(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(results) != 1 {
			t.Fatalf("expected 1 result, got %d", len(results))
		}
		if results[0].Verdict != VerdictUncertain {
			t.Errorf("got verdict %q, want UNCERTAIN when API fails (reason: %s)", results[0].Verdict, results[0].VerdictReason)
		}
	})
}

type mockEC2ErrorInstances struct {
	mockEC2
}

func (m *mockEC2ErrorInstances) DescribeInstances(_ context.Context, _ *ec2.DescribeInstancesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	return nil, fmt.Errorf("ThrottlingException: Rate exceeded")
}

func TestExtractInfraID(t *testing.T) {
	tests := []struct {
		name string
		tags []ec2types.Tag
		want string
	}{
		{
			name: "When VPC has hypershift.openshift.io/infra-id tag, it should prefer that over k8s tag",
			tags: []ec2types.Tag{
				{Key: aws.String("hypershift.openshift.io/infra-id"), Value: aws.String("explicit-infra")},
				{Key: aws.String("kubernetes.io/cluster/k8s-infra"), Value: aws.String("owned")},
			},
			want: "explicit-infra",
		},
		{
			name: "When VPC has only k8s cluster tag, it should extract infraID from the key",
			tags: []ec2types.Tag{
				{Key: aws.String("kubernetes.io/cluster/my-cluster-abc"), Value: aws.String("owned")},
			},
			want: "my-cluster-abc",
		},
		{
			name: "When VPC has no cluster tags, it should return empty",
			tags: []ec2types.Tag{
				{Key: aws.String("Name"), Value: aws.String("some-vpc")},
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractInfraID(tt.tags)
			if got != tt.want {
				t.Errorf("extractInfraID() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestOldestVPC(t *testing.T) {
	tests := []struct {
		name     string
		vpcs     []VPCInfo
		wantZero bool
	}{
		{
			name:     "When VPC list is empty, it should return zero time",
			vpcs:     nil,
			wantZero: true,
		},
		{
			name:     "When all VPCs have zero CreatedAt, it should return zero time",
			vpcs:     []VPCInfo{{VPCID: "vpc-1"}, {VPCID: "vpc-2"}},
			wantZero: true,
		},
		{
			name: "When VPCs have mixed zero and non-zero CreatedAt, it should return the earliest non-zero",
			vpcs: []VPCInfo{
				{VPCID: "vpc-1"},
				{VPCID: "vpc-2", CreatedAt: time.Date(2026, 7, 5, 0, 0, 0, 0, time.UTC)},
				{VPCID: "vpc-3", CreatedAt: time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC)},
			},
			wantZero: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := oldestVPC(tt.vpcs)
			if tt.wantZero && !got.IsZero() {
				t.Errorf("expected zero time, got %v", got)
			}
			if !tt.wantZero && got.IsZero() {
				t.Error("expected non-zero time, got zero")
			}
			if !tt.wantZero {
				expected := time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC)
				if !got.Equal(expected) {
					t.Errorf("expected earliest %v, got %v", expected, got)
				}
			}
		})
	}
}

func TestVPCCreateTime(t *testing.T) {
	tests := []struct {
		name     string
		tags     []ec2types.Tag
		wantZero bool
	}{
		{
			name:     "When VPC has creation_date tag in RFC3339, it should parse",
			tags:     []ec2types.Tag{{Key: aws.String("creation_date"), Value: aws.String("2026-07-03T14:30:00Z")}},
			wantZero: false,
		},
		{
			name:     "When VPC has no creation_date tag, it should return zero",
			tags:     []ec2types.Tag{{Key: aws.String("Name"), Value: aws.String("test")}},
			wantZero: true,
		},
		{
			name:     "When VPC has malformed creation_date tag, it should return zero",
			tags:     []ec2types.Tag{{Key: aws.String("creation_date"), Value: aws.String("not-a-date")}},
			wantZero: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vpc := ec2types.Vpc{Tags: tt.tags}
			got := VPCCreateTime(vpc)
			if tt.wantZero && !got.IsZero() {
				t.Errorf("expected zero time, got %v", got)
			}
			if !tt.wantZero && got.IsZero() {
				t.Error("expected non-zero time")
			}
		})
	}
}
