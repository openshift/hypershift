package aws

import (
	"fmt"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/iam"
	"github.com/aws/aws-sdk-go/service/iam/iamiface"
)

func (o *CreateInfraOptions) CreateWorkerInstanceProfile(client iamiface.IAMAPI, profileName string) error {
	const (
		assumeRolePolicy = `{
    "Version": "2012-10-17",
    "Statement": [
        {
            "Action": "sts:AssumeRole",
            "Principal": {
                "Service": "ec2.amazonaws.com"
            },
            "Effect": "Allow",
            "Sid": ""
        }
    ]
}`
		workerPolicy = `{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "ec2:DescribeInstances",
        "ec2:DescribeRegions"
      ],
      "Resource": "*"
    }
  ]
}`
	)
	roleName := fmt.Sprintf("%s-worker-role", o.InfraID)
	_, err := client.CreateRole(&iam.CreateRoleInput{
		AssumeRolePolicyDocument: aws.String(assumeRolePolicy),
		Path:                     aws.String("/"),
		RoleName:                 aws.String(roleName),
		Tags:                     iamTags(o.InfraID, roleName),
	})
	if err != nil {
		return fmt.Errorf("cannot create worker role: %w", err)
	}
	_, err = client.CreateInstanceProfile(&iam.CreateInstanceProfileInput{
		InstanceProfileName: aws.String(profileName),
		Path:                aws.String("/"),
	})
	if err != nil {
		return fmt.Errorf("cannot create worker instance profile: %w", err)
	}
	_, err = client.AddRoleToInstanceProfile(&iam.AddRoleToInstanceProfileInput{
		InstanceProfileName: aws.String(profileName),
		RoleName:            aws.String(roleName),
	})
	if err != nil {
		return fmt.Errorf("cannot add role to worker instance profile: %w", err)
	}
	_, err = client.PutRolePolicy(&iam.PutRolePolicyInput{
		PolicyName:     aws.String(fmt.Sprintf("%s-worker-policy", o.InfraID)),
		PolicyDocument: aws.String(workerPolicy),
		RoleName:       aws.String(roleName),
	})
	if err != nil {
		return fmt.Errorf("cannot create worker policy: %w", err)
	}
	return nil
}

func iamTags(infraID, name string) []*iam.Tag {
	tags := []*iam.Tag{
		{
			Key:   aws.String(fmt.Sprintf("kubernetes.io/cluster/%s", infraID)),
			Value: aws.String("owned"),
		},
	}
	if len(name) > 0 {
		tags = append(tags, &iam.Tag{
			Key:   aws.String("Name"),
			Value: aws.String(name),
		})
	}
	return tags
}
