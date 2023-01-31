package awsutil

import (
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
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

func DefaultWorkerSGIngressRules(vpcCIDRBlock, sgGroupID, sgUserID string) []*ec2.IpPermission {
	return []*ec2.IpPermission{
		{
			IpProtocol: aws.String("icmp"),
			IpRanges: []*ec2.IpRange{
				{
					CidrIp: aws.String(vpcCIDRBlock),
				},
			},
			FromPort: aws.Int64(-1),
			ToPort:   aws.Int64(-1),
		},
		{
			IpProtocol: aws.String("tcp"),
			IpRanges: []*ec2.IpRange{
				{
					CidrIp: aws.String(vpcCIDRBlock),
				},
			},
			FromPort: aws.Int64(22),
			ToPort:   aws.Int64(22),
		},
		{
			FromPort:   aws.Int64(4789),
			ToPort:     aws.Int64(4789),
			IpProtocol: aws.String("udp"),
			UserIdGroupPairs: []*ec2.UserIdGroupPair{
				{
					GroupId: aws.String(sgGroupID),
					UserId:  aws.String(sgUserID),
				},
			},
		},
		{
			FromPort:   aws.Int64(6081),
			ToPort:     aws.Int64(6081),
			IpProtocol: aws.String("udp"),
			UserIdGroupPairs: []*ec2.UserIdGroupPair{
				{
					GroupId: aws.String(sgGroupID),
					UserId:  aws.String(sgUserID),
				},
			},
		},
		{
			FromPort:   aws.Int64(500),
			ToPort:     aws.Int64(500),
			IpProtocol: aws.String("udp"),
			UserIdGroupPairs: []*ec2.UserIdGroupPair{
				{
					GroupId: aws.String(sgGroupID),
					UserId:  aws.String(sgUserID),
				},
			},
		},
		{
			FromPort:   aws.Int64(4500),
			ToPort:     aws.Int64(4500),
			IpProtocol: aws.String("udp"),
			UserIdGroupPairs: []*ec2.UserIdGroupPair{
				{
					GroupId: aws.String(sgGroupID),
					UserId:  aws.String(sgUserID),
				},
			},
		},
		{
			IpProtocol: aws.String("50"),
			UserIdGroupPairs: []*ec2.UserIdGroupPair{
				{
					GroupId: aws.String(sgGroupID),
					UserId:  aws.String(sgUserID),
				},
			},
		},
		{
			FromPort:   aws.Int64(9000),
			ToPort:     aws.Int64(9999),
			IpProtocol: aws.String("tcp"),
			UserIdGroupPairs: []*ec2.UserIdGroupPair{
				{
					GroupId: aws.String(sgGroupID),
					UserId:  aws.String(sgUserID),
				},
			},
		},
		{
			FromPort:   aws.Int64(9000),
			ToPort:     aws.Int64(9999),
			IpProtocol: aws.String("udp"),
			UserIdGroupPairs: []*ec2.UserIdGroupPair{
				{
					GroupId: aws.String(sgGroupID),
					UserId:  aws.String(sgUserID),
				},
			},
		},
		{
			FromPort:   aws.Int64(10250),
			ToPort:     aws.Int64(10250),
			IpProtocol: aws.String("tcp"),
			UserIdGroupPairs: []*ec2.UserIdGroupPair{
				{
					GroupId: aws.String(sgGroupID),
					UserId:  aws.String(sgUserID),
				},
			},
		},
		{
			FromPort:   aws.Int64(30000),
			ToPort:     aws.Int64(32767),
			IpProtocol: aws.String("tcp"),
			UserIdGroupPairs: []*ec2.UserIdGroupPair{
				{
					GroupId: aws.String(sgGroupID),
					UserId:  aws.String(sgUserID),
				},
			},
		},
		{
			FromPort:   aws.Int64(30000),
			ToPort:     aws.Int64(32767),
			IpProtocol: aws.String("udp"),
			UserIdGroupPairs: []*ec2.UserIdGroupPair{
				{
					GroupId: aws.String(sgGroupID),
					UserId:  aws.String(sgUserID),
				},
			},
		},
	}
}
