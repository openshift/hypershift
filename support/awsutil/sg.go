package awsutil

import (
	"context"
	"fmt"

	"github.com/openshift/hypershift/support/awsapi"

	"github.com/aws/aws-sdk-go-v2/aws"
	ec2v2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"

	"github.com/pkg/errors"
)

func DefaultWorkerSGEgressRules() []ec2types.IpPermission {
	return []ec2types.IpPermission{
		{
			IpProtocol: aws.String("-1"),
			IpRanges: []ec2types.IpRange{
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

func VPCEndpointSecurityGroupRules(machineCIDRs []string, port int32) []ec2types.IpPermission {
	var inboundRules []ec2types.IpPermission
	for _, cidr := range machineCIDRs {
		machineCIDRInboundRules := []ec2types.IpPermission{
			{
				IpProtocol: aws.String("tcp"),
				IpRanges: []ec2types.IpRange{
					{
						CidrIp:      aws.String(cidr),
						Description: aws.String("Control plane service"),
					},
				},
				FromPort: aws.Int32(port),
				ToPort:   aws.Int32(port),
			},
		}
		inboundRules = append(inboundRules, machineCIDRInboundRules...)
	}
	return inboundRules
}

func GetSecurityGroupV2(ctx context.Context, ec2Client awsapi.EC2API, filter []ec2types.Filter) (*ec2types.SecurityGroup, error) {
	describeSGResult, err := ec2Client.DescribeSecurityGroups(ctx, &ec2v2.DescribeSecurityGroupsInput{Filters: filter})
	if err != nil {
		return nil, fmt.Errorf("cannot list security groups: %w", err)
	}
	if len(describeSGResult.SecurityGroups) == 0 {
		return nil, nil
	}
	return &describeSGResult.SecurityGroups[0], nil
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

func GetSecurityGroupByIdV2(ctx context.Context, ec2Client awsapi.EC2API, id string) (*ec2types.SecurityGroup, error) {
	describeSGResult, err := ec2Client.DescribeSecurityGroups(ctx, &ec2v2.DescribeSecurityGroupsInput{GroupIds: []string{id}})
	if err != nil {
		return nil, fmt.Errorf("cannot get security group: %w", err)
	}
	if len(describeSGResult.SecurityGroups) == 0 {
		return nil, nil
	}
	return &describeSGResult.SecurityGroups[0], nil
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

func UpdateResourceTags(ctx context.Context, ec2Client awsapi.EC2API, resourceID string, create, remove map[string]string) error {
	if len(create) > 0 {
		tags := make([]ec2types.Tag, 0, len(create))
		for k, v := range create {
			tags = append(tags, ec2types.Tag{Key: aws.String(k), Value: aws.String(v)})
		}
		if _, err := ec2Client.CreateTags(ctx, &ec2v2.CreateTagsInput{
			Resources: []string{resourceID},
			Tags:      tags,
		}); err != nil {
			return errors.Wrapf(err, "failed to create tags for resource %q: %+v", resourceID, create)
		}
	}
	if len(remove) > 0 {
		tags := make([]ec2types.Tag, 0, len(remove))
		for k, v := range remove {
			tags = append(tags, ec2types.Tag{Key: aws.String(k), Value: aws.String(v)})
		}
		if _, err := ec2Client.DeleteTags(ctx, &ec2v2.DeleteTagsInput{
			Resources: []string{resourceID},
			Tags:      tags,
		}); err != nil {
			return errors.Wrapf(err, "failed to delete tags for resource %q: %v", resourceID, remove)
		}
	}
	return nil
}

func MapToEC2Tags(m map[string]string) []*ec2.Tag {
	if len(m) == 0 {
		return nil
	}
	tags := make([]*ec2.Tag, 0, len(m))
	for k, v := range m {
		tags = append(tags, &ec2.Tag{
			Key:   aws.String(k),
			Value: aws.String(v),
		})
	}
	return tags
}

func EC2TagsToMap(tags []*ec2.Tag) map[string]string {
	if len(tags) == 0 {
		return nil
	}
	m := make(map[string]string)
	for _, tag := range tags {
		m[*tag.Key] = *tag.Value
	}
	return m
}
