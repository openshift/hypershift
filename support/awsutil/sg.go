package awsutil

import (
	"fmt"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"
)

func DefaultWorkerSGEgressRules() []*ec2.IpPermission {
	return []*ec2.IpPermission{
		{
			IpProtocol: aws.String("-1"),
			IpRanges: []*ec2.IpRange{
				{
					CidrIp: aws.String("0.0.0.0/0"),
				},
			},
		},
	}
}

// DefaultWorkerSGIngressRules templates out the required inbound security group rules for the default worker security
// group. This AWS security group is attached to worker node EC2 instances and the PrivateLink VPC Endpoint for the
// Hosted Control Plane.
// Sources:
// - https://github.com/openshift/installer/blob/da42a4d4020f8c8d8140c0cdc45ee11932343f7d/pkg/asset/manifests/aws/cluster.go#L48-L122
// - https://github.com/openshift/installer/blob/da42a4d4020f8c8d8140c0cdc45ee11932343f7d/upi/aws/cloudformation/03_cluster_security.yaml
func DefaultWorkerSGIngressRules(machineCIDRs []string, sgGroupID, sgUserID string) []*ec2.IpPermission {
	inboundRules := []*ec2.IpPermission{
		{
			FromPort:   aws.Int64(4789),
			ToPort:     aws.Int64(4789),
			IpProtocol: aws.String("udp"),
			UserIdGroupPairs: []*ec2.UserIdGroupPair{
				{
					GroupId:     aws.String(sgGroupID),
					UserId:      aws.String(sgUserID),
					Description: aws.String("VXLAN Packets"),
				},
			},
		},
		{
			FromPort:   aws.Int64(6081),
			ToPort:     aws.Int64(6081),
			IpProtocol: aws.String("udp"),
			UserIdGroupPairs: []*ec2.UserIdGroupPair{
				{
					GroupId:     aws.String(sgGroupID),
					UserId:      aws.String(sgUserID),
					Description: aws.String("GENEVE Protocol"),
				},
			},
		},
		{
			FromPort:   aws.Int64(500),
			ToPort:     aws.Int64(500),
			IpProtocol: aws.String("udp"),
			UserIdGroupPairs: []*ec2.UserIdGroupPair{
				{
					GroupId:     aws.String(sgGroupID),
					UserId:      aws.String(sgUserID),
					Description: aws.String("IPSEC IKE Packets"),
				},
			},
		},
		{
			FromPort:   aws.Int64(4500),
			ToPort:     aws.Int64(4500),
			IpProtocol: aws.String("udp"),
			UserIdGroupPairs: []*ec2.UserIdGroupPair{
				{
					GroupId:     aws.String(sgGroupID),
					UserId:      aws.String(sgUserID),
					Description: aws.String("IPSEC NAT-T Packets"),
				},
			},
		},
		{
			FromPort:   aws.Int64(-1),
			ToPort:     aws.Int64(-1),
			IpProtocol: aws.String("50"), // ESP Protocol
			UserIdGroupPairs: []*ec2.UserIdGroupPair{
				{
					GroupId:     aws.String(sgGroupID),
					UserId:      aws.String(sgUserID),
					Description: aws.String("IPSEC ESP Packets"),
				},
			},
		},
		{
			FromPort:   aws.Int64(9000),
			ToPort:     aws.Int64(9999),
			IpProtocol: aws.String("tcp"),
			UserIdGroupPairs: []*ec2.UserIdGroupPair{
				{
					GroupId:     aws.String(sgGroupID),
					UserId:      aws.String(sgUserID),
					Description: aws.String("Internal cluster communication (TCP)"),
				},
			},
		},
		{
			FromPort:   aws.Int64(9000),
			ToPort:     aws.Int64(9999),
			IpProtocol: aws.String("udp"),
			UserIdGroupPairs: []*ec2.UserIdGroupPair{
				{
					GroupId:     aws.String(sgGroupID),
					UserId:      aws.String(sgUserID),
					Description: aws.String("Internal cluster communication (UDP)"),
				},
			},
		},
		{
			FromPort:   aws.Int64(10250),
			ToPort:     aws.Int64(10250),
			IpProtocol: aws.String("tcp"),
			UserIdGroupPairs: []*ec2.UserIdGroupPair{
				{
					GroupId:     aws.String(sgGroupID),
					UserId:      aws.String(sgUserID),
					Description: aws.String("Kubelet"),
				},
			},
		},
		{
			FromPort:   aws.Int64(30000),
			ToPort:     aws.Int64(32767),
			IpProtocol: aws.String("tcp"),
			UserIdGroupPairs: []*ec2.UserIdGroupPair{
				{
					GroupId:     aws.String(sgGroupID),
					UserId:      aws.String(sgUserID),
					Description: aws.String("Kubernetes services (TCP)"),
				},
			},
		},
		{
			FromPort:   aws.Int64(30000),
			ToPort:     aws.Int64(32767),
			IpProtocol: aws.String("udp"),
			UserIdGroupPairs: []*ec2.UserIdGroupPair{
				{
					GroupId:     aws.String(sgGroupID),
					UserId:      aws.String(sgUserID),
					Description: aws.String("Kubernetes services (UDP)"),
				},
			},
		},
	}

	// Typically, only one machineCIDR is provided, however we handle many machineCIDRs because it is allowed by
	// OpenShift.
	for _, cidr := range machineCIDRs {
		machineCIDRInboundRules := []*ec2.IpPermission{
			{
				IpProtocol: aws.String("icmp"),
				IpRanges: []*ec2.IpRange{
					{
						CidrIp:      aws.String(cidr),
						Description: aws.String("ICMP"),
					},
				},
				FromPort: aws.Int64(-1),
				ToPort:   aws.Int64(-1),
			},
			{
				IpProtocol: aws.String("tcp"),
				IpRanges: []*ec2.IpRange{
					{
						CidrIp:      aws.String(cidr),
						Description: aws.String("SSH"),
					},
				},
				FromPort: aws.Int64(22),
				ToPort:   aws.Int64(22),
			},
		}

		inboundRules = append(inboundRules, machineCIDRInboundRules...)
	}

	return inboundRules
}

func VPCEndpointSecurityGroupRules(machineCIDRs []string, port int64) []*ec2.IpPermission {
	var inboundRules []*ec2.IpPermission
	for _, cidr := range machineCIDRs {
		machineCIDRInboundRules := []*ec2.IpPermission{
			{
				IpProtocol: aws.String("tcp"),
				IpRanges: []*ec2.IpRange{
					{
						CidrIp:      aws.String(cidr),
						Description: aws.String("Control plane service"),
					},
				},
				FromPort: aws.Int64(port),
				ToPort:   aws.Int64(port),
			},
		}
		inboundRules = append(inboundRules, machineCIDRInboundRules...)
	}
	return inboundRules
}

func GetSecurityGroup(ec2Client ec2iface.EC2API, filter []*ec2.Filter) (*ec2.SecurityGroup, error) {
	describeSGResult, err := ec2Client.DescribeSecurityGroups(&ec2.DescribeSecurityGroupsInput{Filters: filter})
	if err != nil {
		return nil, fmt.Errorf("cannot list security groups: %w", err)
	}
	if len(describeSGResult.SecurityGroups) == 0 {
		return nil, nil
	}
	return describeSGResult.SecurityGroups[0], nil
}

func GetSecurityGroupById(ec2Client ec2iface.EC2API, id string) (*ec2.SecurityGroup, error) {
	describeSGResult, err := ec2Client.DescribeSecurityGroups(&ec2.DescribeSecurityGroupsInput{GroupIds: []*string{aws.String(id)}})
	if err != nil {
		return nil, fmt.Errorf("cannot get security group: %w", err)
	}
	if len(describeSGResult.SecurityGroups) == 0 {
		return nil, nil
	}
	return describeSGResult.SecurityGroups[0], nil
}
