package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	cmdutil "github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/support/awsapi"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
)

const (
	CredentialSourceWebIdentity         = "web-identity"
	CredentialSourceEC2InstanceMetadata = "ec2-instance-metadata"
)

type CreateOperatorRolesOptions struct {
	AWSCredentialsOpts awsutil.AWSCredentialsOptions
	Region             string
	NamePrefix         string

	OIDCIssuerURL   string
	InstanceRoleARN string

	OIDCStorageProviderS3BucketName string
	Route53HostedZoneID             string
	OperatorNamespace               string
	OutputFile                      string
	AdditionalTags                  []string

	additionalIAMTags []iamtypes.Tag
}

type CreateOperatorRolesOutput struct {
	OperatorEC2RoleARN    string `json:"operatorEC2RoleARN"`
	OperatorOIDCS3RoleARN string `json:"operatorOIDCS3RoleARN"`
	ExternalDNSRoleARN    string `json:"externalDNSRoleARN"`
}

func NewCreateOperatorRolesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "aws",
		Short:        "Creates AWS IAM roles for the HyperShift operator and external-dns",
		SilenceUsage: true,
	}

	opts := CreateOperatorRolesOptions{
		NamePrefix:        "hypershift",
		OperatorNamespace: "hypershift",
	}

	cmd.Flags().StringVar(&opts.Region, "region", opts.Region, "AWS region (defaults to AWS_REGION, AWS_DEFAULT_REGION, or ~/.aws/config)")
	cmd.Flags().StringVar(&opts.NamePrefix, "name-prefix", opts.NamePrefix, "Prefix for IAM role names")
	cmd.Flags().StringVar(&opts.OIDCIssuerURL, "oidc-issuer-url", "", "Management cluster OIDC issuer URL for web identity trust (auto-discovered from cluster if not set). Mutually exclusive with --instance-role-arn.")
	cmd.Flags().StringVar(&opts.InstanceRoleARN, "instance-role-arn", "", "ARN of the instance role to trust via sts:AssumeRole (for clusters without an OIDC provider). Mutually exclusive with --oidc-issuer-url.")
	cmd.Flags().StringVar(&opts.OIDCStorageProviderS3BucketName, "oidc-storage-provider-s3-bucket-name", "", "S3 bucket name for OIDC documents (scopes the S3 policy)")
	cmd.Flags().StringVar(&opts.Route53HostedZoneID, "route53-hosted-zone-id", "", "Route53 hosted zone ID to scope the external-dns policy (defaults to all zones)")
	cmd.Flags().StringVar(&opts.OperatorNamespace, "operator-namespace", opts.OperatorNamespace, "Namespace where the HyperShift operator is installed")
	cmd.Flags().StringVar(&opts.OutputFile, "output-file", "", "Path to file for JSON output (optional, defaults to stdout)")
	cmd.Flags().StringSliceVar(&opts.AdditionalTags, "additional-tags", nil, "Additional tags to set on IAM resources (key=value)")

	opts.AWSCredentialsOpts.BindFlags(cmd.Flags())

	_ = cmd.MarkFlagRequired("oidc-storage-provider-s3-bucket-name")

	logger := cmdutil.NewLogger()
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := opts.Validate(cmd.Context()); err != nil {
			return err
		}
		return opts.Run(cmd.Context(), logger)
	}

	return cmd
}

func (o *CreateOperatorRolesOptions) Validate(ctx context.Context) error {
	if o.OIDCIssuerURL != "" && o.InstanceRoleARN != "" {
		return fmt.Errorf("--oidc-issuer-url and --instance-role-arn are mutually exclusive")
	}
	if o.OIDCIssuerURL == "" && o.InstanceRoleARN == "" {
		// Auto-discover OIDC issuer from management cluster
		client, err := cmdutil.GetClient()
		if err != nil {
			return fmt.Errorf("no --oidc-issuer-url or --instance-role-arn specified, and failed to connect to cluster for auto-discovery: %w", err)
		}
		issuer, err := discoverOIDCIssuerURL(ctx, client)
		if err != nil {
			return fmt.Errorf("no --oidc-issuer-url or --instance-role-arn specified, and auto-discovery failed: %w", err)
		}
		o.OIDCIssuerURL = issuer
	}
	return nil
}

func discoverOIDCIssuerURL(ctx context.Context, client crclient.Client) (string, error) {
	auth := &configv1.Authentication{}
	auth.Name = "cluster"
	if err := client.Get(ctx, crclient.ObjectKeyFromObject(auth), auth); err != nil {
		return "", fmt.Errorf("failed to get Authentication CR: %w", err)
	}
	issuer := auth.Spec.ServiceAccountIssuer
	if issuer == "" {
		return "", fmt.Errorf("authentication CR has no serviceAccountIssuer set; specify --oidc-issuer-url or --instance-role-arn explicitly")
	}
	return issuer, nil
}

func (o *CreateOperatorRolesOptions) Run(ctx context.Context, logger logr.Logger) error {
	if err := o.parseAdditionalTags(); err != nil {
		return err
	}

	var awsSession *aws.Config
	if o.AWSCredentialsOpts.AWSCredentialsFile != "" || o.AWSCredentialsOpts.STSCredentialsFile != "" {
		if err := o.AWSCredentialsOpts.Validate(); err != nil {
			return err
		}
		var err error
		awsSession, err = o.AWSCredentialsOpts.GetSession(ctx, "cli-create-operator-roles", nil, o.Region)
		if err != nil {
			return err
		}
	} else {
		awsSession = awsutil.NewSession(ctx, "cli-create-operator-roles", "", "", "", o.Region)
	}
	awsConfig := awsutil.NewConfig()
	iamClient := iam.NewFromConfig(*awsSession, func(o *iam.Options) {
		o.Retryer = awsConfig()
	})

	results, err := o.CreateOperatorRoles(ctx, iamClient, logger)
	if err != nil {
		return err
	}

	return o.output(results, logger)
}

func (o *CreateOperatorRolesOptions) CreateOperatorRoles(ctx context.Context, iamClient awsapi.IAMAPI, logger logr.Logger) (*CreateOperatorRolesOutput, error) {
	trustPolicies, err := o.buildTrustPolicies(ctx, iamClient, logger)
	if err != nil {
		return nil, err
	}

	output := &CreateOperatorRolesOutput{}

	// Role 1: Operator EC2/ELBv2 — VPC endpoint services and instance type queries
	ec2RoleOpts := CreateIAMRoleOptions{
		RoleName:          fmt.Sprintf("%s-operator-ec2", o.NamePrefix),
		TrustPolicy:       trustPolicies.operatorTrust,
		PermissionsPolicy: operatorEC2Policy,
		additionalIAMTags: o.additionalIAMTags,
	}
	output.OperatorEC2RoleARN, err = createOrUpdateRole(ctx, iamClient, ec2RoleOpts, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create operator EC2 role: %w", err)
	}

	// Role 2: Operator OIDC S3 — upload/delete OIDC discovery docs
	s3RoleOpts := CreateIAMRoleOptions{
		RoleName:          fmt.Sprintf("%s-operator-oidc-s3", o.NamePrefix),
		TrustPolicy:       trustPolicies.operatorTrust,
		PermissionsPolicy: fmt.Sprintf(operatorOIDCS3PolicyTemplate, o.OIDCStorageProviderS3BucketName),
		additionalIAMTags: o.additionalIAMTags,
	}
	output.OperatorOIDCS3RoleARN, err = createOrUpdateRole(ctx, iamClient, s3RoleOpts, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create operator OIDC S3 role: %w", err)
	}

	// Role 3: External DNS — Route53 record management
	route53Resource := "arn:aws:route53:::hostedzone/*"
	if o.Route53HostedZoneID != "" {
		route53Resource = fmt.Sprintf("arn:aws:route53:::hostedzone/%s", o.Route53HostedZoneID)
	}
	externalDNSRoleOpts := CreateIAMRoleOptions{
		RoleName:          fmt.Sprintf("%s-external-dns", o.NamePrefix),
		TrustPolicy:       trustPolicies.externalDNSTrust,
		PermissionsPolicy: fmt.Sprintf(externalDNSPolicyTemplate, route53Resource),
		additionalIAMTags: o.additionalIAMTags,
	}
	output.ExternalDNSRoleARN, err = createOrUpdateRole(ctx, iamClient, externalDNSRoleOpts, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create external-dns role: %w", err)
	}

	return output, nil
}

func createOrUpdateRole(ctx context.Context, iamClient awsapi.IAMAPI, opts CreateIAMRoleOptions, logger logr.Logger) (string, error) {
	arn, err := opts.CreateRoleWithInlinePolicy(ctx, iamClient, logger)
	if err != nil {
		return "", err
	}

	_, err = iamClient.UpdateAssumeRolePolicy(ctx, &iam.UpdateAssumeRolePolicyInput{
		RoleName:       aws.String(opts.RoleName),
		PolicyDocument: aws.String(opts.TrustPolicy),
	})
	if err != nil {
		return "", fmt.Errorf("failed to update trust policy for role %s: %w", opts.RoleName, err)
	}
	logger.Info("Ensured trust policy is up to date", "role", opts.RoleName)

	return arn, nil
}

type operatorTrustPolicies struct {
	operatorTrust    string
	externalDNSTrust string
}

func (o *CreateOperatorRolesOptions) buildTrustPolicies(ctx context.Context, iamClient awsapi.IAMAPI, logger logr.Logger) (*operatorTrustPolicies, error) {
	if o.InstanceRoleARN != "" {
		trust := instanceRoleTrustPolicy(o.InstanceRoleARN)
		return &operatorTrustPolicies{
			operatorTrust:    trust,
			externalDNSTrust: trust,
		}, nil
	}

	providerARN, providerName, err := o.resolveOIDCProvider(ctx, iamClient, logger)
	if err != nil {
		return nil, err
	}

	operatorSA := fmt.Sprintf("system:serviceaccount:%s:operator", o.OperatorNamespace)
	externalDNSSA := fmt.Sprintf("system:serviceaccount:%s:external-dns", o.OperatorNamespace)

	return &operatorTrustPolicies{
		operatorTrust:    oidcTrustPolicy(providerARN, providerName, operatorSA),
		externalDNSTrust: oidcTrustPolicy(providerARN, providerName, externalDNSSA),
	}, nil
}

func (o *CreateOperatorRolesOptions) resolveOIDCProvider(ctx context.Context, iamClient awsapi.IAMAPI, logger logr.Logger) (providerARN, providerName string, err error) {
	providerName = strings.TrimPrefix(o.OIDCIssuerURL, "https://")

	providers, err := iamClient.ListOpenIDConnectProviders(ctx, &iam.ListOpenIDConnectProvidersInput{})
	if err != nil {
		return "", "", fmt.Errorf("failed to list OIDC providers: %w", err)
	}

	for _, p := range providers.OpenIDConnectProviderList {
		if strings.HasSuffix(*p.Arn, "/"+providerName) {
			logger.Info("Found OIDC provider", "arn", *p.Arn)
			return *p.Arn, providerName, nil
		}
	}

	return "", "", fmt.Errorf("no OIDC provider found matching issuer URL %q; register the management cluster's OIDC provider in AWS IAM first", o.OIDCIssuerURL)
}

func instanceRoleTrustPolicy(roleARN string) string {
	return fmt.Sprintf(`{
	"Version": "2012-10-17",
	"Statement": [
		{
			"Effect": "Allow",
			"Principal": {
				"AWS": %q
			},
			"Action": "sts:AssumeRole"
		}
	]
}`, roleARN)
}

func (o *CreateOperatorRolesOptions) output(results *CreateOperatorRolesOutput, logger logr.Logger) error {
	out := os.Stdout
	if o.OutputFile != "" {
		var err error
		out, err = os.Create(o.OutputFile)
		if err != nil {
			return fmt.Errorf("cannot create output file: %w", err)
		}
		defer func() {
			if cerr := out.Close(); cerr != nil {
				logger.Error(cerr, "failed to close output file")
			}
		}()
	}

	outputBytes, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize result: %w", err)
	}
	if _, err = out.Write(outputBytes); err != nil {
		return fmt.Errorf("failed to write result: %w", err)
	}

	credSource := CredentialSourceWebIdentity
	if o.InstanceRoleARN != "" {
		credSource = CredentialSourceEC2InstanceMetadata
	}
	logger.Info("Operator roles created. Install with:",
		"command", fmt.Sprintf(
			"hypershift install --aws-private-role-arn=%s --oidc-storage-provider-s3-role-arn=%s --external-dns-role-arn=%s --aws-role-credential-source=%s ...",
			results.OperatorEC2RoleARN,
			results.OperatorOIDCS3RoleARN,
			results.ExternalDNSRoleARN,
			credSource,
		),
	)

	return nil
}

func (o *CreateOperatorRolesOptions) parseAdditionalTags() error {
	parsed, err := cmdutil.ParseAWSTags(o.AdditionalTags)
	if err != nil {
		return err
	}
	for k, v := range parsed {
		o.additionalIAMTags = append(o.additionalIAMTags, iamtypes.Tag{
			Key:   aws.String(k),
			Value: aws.String(v),
		})
	}
	return nil
}

const operatorEC2Policy = `{
	"Version": "2012-10-17",
	"Statement": [
		{
			"Sid": "VPCEndpointServiceManagement",
			"Effect": "Allow",
			"Action": [
				"ec2:CreateVpcEndpointServiceConfiguration",
				"ec2:DeleteVpcEndpointServiceConfigurations",
				"ec2:DescribeVpcEndpointServiceConfigurations",
				"ec2:DescribeVpcEndpointServicePermissions",
				"ec2:ModifyVpcEndpointServicePermissions",
				"ec2:DescribeVpcEndpointConnections",
				"ec2:RejectVpcEndpointConnections",
				"ec2:CreateTags"
			],
			"Resource": "*"
		},
		{
			"Sid": "LoadBalancerDiscovery",
			"Effect": "Allow",
			"Action": [
				"elasticloadbalancing:DescribeLoadBalancers"
			],
			"Resource": "*"
		},
		{
			"Sid": "InstanceTypeDiscovery",
			"Effect": "Allow",
			"Action": [
				"ec2:DescribeInstanceTypes"
			],
			"Resource": "*"
		}
	]
}`

const operatorOIDCS3PolicyTemplate = `{
	"Version": "2012-10-17",
	"Statement": [
		{
			"Sid": "OIDCDocumentManagement",
			"Effect": "Allow",
			"Action": [
				"s3:PutObject",
				"s3:DeleteObject",
				"s3:DeleteObjects"
			],
			"Resource": "arn:aws:s3:::%s/*"
		}
	]
}`

const externalDNSPolicyTemplate = `{
	"Version": "2012-10-17",
	"Statement": [
		{
			"Sid": "HostedZoneDiscovery",
			"Effect": "Allow",
			"Action": [
				"route53:ListHostedZones",
				"route53:GetHostedZone",
				"route53:GetChange"
			],
			"Resource": "*"
		},
		{
			"Sid": "DNSRecordManagement",
			"Effect": "Allow",
			"Action": [
				"route53:ChangeResourceRecordSets",
				"route53:ListResourceRecordSets"
			],
			"Resource": "%s"
		}
	]
}`
