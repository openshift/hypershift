package karpenter

import (
	"errors"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/support/awsapi"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	karpenterv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	"go.uber.org/mock/gomock"
)

func TestMatchesInstanceHostname(t *testing.T) {
	const (
		customDomainNodeName = "ip-10-0-1-2.custom.example.internal"
		privateDNSName       = "ip-10-0-1-2.eu-central-1.compute.internal"
	)

	instance := func(id, privateDNSName, vpcID string) ec2types.Instance {
		return ec2types.Instance{
			InstanceId:     aws.String(id),
			PrivateDnsName: aws.String(privateDNSName),
			VpcId:          aws.String(vpcID),
		}
	}

	testCases := map[string]struct {
		expectEC2 func(*awsapi.MockEC2API)
		instances []ec2types.Instance
		nodeName  string
		want      bool
		wantErr   string
	}{
		"When the node name is the PrivateDnsName, it should match without looking up the DHCP options": {
			expectEC2: func(*awsapi.MockEC2API) {},
			instances: []ec2types.Instance{instance("i-1", privateDNSName, "vpc-1")},
			nodeName:  privateDNSName,
			want:      true,
		},
		"When a later instance has the PrivateDnsName, it should match without looking up the DHCP options": {
			expectEC2: func(*awsapi.MockEC2API) {},
			instances: []ec2types.Instance{
				instance("i-1", "ip-10-0-1-1.eu-central-1.compute.internal", "vpc-1"),
				instance("i-2", privateDNSName, "vpc-1"),
			},
			nodeName: privateDNSName,
			want:     true,
		},
		"When the node name has the DHCP domain but another host label, it should not match": {
			expectEC2: expectDHCPDomainName("custom.example.internal"),
			instances: []ec2types.Instance{instance("i-1", privateDNSName, "vpc-1")},
			nodeName:  "ip-10-0-9-9.custom.example.internal",
		},
		"When the node name is the first label of the PrivateDnsName with the DHCP domain, it should match": {
			expectEC2: expectDHCPDomainName("custom.example.internal"),
			instances: []ec2types.Instance{instance("i-1", privateDNSName, "vpc-1")},
			nodeName:  customDomainNodeName,
			want:      true,
		},
		"When the node name uses a domain that is not in the DHCP options, it should not match": {
			expectEC2: expectDHCPDomainName("custom.example.internal"),
			instances: []ec2types.Instance{instance("i-1", privateDNSName, "vpc-1")},
			nodeName:  "ip-10-0-1-2.other.example.internal",
		},
		"When the DHCP domain-name has multiple domains and the node name uses the first, it should match": {
			expectEC2: expectDHCPDomainName("a.example.internal b.example.internal"),
			instances: []ec2types.Instance{instance("i-1", privateDNSName, "vpc-1")},
			nodeName:  "ip-10-0-1-2.a.example.internal",
			want:      true,
		},
		"When the DHCP domain-name has multiple domains and the node name uses the second, it should not match": {
			expectEC2: expectDHCPDomainName("a.example.internal b.example.internal"),
			instances: []ec2types.Instance{instance("i-1", privateDNSName, "vpc-1")},
			nodeName:  "ip-10-0-1-2.b.example.internal",
		},
		"When the DHCP domain has upper case letters and a trailing dot, it should match the lower case node name without the dot": {
			expectEC2: expectDHCPDomainName("Corp.Example.com."),
			instances: []ec2types.Instance{instance("i-1", privateDNSName, "vpc-1")},
			nodeName:  "ip-10-0-1-2.corp.example.com",
			want:      true,
		},
		"When multiple instances are in the same VPC, it should look up the DHCP options once": {
			expectEC2: expectDHCPDomainName("custom.example.internal"),
			instances: []ec2types.Instance{
				instance("i-1", "ip-10-0-1-1.eu-central-1.compute.internal", "vpc-1"),
				instance("i-2", privateDNSName, "vpc-1"),
			},
			nodeName: customDomainNodeName,
			want:     true,
		},
		"When describing the VPC fails, it should error": {
			expectEC2: func(m *awsapi.MockEC2API) {
				m.EXPECT().DescribeVpcs(gomock.Any(), gomock.Any()).Return(nil, errors.New("AccessDenied: ec2:DescribeVpcs"))
			},
			instances: []ec2types.Instance{instance("i-1", privateDNSName, "vpc-1")},
			nodeName:  customDomainNodeName,
			wantErr:   "AccessDenied: ec2:DescribeVpcs",
		},
		"When describing the VPC returns no VPC, it should error": {
			expectEC2: func(m *awsapi.MockEC2API) {
				m.EXPECT().DescribeVpcs(gomock.Any(), gomock.Any()).Return(&ec2.DescribeVpcsOutput{}, nil)
			},
			instances: []ec2types.Instance{instance("i-1", privateDNSName, "vpc-1")},
			nodeName:  customDomainNodeName,
			wantErr:   "vpc vpc-1 not found",
		},
		"When describing the DHCP options fails, it should error": {
			expectEC2: func(m *awsapi.MockEC2API) {
				expectDHCPOptions(m, nil, errors.New("AccessDenied: ec2:DescribeDhcpOptions"))
			},
			instances: []ec2types.Instance{instance("i-1", privateDNSName, "vpc-1")},
			nodeName:  customDomainNodeName,
			wantErr:   "AccessDenied: ec2:DescribeDhcpOptions",
		},
		"When describing the DHCP options returns no DHCP options, it should error": {
			expectEC2: func(m *awsapi.MockEC2API) {
				expectDHCPOptions(m, &ec2.DescribeDhcpOptionsOutput{}, nil)
			},
			instances: []ec2types.Instance{instance("i-1", privateDNSName, "vpc-1")},
			nodeName:  customDomainNodeName,
			wantErr:   "dhcp options dopt-1 not found",
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			g := NewWithT(t)

			nodeClaims := []karpenterv1.NodeClaim{}
			for _, instance := range tc.instances {
				nodeClaims = append(nodeClaims, karpenterv1.NodeClaim{
					Status: karpenterv1.NodeClaimStatus{ProviderID: "aws:///eu-central-1a/" + aws.ToString(instance.InstanceId)},
				})
			}

			mockEC2 := awsapi.NewMockEC2API(gomock.NewController(t))
			mockEC2.EXPECT().DescribeInstances(gomock.Any(), gomock.Any()).Return(
				&ec2.DescribeInstancesOutput{
					Reservations: []ec2types.Reservation{{Instances: tc.instances}},
				}, nil,
			)
			tc.expectEC2(mockEC2)

			got, err := matchesInstanceHostname(t.Context(), tc.nodeName, nodeClaims, mockEC2)
			if tc.wantErr != "" {
				g.Expect(err).To(MatchError(ContainSubstring(tc.wantErr)))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
			g.Expect(got).To(Equal(tc.want))
		})
	}
}

// expectDHCPDomainName expects one lookup of the DHCP option set "dopt-1" of a VPC, which has the given domain-name.
func expectDHCPDomainName(domainName string) func(*awsapi.MockEC2API) {
	dhcpOptions := &ec2.DescribeDhcpOptionsOutput{
		DhcpOptions: []ec2types.DhcpOptions{{
			DhcpConfigurations: []ec2types.DhcpConfiguration{{
				Key:    aws.String("domain-name"),
				Values: []ec2types.AttributeValue{{Value: aws.String(domainName)}},
			}},
		}},
	}

	return func(m *awsapi.MockEC2API) {
		expectDHCPOptions(m, dhcpOptions, nil)
	}
}

// expectDHCPOptions expects exactly one VPC lookup that returns the DHCP option set "dopt-1" and exactly one lookup of that option set.
// A second lookup or a missing one fails the test.
func expectDHCPOptions(m *awsapi.MockEC2API, output *ec2.DescribeDhcpOptionsOutput, err error) {
	m.EXPECT().DescribeVpcs(gomock.Any(), &ec2.DescribeVpcsInput{VpcIds: []string{"vpc-1"}}).Return(
		&ec2.DescribeVpcsOutput{Vpcs: []ec2types.Vpc{{DhcpOptionsId: aws.String("dopt-1")}}}, nil,
	).Times(1)
	m.EXPECT().DescribeDhcpOptions(gomock.Any(), &ec2.DescribeDhcpOptionsInput{DhcpOptionsIds: []string{"dopt-1"}}).Return(output, err).Times(1)
}
