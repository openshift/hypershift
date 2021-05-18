package aws

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/url"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/iam"
	"github.com/aws/aws-sdk-go/service/iam/iamiface"
	jose "gopkg.in/square/go-jose.v2"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
)

const (
	// requires Provider ARN and Issuer URL
	oidcTrustPolicyTemplate = `{
		"Version": "2012-10-17",
		"Statement": [
			{
				"Effect": "Allow",
				"Principal": {
					"Federated": "%s"
				},
					"Action": "sts:AssumeRoleWithWebIdentity",
				"Condition": {
					"StringEquals": {
						"%s:sub": "%s"
					}
				}
			}
		]
	}`

	ingressPermPolicy = `{
	"Version": "2012-10-17",
	"Statement": [
		{
			"Effect": "Allow",
			"Action": [
				"elasticloadbalancing:DescribeLoadBalancers",
				"route53:ListHostedZones",
				"route53:ChangeResourceRecordSets",
				"tag:GetResources"
			],
			"Resource": "*"
		}
	]
}`

	imageRegistryPermPolicy = `{
	"Version": "2012-10-17",
	"Statement": [
		{
			"Effect": "Allow",
			"Action": [
				"s3:CreateBucket",
				"s3:DeleteBucket",
				"s3:PutBucketTagging",
				"s3:GetBucketTagging",
				"s3:PutBucketPublicAccessBlock",
				"s3:GetBucketPublicAccessBlock",
				"s3:PutEncryptionConfiguration",
				"s3:GetEncryptionConfiguration",
				"s3:PutLifecycleConfiguration",
				"s3:GetLifecycleConfiguration",
				"s3:GetBucketLocation",
				"s3:ListBucket",
				"s3:GetObject",
				"s3:PutObject",
				"s3:DeleteObject",
				"s3:ListBucketMultipartUploads",
				"s3:AbortMultipartUpload",
				"s3:ListMultipartUploadParts"
			],
			"Resource": "*"
		}
	]
}`

	awsEBSCSIPermPolicy = `{
	"Version": "2012-10-17",
	"Statement": [
		{
			"Effect": "Allow",
			"Action": [
				"ec2:AttachVolume",
				"ec2:CreateSnapshot",
				"ec2:CreateTags",
				"ec2:CreateVolume",
				"ec2:DeleteSnapshot",
				"ec2:DeleteTags",
				"ec2:DeleteVolume",
				"ec2:DescribeInstances",
				"ec2:DescribeSnapshots",
				"ec2:DescribeTags",
				"ec2:DescribeVolumes",
				"ec2:DescribeVolumesModifications",
				"ec2:DetachVolume",
				"ec2:ModifyVolume"
			],
			"Resource": "*"
		}
	]
}`

	cloudControllerPolicy = `{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Action": [
        "ec2:DescribeInstances",
        "ec2:DescribeImages",
        "ec2:DescribeRegions",
        "ec2:DescribeRouteTables",
        "ec2:DescribeSecurityGroups",
        "ec2:DescribeSubnets",
        "ec2:DescribeVolumes",
        "ec2:CreateSecurityGroup",
        "ec2:CreateTags",
        "ec2:CreateVolume",
        "ec2:ModifyInstanceAttribute",
        "ec2:ModifyVolume",
        "ec2:AttachVolume",
        "ec2:AuthorizeSecurityGroupIngress",
        "ec2:CreateRoute",
        "ec2:DeleteRoute",
        "ec2:DeleteSecurityGroup",
        "ec2:DeleteVolume",
        "ec2:DetachVolume",
        "ec2:RevokeSecurityGroupIngress",
        "ec2:DescribeVpcs",
        "elasticloadbalancing:AddTags",
        "elasticloadbalancing:AttachLoadBalancerToSubnets",
        "elasticloadbalancing:ApplySecurityGroupsToLoadBalancer",
        "elasticloadbalancing:CreateLoadBalancer",
        "elasticloadbalancing:CreateLoadBalancerPolicy",
        "elasticloadbalancing:CreateLoadBalancerListeners",
        "elasticloadbalancing:ConfigureHealthCheck",
        "elasticloadbalancing:DeleteLoadBalancer",
        "elasticloadbalancing:DeleteLoadBalancerListeners",
        "elasticloadbalancing:DescribeLoadBalancers",
        "elasticloadbalancing:DescribeLoadBalancerAttributes",
        "elasticloadbalancing:DetachLoadBalancerFromSubnets",
        "elasticloadbalancing:DeregisterInstancesFromLoadBalancer",
        "elasticloadbalancing:ModifyLoadBalancerAttributes",
        "elasticloadbalancing:RegisterInstancesWithLoadBalancer",
        "elasticloadbalancing:SetLoadBalancerPoliciesForBackendServer",
        "elasticloadbalancing:AddTags",
        "elasticloadbalancing:CreateListener",
        "elasticloadbalancing:CreateTargetGroup",
        "elasticloadbalancing:DeleteListener",
        "elasticloadbalancing:DeleteTargetGroup",
        "elasticloadbalancing:DescribeListeners",
        "elasticloadbalancing:DescribeLoadBalancerPolicies",
        "elasticloadbalancing:DescribeTargetGroups",
        "elasticloadbalancing:DescribeTargetHealth",
        "elasticloadbalancing:ModifyListener",
        "elasticloadbalancing:ModifyTargetGroup",
        "elasticloadbalancing:RegisterTargets",
        "elasticloadbalancing:SetLoadBalancerPoliciesOfListener",
        "iam:CreateServiceLinkedRole",
        "kms:DescribeKey"
      ],
      "Resource": [
        "*"
      ],
      "Effect": "Allow"
    }
  ]
}`

	nodePoolPolicy = `{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Action": [
        "ec2:AllocateAddress",
        "ec2:AssociateRouteTable",
        "ec2:AttachInternetGateway",
        "ec2:AuthorizeSecurityGroupIngress",
        "ec2:CreateInternetGateway",
        "ec2:CreateNatGateway",
        "ec2:CreateRoute",
        "ec2:CreateRouteTable",
        "ec2:CreateSecurityGroup",
        "ec2:CreateSubnet",
        "ec2:CreateTags",
        "ec2:DeleteInternetGateway",
        "ec2:DeleteNatGateway",
        "ec2:DeleteRouteTable",
        "ec2:DeleteSecurityGroup",
        "ec2:DeleteSubnet",
        "ec2:DeleteTags",
        "ec2:DescribeAccountAttributes",
        "ec2:DescribeAddresses",
        "ec2:DescribeAvailabilityZones",
        "ec2:DescribeInstances",
        "ec2:DescribeInternetGateways",
        "ec2:DescribeNatGateways",
        "ec2:DescribeNetworkInterfaces",
        "ec2:DescribeNetworkInterfaceAttribute",
        "ec2:DescribeRouteTables",
        "ec2:DescribeSecurityGroups",
        "ec2:DescribeSubnets",
        "ec2:DescribeVpcs",
        "ec2:DescribeVpcAttribute",
        "ec2:DescribeVolumes",
        "ec2:DetachInternetGateway",
        "ec2:DisassociateRouteTable",
        "ec2:DisassociateAddress",
        "ec2:ModifyInstanceAttribute",
        "ec2:ModifyNetworkInterfaceAttribute",
        "ec2:ModifySubnetAttribute",
        "ec2:ReleaseAddress",
        "ec2:RevokeSecurityGroupIngress",
        "ec2:RunInstances",
        "ec2:TerminateInstances",
        "tag:GetResources",
        "ec2:CreateLaunchTemplate",
        "ec2:CreateLaunchTemplateVersion",
        "ec2:DescribeLaunchTemplates",
        "ec2:DescribeLaunchTemplateVersions",
        "ec2:DeleteLaunchTemplate",
        "ec2:DeleteLaunchTemplateVersions"
      ],
      "Resource": [
        "*"
      ],
      "Effect": "Allow"
    },
    {
      "Condition": {
        "StringLike": {
          "iam:AWSServiceName": "elasticloadbalancing.amazonaws.com"
        }
      },
      "Action": [
        "iam:CreateServiceLinkedRole"
      ],
      "Resource": [
        "arn:*:iam::*:role/aws-service-role/elasticloadbalancing.amazonaws.com/AWSServiceRoleForElasticLoadBalancing"
      ],
      "Effect": "Allow"
    },
    {
      "Action": [
        "iam:PassRole"
      ],
      "Resource": [
        "arn:*:iam::*:role/*-worker-role"
      ],
      "Effect": "Allow"
    }
  ]
}`
)

type KeyResponse struct {
	Keys []jose.JSONWebKey `json:"keys"`
}

func DefaultProfileName(infraID string) string {
	return infraID + "-worker"
}

// inputs: none
// outputs rsa keypair
func (o *CreateIAMOptions) CreateOIDCResources(iamClient iamiface.IAMAPI) (*CreateIAMOutput, error) {
	output := &CreateIAMOutput{
		Region:    o.Region,
		InfraID:   o.InfraID,
		IssuerURL: o.IssuerURL,
	}

	// Create the service account signing key
	privKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return nil, err
	}

	output.ServiceAccountSigningKey = pem.EncodeToMemory(&pem.Block{
		Type:    "RSA PRIVATE KEY",
		Headers: nil,
		Bytes:   x509.MarshalPKCS1PrivateKey(privKey),
	})

	// Discover the thumbprint for the CA on the OIDC discovery endpoint
	url, err := url.Parse(o.IssuerURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse issuer URL: %w", err)
	}
	if url.Scheme != "https" {
		return nil, fmt.Errorf("issuer URL must be https")
	}
	providerName := url.Host + url.Path
	conn, err := tls.Dial("tcp", fmt.Sprintf("%s:443", url.Host), &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		return nil, fmt.Errorf("failed to determine CA thumbprint for OIDC discovery endpoint: %w", err)
	}
	certs := conn.ConnectionState().PeerCertificates
	cert := certs[len(certs)-1]
	thumbprint := fmt.Sprintf("%x", sha1.Sum(cert.Raw))
	conn.Close()
	log.Info("OIDC CA thumbprint discovered", "thumbprint", thumbprint)

	// Create the OIDC provider
	oidcProviderList, err := iamClient.ListOpenIDConnectProviders(&iam.ListOpenIDConnectProvidersInput{})
	if err != nil {
		return nil, err
	}

	for _, provider := range oidcProviderList.OpenIDConnectProviderList {
		if strings.Contains(*provider.Arn, providerName) {
			_, err := iamClient.DeleteOpenIDConnectProvider(&iam.DeleteOpenIDConnectProviderInput{
				OpenIDConnectProviderArn: provider.Arn,
			})
			if err != nil {
				log.Error(err, "Failed to remove existing OIDC provider", "provider", *provider.Arn)
				return nil, err
			}
			log.Info("Removing existing OIDC provider", "provider", *provider.Arn)
			break
		}
	}

	oidcOutput, err := iamClient.CreateOpenIDConnectProvider(&iam.CreateOpenIDConnectProviderInput{
		ClientIDList: []*string{
			aws.String("openshift"),
		},
		ThumbprintList: []*string{
			aws.String(thumbprint),
		},
		Url: aws.String(o.IssuerURL),
	})
	if err != nil {
		return nil, err
	}

	providerARN := *oidcOutput.OpenIDConnectProviderArn
	log.Info("Created OIDC provider", "provider", providerARN)

	// TODO: The policies and secrets for these roles can be extracted from the
	// release payload, avoiding this current hardcoding.
	ingressTrustPolicy := fmt.Sprintf(oidcTrustPolicyTemplate, providerARN, providerName, "system:serviceaccount:openshift-ingress-operator:ingress-operator")
	arn, err := o.CreateOIDCRole(iamClient, "openshift-ingress", ingressTrustPolicy, ingressPermPolicy)
	if err != nil {
		return nil, err
	}
	output.Roles = append(output.Roles, hyperv1.AWSRoleCredentials{
		ARN:       arn,
		Namespace: "openshift-ingress-operator",
		Name:      "cloud-credentials",
	})

	registryTrustPolicy := fmt.Sprintf(oidcTrustPolicyTemplate, providerARN, providerName, "system:serviceaccount:openshift-image-registry:cluster-image-registry-operator")
	arn, err = o.CreateOIDCRole(iamClient, "openshift-image-registry", registryTrustPolicy, imageRegistryPermPolicy)
	if err != nil {
		return nil, err
	}
	output.Roles = append(output.Roles, hyperv1.AWSRoleCredentials{
		ARN:       arn,
		Namespace: "openshift-image-registry",
		Name:      "installer-cloud-credentials",
	})

	csiTrustPolicy := fmt.Sprintf(oidcTrustPolicyTemplate, providerARN, providerName, "system:serviceaccount:openshift-cluster-csi-drivers:aws-ebs-csi-driver-operator")
	arn, err = o.CreateOIDCRole(iamClient, "aws-ebs-csi-driver-operator", csiTrustPolicy, awsEBSCSIPermPolicy)
	if err != nil {
		return nil, err
	}
	output.Roles = append(output.Roles, hyperv1.AWSRoleCredentials{
		ARN:       arn,
		Namespace: "openshift-cluster-csi-drivers",
		Name:      "ebs-cloud-credentials",
	})

	return output, nil
}

// CreateOIDCRole create an IAM Role with a trust policy for the OIDC provider
func (o *CreateIAMOptions) CreateOIDCRole(client iamiface.IAMAPI, name, trustPolicy, permPolicy string) (string, error) {
	roleName := fmt.Sprintf("%s-%s", o.InfraID, name)
	role, err := existingRole(client, roleName)
	var arn string
	if err != nil {
		return "", err
	}
	if role == nil {
		output, err := client.CreateRole(&iam.CreateRoleInput{
			AssumeRolePolicyDocument: aws.String(trustPolicy),
			RoleName:                 aws.String(roleName),
		})
		if err != nil {
			return "", err
		}
		log.Info("Created role", "name", roleName)
		arn = *output.Role.Arn
	} else {
		log.Info("Found existing role", "name", roleName)
		arn = *role.Arn
	}

	rolePolicyName := roleName
	hasPolicy, err := existingRolePolicy(client, roleName, rolePolicyName)
	if err != nil {
		return "", err
	}
	if !hasPolicy {
		_, err = client.PutRolePolicy(&iam.PutRolePolicyInput{
			PolicyName:     aws.String(rolePolicyName),
			PolicyDocument: aws.String(permPolicy),
			RoleName:       aws.String(roleName),
		})
		if err != nil {
			return "", err
		}
		log.Info("Created role policy", "name", rolePolicyName)
	}

	return arn, nil
}

func (o *CreateIAMOptions) CreateWorkerInstanceProfile(client iamiface.IAMAPI, profileName string) error {
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
	roleName := fmt.Sprintf("%s-role", profileName)
	role, err := existingRole(client, roleName)
	if err != nil {
		return err
	}
	if role == nil {
		_, err := client.CreateRole(&iam.CreateRoleInput{
			AssumeRolePolicyDocument: aws.String(assumeRolePolicy),
			Path:                     aws.String("/"),
			RoleName:                 aws.String(roleName),
		})
		if err != nil {
			return fmt.Errorf("cannot create worker role: %w", err)
		}
		log.Info("Created role", "name", roleName)
	} else {
		log.Info("Found existing role", "name", roleName)
	}
	instanceProfile, err := existingInstanceProfile(client, profileName)
	if err != nil {
		return err
	}
	if instanceProfile == nil {
		result, err := client.CreateInstanceProfile(&iam.CreateInstanceProfileInput{
			InstanceProfileName: aws.String(profileName),
			Path:                aws.String("/"),
		})
		if err != nil {
			return fmt.Errorf("cannot create instance profile: %w", err)
		}
		instanceProfile = result.InstanceProfile
		log.Info("Created instance profile", "name", profileName)
	} else {
		log.Info("Found existing instance profile", "name", profileName)
	}
	hasRole := false
	for _, role := range instanceProfile.Roles {
		if aws.StringValue(role.RoleName) == aws.StringValue(role.RoleName) {
			hasRole = true
		}
	}
	if !hasRole {
		_, err = client.AddRoleToInstanceProfile(&iam.AddRoleToInstanceProfileInput{
			InstanceProfileName: aws.String(profileName),
			RoleName:            aws.String(roleName),
		})
		if err != nil {
			return fmt.Errorf("cannot add role to instance profile: %w", err)
		}
		log.Info("Added role to instance profile", "role", roleName, "profile", profileName)
	}
	rolePolicyName := fmt.Sprintf("%s-policy", profileName)
	hasPolicy, err := existingRolePolicy(client, roleName, rolePolicyName)
	if err != nil {
		return err
	}
	if !hasPolicy {
		_, err = client.PutRolePolicy(&iam.PutRolePolicyInput{
			PolicyName:     aws.String(rolePolicyName),
			PolicyDocument: aws.String(workerPolicy),
			RoleName:       aws.String(roleName),
		})
		if err != nil {
			return fmt.Errorf("cannot create profile policy: %w", err)
		}
		log.Info("Created role policy", "name", rolePolicyName)
	}
	return nil
}

func (o *CreateIAMOptions) CreateCredentialedUserWithPolicy(ctx context.Context, client iamiface.IAMAPI, userName, policyDocument string) (*iam.AccessKey, error) {
	var user *iam.User
	user, err := existingUser(client, userName)
	if err != nil {
		return nil, err
	}
	if user != nil {
		log.Info("Found existing user", "user", userName)

		// Clean up any old access keys since we can only have 2 per user by quota
		// This is best effort and errors are ignored
		if output, err := client.ListAccessKeysWithContext(ctx, &iam.ListAccessKeysInput{
			UserName: aws.String(userName),
		}); err == nil {
			for _, key := range output.AccessKeyMetadata {
				if _, err := client.DeleteAccessKeyWithContext(ctx, &iam.DeleteAccessKeyInput{
					AccessKeyId: key.AccessKeyId,
					UserName:    key.UserName,
				}); err == nil {
					log.Info("Deleted old access key", "id", key.AccessKeyId, "user", userName)
				}
			}
		}
	} else {
		if output, err := client.CreateUserWithContext(ctx, &iam.CreateUserInput{
			UserName: aws.String(userName),
			Tags:     iamTags(o.InfraID, userName),
		}); err != nil {
			return nil, fmt.Errorf("failed to create user: %w", err)
		} else {
			user = output.User
		}
		log.Info("Created user", "user", userName)
	}

	policyName := userName
	hasPolicy, err := existingUserPolicy(client, userName, userName)
	if err != nil {
		return nil, err
	}
	if hasPolicy {
		log.Info("Found existing user policy", "user", userName)
	} else {
		_, err := client.PutUserPolicyWithContext(ctx, &iam.PutUserPolicyInput{
			PolicyName:     aws.String(policyName),
			PolicyDocument: aws.String(policyDocument),
			UserName:       aws.String(userName),
		})
		if err != nil {
			return nil, err
		}
		log.Info("Created user policy", "user", userName)
	}

	// We create a new access key regardless as there is no way to get access to existing keys
	if output, err := client.CreateAccessKeyWithContext(ctx, &iam.CreateAccessKeyInput{
		UserName: user.UserName,
	}); err != nil {
		return nil, fmt.Errorf("failed to create access key: %w", err)
	} else {
		log.Info("Created access key", "user", aws.StringValue(user.UserName))
		return output.AccessKey, nil
	}
}

func existingRole(client iamiface.IAMAPI, roleName string) (*iam.Role, error) {
	result, err := client.GetRole(&iam.GetRoleInput{RoleName: aws.String(roleName)})
	if err != nil {
		if awsErr, ok := err.(awserr.Error); ok {
			if awsErr.Code() == iam.ErrCodeNoSuchEntityException {
				return nil, nil
			}
		}
		return nil, fmt.Errorf("cannot get existing role: %w", err)
	}
	return result.Role, nil
}

func existingUser(client iamiface.IAMAPI, userName string) (*iam.User, error) {
	result, err := client.GetUser(&iam.GetUserInput{UserName: aws.String(userName)})
	if err != nil {
		if awsErr, ok := err.(awserr.Error); ok {
			if awsErr.Code() == iam.ErrCodeNoSuchEntityException {
				return nil, nil
			}
		}
		return nil, fmt.Errorf("cannot get existing role: %w", err)
	}
	return result.User, nil
}

func existingInstanceProfile(client iamiface.IAMAPI, profileName string) (*iam.InstanceProfile, error) {
	result, err := client.GetInstanceProfile(&iam.GetInstanceProfileInput{
		InstanceProfileName: aws.String(profileName),
	})
	if err != nil {
		if awsErr, ok := err.(awserr.Error); ok {
			if awsErr.Code() == iam.ErrCodeNoSuchEntityException {
				return nil, nil
			}
		}
		return nil, fmt.Errorf("cannot get existing instance profile: %w", err)
	}
	return result.InstanceProfile, nil
}

func existingRolePolicy(client iamiface.IAMAPI, roleName, policyName string) (bool, error) {
	result, err := client.GetRolePolicy(&iam.GetRolePolicyInput{
		RoleName:   aws.String(roleName),
		PolicyName: aws.String(policyName),
	})
	if err != nil {
		if awsErr, ok := err.(awserr.Error); ok {
			if awsErr.Code() == iam.ErrCodeNoSuchEntityException {
				return false, nil
			}
		}
		return false, fmt.Errorf("cannot get existing role policy: %w", err)
	}
	return aws.StringValue(result.PolicyName) == policyName, nil
}

func existingUserPolicy(client iamiface.IAMAPI, userName, policyName string) (bool, error) {
	result, err := client.GetUserPolicy(&iam.GetUserPolicyInput{
		UserName:   aws.String(userName),
		PolicyName: aws.String(policyName),
	})
	if err != nil {
		if awsErr, ok := err.(awserr.Error); ok {
			if awsErr.Code() == iam.ErrCodeNoSuchEntityException {
				return false, nil
			}
		}
		return false, fmt.Errorf("cannot get existing user policy: %w", err)
	}
	return aws.StringValue(result.PolicyName) == policyName, nil
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
