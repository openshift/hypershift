package aws

import (
	"fmt"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"
)

func (o *CreateInfraOptions) CreateWorkerSecurityGroup(client ec2iface.EC2API, vpcID string) (string, error) {
	groupName := fmt.Sprintf("%s-worker-sg", o.InfraID)
	result, err := client.CreateSecurityGroup(&ec2.CreateSecurityGroupInput{
		GroupName:   aws.String(groupName),
		Description: aws.String("worker security group"),
		VpcId:       aws.String(vpcID),
		TagSpecifications: []*ec2.TagSpecification{
			{
				ResourceType: aws.String("security-group"),
				Tags:         ec2Tags(o.InfraID, groupName),
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("cannot create worker security group: %w", err)
	}
	securityGroupID := aws.StringValue(result.GroupId)
	ingressRules := []*ec2.AuthorizeSecurityGroupIngressInput{
		{
			GroupId:    aws.String(securityGroupID),
			IpProtocol: aws.String("icmp"),
			CidrIp:     aws.String(DefaultCIDRBlock),
			FromPort:   aws.Int64(-1),
			ToPort:     aws.Int64(-1),
		},
		{
			GroupId:    aws.String(securityGroupID),
			IpProtocol: aws.String("tcp"),
			CidrIp:     aws.String(DefaultCIDRBlock),
			FromPort:   aws.Int64(22),
			ToPort:     aws.Int64(22),
		},
		{
			GroupId: aws.String(securityGroupID),
			IpPermissions: []*ec2.IpPermission{
				{
					FromPort:   aws.Int64(4789),
					ToPort:     aws.Int64(4789),
					IpProtocol: aws.String("udp"),
					UserIdGroupPairs: []*ec2.UserIdGroupPair{
						{
							GroupId: aws.String(securityGroupID),
							VpcId:   aws.String(vpcID),
						},
					},
				},
			},
		},
		{
			GroupId: aws.String(securityGroupID),
			IpPermissions: []*ec2.IpPermission{
				{
					FromPort:   aws.Int64(6081),
					ToPort:     aws.Int64(6081),
					IpProtocol: aws.String("udp"),
					UserIdGroupPairs: []*ec2.UserIdGroupPair{
						{
							GroupId: aws.String(securityGroupID),
							VpcId:   aws.String(vpcID),
						},
					},
				},
			},
		},
		{
			GroupId: aws.String(securityGroupID),
			IpPermissions: []*ec2.IpPermission{
				{
					FromPort:   aws.Int64(500),
					ToPort:     aws.Int64(500),
					IpProtocol: aws.String("udp"),
					UserIdGroupPairs: []*ec2.UserIdGroupPair{
						{
							GroupId: aws.String(securityGroupID),
							VpcId:   aws.String(vpcID),
						},
					},
				},
			},
		},
		{
			GroupId: aws.String(securityGroupID),
			IpPermissions: []*ec2.IpPermission{
				{
					FromPort:   aws.Int64(4500),
					ToPort:     aws.Int64(4500),
					IpProtocol: aws.String("udp"),
					UserIdGroupPairs: []*ec2.UserIdGroupPair{
						{
							GroupId: aws.String(securityGroupID),
							VpcId:   aws.String(vpcID),
						},
					},
				},
			},
		},
		{
			GroupId: aws.String(securityGroupID),
			IpPermissions: []*ec2.IpPermission{
				{
					FromPort:   aws.Int64(0),
					ToPort:     aws.Int64(0),
					IpProtocol: aws.String("50"),
					UserIdGroupPairs: []*ec2.UserIdGroupPair{
						{
							GroupId: aws.String(securityGroupID),
							VpcId:   aws.String(vpcID),
						},
					},
				},
			},
		},
		{
			GroupId: aws.String(securityGroupID),
			IpPermissions: []*ec2.IpPermission{
				{
					FromPort:   aws.Int64(9000),
					ToPort:     aws.Int64(9999),
					IpProtocol: aws.String("tcp"),
					UserIdGroupPairs: []*ec2.UserIdGroupPair{
						{
							GroupId: aws.String(securityGroupID),
							VpcId:   aws.String(vpcID),
						},
					},
				},
			},
		},
		{
			GroupId: aws.String(securityGroupID),
			IpPermissions: []*ec2.IpPermission{
				{
					FromPort:   aws.Int64(9000),
					ToPort:     aws.Int64(9999),
					IpProtocol: aws.String("udp"),
					UserIdGroupPairs: []*ec2.UserIdGroupPair{
						{
							GroupId: aws.String(securityGroupID),
							VpcId:   aws.String(vpcID),
						},
					},
				},
			},
		},
		{
			GroupId: aws.String(securityGroupID),
			IpPermissions: []*ec2.IpPermission{
				{
					FromPort:   aws.Int64(10250),
					ToPort:     aws.Int64(10250),
					IpProtocol: aws.String("tcp"),
					UserIdGroupPairs: []*ec2.UserIdGroupPair{
						{
							GroupId: aws.String(securityGroupID),
							VpcId:   aws.String(vpcID),
						},
					},
				},
			},
		},
		{
			GroupId: aws.String(securityGroupID),
			IpPermissions: []*ec2.IpPermission{
				{
					FromPort:   aws.Int64(30000),
					ToPort:     aws.Int64(32767),
					IpProtocol: aws.String("tcp"),
					UserIdGroupPairs: []*ec2.UserIdGroupPair{
						{
							GroupId: aws.String(securityGroupID),
							VpcId:   aws.String(vpcID),
						},
					},
				},
			},
		},
		{
			GroupId: aws.String(securityGroupID),
			IpPermissions: []*ec2.IpPermission{
				{
					FromPort:   aws.Int64(30000),
					ToPort:     aws.Int64(32767),
					IpProtocol: aws.String("udp"),
					UserIdGroupPairs: []*ec2.UserIdGroupPair{
						{
							GroupId: aws.String(securityGroupID),
							VpcId:   aws.String(vpcID),
						},
					},
				},
			},
		},
	}
	for _, ingress := range ingressRules {
		_, err := client.AuthorizeSecurityGroupIngress(ingress)
		if err != nil {
			return "", fmt.Errorf("cannot apply security group ingress rule: %w", err)
		}
	}
	return securityGroupID, nil
}
