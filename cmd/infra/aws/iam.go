package aws

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/iam"
	"github.com/aws/aws-sdk-go/service/iam/iamiface"
	jose "gopkg.in/square/go-jose.v2"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
)

const (
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
        "ec2:DescribeImages",
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

	controlPlaneOperatorPolicy = `{
	"Version": "2012-10-17",
	"Statement": [
		{
			"Effect": "Allow",
			"Action": [
				"ec2:CreateVpcEndpoint",
				"ec2:DescribeVpcEndpoints",
				"ec2:DeleteVpcEndpoints",
				"ec2:CreateTags",
				"route53:ListHostedZones",
				"route53:ChangeResourceRecordSets",
				"route53:ListResourceRecordSets"
			],
			"Resource": "*"
		}
	]
}`
	cloudNetworkConfigControllerPolicy = `{
	"Version": "2012-10-17",
	"Statement": [
		{
			"Effect": "Allow",
			"Action": [
				"ec2:DescribeInstances",
        "ec2:DescribeInstanceStatus",
        "ec2:DescribeInstanceTypes",
        "ec2:UnassignPrivateIpAddresses",
        "ec2:AssignPrivateIpAddresses",
        "ec2:UnassignIpv6Addresses",
        "ec2:AssignIpv6Addresses",
        "ec2:DescribeSubnets",
        "ec2:DescribeNetworkInterfaces"
			],
			"Resource": "*"
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

	// Create the OIDC provider
	oidcProviderList, err := iamClient.ListOpenIDConnectProviders(&iam.ListOpenIDConnectProvidersInput{})
	if err != nil {
		return nil, err
	}

	providerName := strings.TrimPrefix(o.IssuerURL, "https://")
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
		// The AWS console mentions that this will be ignored for S3 buckets but creation fails if we don't
		// pass a thumbprint.
		ThumbprintList: []*string{
			aws.String("A9D53002E97E00E043244F3D170D6F4C414104FD"), // root CA thumbprint for s3 (DigiCert)
		},
		Url:  aws.String(o.IssuerURL),
		Tags: o.additionalIAMTags,
	})
	if err != nil {
		return nil, err
	}

	providerARN := *oidcOutput.OpenIDConnectProviderArn
	log.Info("Created OIDC provider", "provider", providerARN)

	// TODO: The policies and secrets for these roles can be extracted from the
	// release payload, avoiding this current hardcoding.
	ingressTrustPolicy := oidcTrustPolicy(providerARN, providerName, "system:serviceaccount:openshift-ingress-operator:ingress-operator")
	arn, err := o.CreateOIDCRole(iamClient, "openshift-ingress", ingressTrustPolicy, ingressPermPolicy)
	if err != nil {
		return nil, err
	}
	output.Roles = append(output.Roles, hyperv1.AWSRoleCredentials{
		ARN:       arn,
		Namespace: "openshift-ingress-operator",
		Name:      "cloud-credentials",
	})

	registryTrustPolicy := oidcTrustPolicy(providerARN, providerName,
		"system:serviceaccount:openshift-image-registry:cluster-image-registry-operator",
		"system:serviceaccount:openshift-image-registry:registry")
	arn, err = o.CreateOIDCRole(iamClient, "openshift-image-registry", registryTrustPolicy, imageRegistryPermPolicy)
	if err != nil {
		return nil, err
	}
	output.Roles = append(output.Roles, hyperv1.AWSRoleCredentials{
		ARN:       arn,
		Namespace: "openshift-image-registry",
		Name:      "installer-cloud-credentials",
	})

	csiTrustPolicy := oidcTrustPolicy(providerARN, providerName, "system:serviceaccount:openshift-cluster-csi-drivers:aws-ebs-csi-driver-operator")
	arn, err = o.CreateOIDCRole(iamClient, "aws-ebs-csi-driver-operator", csiTrustPolicy, awsEBSCSIPermPolicy)
	if err != nil {
		return nil, err
	}
	output.Roles = append(output.Roles, hyperv1.AWSRoleCredentials{
		ARN:       arn,
		Namespace: "openshift-cluster-csi-drivers",
		Name:      "ebs-cloud-credentials",
	})

	kubeCloudControllerTrustPolicy := oidcTrustPolicy(providerARN, providerName, "system:serviceaccount:kube-system:kube-controller-manager")
	arn, err = o.CreateOIDCRole(iamClient, "cloud-controller", kubeCloudControllerTrustPolicy, cloudControllerPolicy)
	if err != nil {
		return nil, err
	}
	output.KubeCloudControllerRoleARN = arn

	nodePoolManagementTrustPolicy := oidcTrustPolicy(providerARN, providerName, "system:serviceaccount:kube-system:capa-controller-manager")
	arn, err = o.CreateOIDCRole(iamClient, "node-pool", nodePoolManagementTrustPolicy, nodePoolPolicy)
	if err != nil {
		return nil, err
	}
	output.NodePoolManagementRoleARN = arn

	controlPlaneOperatorTrustPolicy := oidcTrustPolicy(providerARN, providerName, "system:serviceaccount:kube-system:control-plane-operator")
	arn, err = o.CreateOIDCRole(iamClient, "control-plane-operator", controlPlaneOperatorTrustPolicy, controlPlaneOperatorPolicy)
	if err != nil {
		return nil, err
	}
	output.ControlPlaneOperatorRoleARN = arn

	cloudNetworkConfigControllerTrustPolicy := oidcTrustPolicy(providerARN, providerName, "system:serviceaccount:openshift-cloud-network-config-controller:cloud-network-config-controller")
	arn, err = o.CreateOIDCRole(iamClient, "cloud-network-config-controller", cloudNetworkConfigControllerTrustPolicy, cloudNetworkConfigControllerPolicy)
	if err != nil {
		return nil, err
	}
	output.Roles = append(output.Roles, hyperv1.AWSRoleCredentials{
		ARN:       arn,
		Namespace: "openshift-cloud-network-config-controller",
		Name:      "cloud-credentials",
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
			Tags:                     o.additionalIAMTags,
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
			Tags:                     o.additionalIAMTags,
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
			Tags:                o.additionalIAMTags,
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
		if aws.StringValue(role.RoleName) == roleName {
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

type oidcTrustPolicyParams struct {
	ProviderARN     string
	ProviderName    string
	ServiceAccounts string
}

const (
	oidcTrustPolicyTemplate = `{
		"Version": "2012-10-17",
		"Statement": [
			{
				"Effect": "Allow",
				"Principal": {
					"Federated": "{{ .ProviderARN }}"
				},
					"Action": "sts:AssumeRoleWithWebIdentity",
				"Condition": {
					"StringEquals": {
						"{{ .ProviderName }}:sub": {{ .ServiceAccounts }}
					}
				}
			}
		]
	}`
)

func oidcTrustPolicy(providerARN, providerName string, serviceAccounts ...string) string {
	params := oidcTrustPolicyParams{
		ProviderARN:  providerARN,
		ProviderName: providerName,
	}
	if len(serviceAccounts) == 1 {
		params.ServiceAccounts = fmt.Sprintf("%q", serviceAccounts[0])
	} else {
		sas := &bytes.Buffer{}
		fmt.Fprintf(sas, "[")
		for i, sa := range serviceAccounts {
			fmt.Fprintf(sas, "%q", sa)
			if i < len(serviceAccounts)-1 {
				fmt.Fprintf(sas, ", ")
			}
		}
		fmt.Fprintf(sas, "]")
		params.ServiceAccounts = sas.String()
	}

	tmpl, err := template.New("oidcTrustPolicy").Parse(oidcTrustPolicyTemplate)
	if err != nil {
		panic(fmt.Sprintf("programmer error, oidcTrustPolicyTemplate failed to parse: %v", err))
	}
	b := &bytes.Buffer{}
	if err = tmpl.Execute(b, params); err != nil {
		panic(fmt.Sprintf("failed to execute oidcTrustPolicyTemplate: %v", err))
	}
	return b.String()
}
