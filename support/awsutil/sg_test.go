package awsutil

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

func TestDiffPermissions(t *testing.T) {
	ipRange := func(desc, cidr string) ec2types.IpRange {
		return ec2types.IpRange{
			Description: aws.String(desc),
			CidrIp:      aws.String(cidr),
		}
	}

	groupPair := func(groupID, userID, desc string) ec2types.UserIdGroupPair {
		return ec2types.UserIdGroupPair{
			GroupId:     aws.String(groupID),
			UserId:     aws.String(userID),
			Description: aws.String(desc),
		}
	}

	ipRangePerm := func(from, to int32, protocol string, ranges ...ec2types.IpRange) ec2types.IpPermission {
		return ec2types.IpPermission{
			FromPort:   aws.Int32(from),
			ToPort:     aws.Int32(to),
			IpProtocol: aws.String(protocol),
			IpRanges:   ranges,
		}
	}

	groupPairPerm := func(from, to int32, protocol string, pairs ...ec2types.UserIdGroupPair) ec2types.IpPermission {
		return ec2types.IpPermission{
			FromPort:         aws.Int32(from),
			ToPort:           aws.Int32(to),
			IpProtocol:       aws.String(protocol),
			UserIdGroupPairs: pairs,
		}
	}

	tests := []struct {
		name     string
		actual   []ec2types.IpPermission
		required []ec2types.IpPermission
		expected []ec2types.IpPermission
	}{
		{
			name:   "When actual is empty it should return all required permissions",
			actual: nil,
			required: []ec2types.IpPermission{
				ipRangePerm(443, 443, "tcp", ipRange("Control plane service", "10.0.0.0/16")),
				groupPairPerm(4789, 4789, "udp", groupPair("sg-123", "111111111111", "VXLAN Packets")),
			},
			expected: []ec2types.IpPermission{
				ipRangePerm(443, 443, "tcp", ipRange("Control plane service", "10.0.0.0/16")),
				groupPairPerm(4789, 4789, "udp", groupPair("sg-123", "111111111111", "VXLAN Packets")),
			},
		},
		{
			name: "When all IpRange permissions already exist it should return none",
			actual: []ec2types.IpPermission{
				ipRangePerm(443, 443, "tcp", ipRange("Control plane service", "10.0.0.0/16")),
				ipRangePerm(6443, 6443, "tcp", ipRange("Control plane service", "10.0.0.0/16")),
			},
			required: []ec2types.IpPermission{
				ipRangePerm(443, 443, "tcp", ipRange("Control plane service", "10.0.0.0/16")),
			},
			expected: nil,
		},
		{
			name: "When all UserIdGroupPair permissions already exist it should return none",
			actual: []ec2types.IpPermission{
				groupPairPerm(4789, 4789, "udp", groupPair("sg-123", "111111111111", "VXLAN Packets")),
				groupPairPerm(6081, 6081, "udp", groupPair("sg-123", "111111111111", "GENEVE Protocol")),
			},
			required: []ec2types.IpPermission{
				groupPairPerm(4789, 4789, "udp", groupPair("sg-123", "111111111111", "VXLAN Packets")),
				groupPairPerm(6081, 6081, "udp", groupPair("sg-123", "111111111111", "GENEVE Protocol")),
			},
			expected: nil,
		},
		{
			name: "When some UserIdGroupPair permissions are missing it should return only the missing ones",
			actual: []ec2types.IpPermission{
				groupPairPerm(4789, 4789, "udp", groupPair("sg-123", "111111111111", "VXLAN Packets")),
			},
			required: []ec2types.IpPermission{
				groupPairPerm(4789, 4789, "udp", groupPair("sg-123", "111111111111", "VXLAN Packets")),
				groupPairPerm(6081, 6081, "udp", groupPair("sg-123", "111111111111", "GENEVE Protocol")),
				groupPairPerm(10250, 10250, "tcp", groupPair("sg-123", "111111111111", "Kubelet")),
			},
			expected: []ec2types.IpPermission{
				groupPairPerm(6081, 6081, "udp", groupPair("sg-123", "111111111111", "GENEVE Protocol")),
				groupPairPerm(10250, 10250, "tcp", groupPair("sg-123", "111111111111", "Kubelet")),
			},
		},
		{
			name: "When mixed IpRange and UserIdGroupPair permissions exist it should correctly diff both types",
			actual: []ec2types.IpPermission{
				groupPairPerm(4789, 4789, "udp", groupPair("sg-123", "111111111111", "VXLAN Packets")),
				ipRangePerm(22, 22, "tcp", ipRange("SSH", "10.0.0.0/16")),
			},
			required: []ec2types.IpPermission{
				groupPairPerm(4789, 4789, "udp", groupPair("sg-123", "111111111111", "VXLAN Packets")),
				groupPairPerm(6081, 6081, "udp", groupPair("sg-123", "111111111111", "GENEVE Protocol")),
				ipRangePerm(22, 22, "tcp", ipRange("SSH", "10.0.0.0/16")),
				ipRangePerm(-1, -1, "icmp", ipRange("ICMP", "10.0.0.0/16")),
			},
			expected: []ec2types.IpPermission{
				groupPairPerm(6081, 6081, "udp", groupPair("sg-123", "111111111111", "GENEVE Protocol")),
				ipRangePerm(-1, -1, "icmp", ipRange("ICMP", "10.0.0.0/16")),
			},
		},
		{
			name: "When UserIdGroupPair has different group ID it should detect as missing",
			actual: []ec2types.IpPermission{
				groupPairPerm(4789, 4789, "udp", groupPair("sg-999", "111111111111", "VXLAN Packets")),
			},
			required: []ec2types.IpPermission{
				groupPairPerm(4789, 4789, "udp", groupPair("sg-123", "111111111111", "VXLAN Packets")),
			},
			expected: []ec2types.IpPermission{
				groupPairPerm(4789, 4789, "udp", groupPair("sg-123", "111111111111", "VXLAN Packets")),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := DiffPermissions(tt.actual, tt.required)
			if len(tt.expected) == 0 && len(result) == 0 {
				return
			}
			if len(result) != len(tt.expected) {
				t.Fatalf("expected %d missing permissions, got %d", len(tt.expected), len(result))
			}
			for i := range tt.expected {
				if aws.ToInt32(result[i].FromPort) != aws.ToInt32(tt.expected[i].FromPort) ||
					aws.ToInt32(result[i].ToPort) != aws.ToInt32(tt.expected[i].ToPort) ||
					aws.ToString(result[i].IpProtocol) != aws.ToString(tt.expected[i].IpProtocol) {
					t.Errorf("permission %d mismatch: got port %d-%d/%s, want port %d-%d/%s",
						i,
						aws.ToInt32(result[i].FromPort), aws.ToInt32(result[i].ToPort), aws.ToString(result[i].IpProtocol),
						aws.ToInt32(tt.expected[i].FromPort), aws.ToInt32(tt.expected[i].ToPort), aws.ToString(tt.expected[i].IpProtocol),
					)
				}
			}
		})
	}
}
