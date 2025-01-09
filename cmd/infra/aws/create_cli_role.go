package aws

import (
	"context"
	"fmt"

	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	"github.com/openshift/hypershift/cmd/log"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/iam"
	"github.com/aws/aws-sdk-go/service/sts"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
)

const (
	cliRolePolicy = `{
		"Version": "2012-10-17",
		"Statement": [
			{
				"Sid": "EC2",
				"Effect": "Allow",
				"Action": [
					"ec2:CreateDhcpOptions",
					"ec2:DeleteSubnet",
					"ec2:ReplaceRouteTableAssociation",
					"ec2:DescribeAddresses",
					"ec2:DescribeInstances",
					"ec2:DeleteVpcEndpoints",
					"ec2:CreateNatGateway",
					"ec2:CreateVpc",
					"ec2:DescribeDhcpOptions",
					"ec2:AttachInternetGateway",
					"ec2:DeleteVpcEndpointServiceConfigurations",
					"ec2:DeleteRouteTable",
					"ec2:AssociateRouteTable",
					"ec2:DescribeInternetGateways",
					"ec2:DescribeAvailabilityZones",
					"ec2:CreateRoute",
					"ec2:CreateInternetGateway",
					"ec2:RevokeSecurityGroupEgress",
					"ec2:ModifyVpcAttribute",
					"ec2:DeleteInternetGateway",
					"ec2:DescribeVpcEndpointConnections",
					"ec2:RejectVpcEndpointConnections",
					"ec2:DescribeRouteTables",
					"ec2:ReleaseAddress",
					"ec2:AssociateDhcpOptions",
					"ec2:TerminateInstances",
					"ec2:CreateTags",
					"ec2:DeleteRoute",
					"ec2:CreateRouteTable",
					"ec2:DetachInternetGateway",
					"ec2:DescribeVpcEndpointServiceConfigurations",
					"ec2:DescribeNatGateways",
					"ec2:DisassociateRouteTable",
					"ec2:AllocateAddress",
					"ec2:DescribeSecurityGroups",
					"ec2:RevokeSecurityGroupIngress",
					"ec2:CreateVpcEndpoint",
					"ec2:DescribeVpcs",
					"ec2:DeleteSecurityGroup",
					"ec2:DeleteDhcpOptions",
					"ec2:DeleteNatGateway",
					"ec2:DescribeVpcEndpoints",
					"ec2:DeleteVpc",
					"ec2:CreateSubnet",
					"ec2:DescribeSubnets"
				],
				"Resource": "*"
			},
			{
				"Sid": "ELB",
				"Effect": "Allow",
				"Action": [
					"elasticloadbalancing:DeleteLoadBalancer",
					"elasticloadbalancing:DescribeLoadBalancers",
					"elasticloadbalancing:DescribeTargetGroups",
					"elasticloadbalancing:DeleteTargetGroup"
				],
				"Resource": "*"
			},
			{
				"Sid": "IAMPassRole",
				"Effect": "Allow",
				"Action": "iam:PassRole",
				"Resource": "arn:*:iam::*:role/*-worker-role",
				"Condition": {
					"ForAnyValue:StringEqualsIfExists": {
						"iam:PassedToService": "ec2.amazonaws.com"
					}
				}
			},
			{
				"Sid": "IAM",
				"Effect": "Allow",
				"Action": [
					"iam:CreateInstanceProfile",
					"iam:DeleteInstanceProfile",
					"iam:TagInstanceProfile",
					"iam:GetRole",
					"iam:UpdateAssumeRolePolicy",
					"iam:GetInstanceProfile",
					"iam:TagRole",
					"iam:RemoveRoleFromInstanceProfile",
					"iam:CreateRole",
					"iam:DeleteRole",
					"iam:PutRolePolicy",
					"iam:AddRoleToInstanceProfile",
					"iam:CreateOpenIDConnectProvider",
					"iam:TagOpenIDConnectProvider",
					"iam:ListOpenIDConnectProviders",
					"iam:DeleteRolePolicy",
					"iam:UpdateRole",
					"iam:DeleteOpenIDConnectProvider",
					"iam:GetRolePolicy"
				],
				"Resource": "*"
			},
			{
				"Sid": "Route53",
				"Effect": "Allow",
				"Action": [
					"route53:ListHostedZonesByVPC",
					"route53:CreateHostedZone",
					"route53:ListHostedZones",
					"route53:ChangeResourceRecordSets",
					"route53:ListResourceRecordSets",
					"route53:DeleteHostedZone",
					"route53:AssociateVPCWithHostedZone",
					"route53:ListHostedZonesByName"
				],
				"Resource": "*"
			},
			{
				"Sid": "S3",
				"Effect": "Allow",
				"Action": [
					"s3:ListAllMyBuckets",
					"s3:ListBucket",
					"s3:DeleteObject",
					"s3:DeleteBucket"
				],
				"Resource": "*"
			}
		]
	}`
)

type CreateCLIRoleOptions struct {
	AWSCredentialsFile string
	RoleName           string
	AdditionalTags     map[string]string
}

func NewCreateCLIRoleCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "cli-role",
		Short:        "Creates AWS IAM role for the CLI to assume",
		SilenceUsage: true,
	}

	opts := CreateCLIRoleOptions{
		AWSCredentialsFile: "",
		RoleName:           "hypershift-cli-role",
	}

	cmd.Flags().StringVar(&opts.AWSCredentialsFile, "aws-creds", opts.AWSCredentialsFile, "Path to an AWS credentials file (required)")
	cmd.Flags().StringVar(&opts.RoleName, "name", opts.RoleName, "Role name")
	cmd.Flags().StringToStringVarP(&opts.AdditionalTags, "additional-tags", "t", opts.AdditionalTags, "Additional tags to apply to the role created (e.g. 'key1=value1,key2=value2')")

	cmd.MarkFlagRequired("aws-creds")

	logger := log.Log
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := opts.Run(cmd.Context(), logger); err != nil {
			logger.Error(err, "failed to create cli role")
			return err
		}
		return nil
	}

	return cmd
}

func (o *CreateCLIRoleOptions) Run(ctx context.Context, logger logr.Logger) error {
	tags, err := o.ParseAdditionalTags()
	if err != nil {
		return err
	}

	awsSession := awsutil.NewSession("cli-create-role", o.AWSCredentialsFile, "", "", "")
	awsConfig := awsutil.NewConfig()
	iamClient := iam.New(awsSession, awsConfig)
	stsClient := sts.New(awsSession, awsConfig)

	trustPolicy, err := assumeRoleTrustPolicy(ctx, stsClient)
	if err != nil {
		return err
	}

	createIAMRoleOpts := CreateIAMRoleOptions{
		RoleName:          o.RoleName,
		TrustPolicy:       trustPolicy,
		PermissionsPolicy: cliRolePolicy,
		additionalIAMTags: tags,
	}

	roleArn, err := createIAMRoleOpts.CreateRoleWithInlinePolicy(ctx, logger, iamClient)
	if err != nil {
		return err
	}

	fmt.Printf("Successfully created/updated role %s, arn: %s\n", o.RoleName, roleArn)
	return nil
}

func assumeRoleTrustPolicy(ctx context.Context, client *sts.STS) (string, error) {
	identity, err := client.GetCallerIdentityWithContext(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return "", err
	}

	assumeRolePolicy := fmt.Sprintf(`{
		"Version": "2012-10-17",
		"Statement": [
			{
				"Effect": "Allow",
				"Principal": {
					"AWS": "%s"
				},
				"Action": "sts:AssumeRole"
			}
		]
	}`, *identity.Arn)

	return assumeRolePolicy, nil
}

func (o *CreateCLIRoleOptions) ParseAdditionalTags() ([]*iam.Tag, error) {
	additionalIAMTags := make([]*iam.Tag, 0, len(o.AdditionalTags))
	for k, v := range o.AdditionalTags {
		additionalIAMTags = append(additionalIAMTags, &iam.Tag{
			Key:   aws.String(k),
			Value: aws.String(v),
		})
	}
	return additionalIAMTags, nil
}
