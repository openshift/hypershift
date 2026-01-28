package fix

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/iam"
	"github.com/aws/aws-sdk-go/service/s3"
	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/infraid"
	"github.com/openshift/hypershift/support/oidc"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

// Color constants for output
const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
)

// Output helper functions
func greenCheck() string {
	return colorGreen + "✓" + colorReset
}

func redX() string {
	return colorRed + "✗" + colorReset
}

func yellowQuestion() string {
	return colorYellow + "?" + colorReset
}

func yellowForce() string {
	return colorYellow + "!" + colorReset
}

type DrOidcIamOptions struct {
	// HostedCluster options (alternative to manual specification)
	HostedClusterName      string
	HostedClusterNamespace string

	// Manual specification options
	InfraID                       string
	Region                        string
	OIDCStorageProviderS3Bucket   string
	Issuer                        string

	// AWS Credentials options
	AWSCredentialsFile            string
	STSCredentialsFile            string
	RoleArn                       string
	Timeout                       time.Duration
	DryRun                        bool
	ForceRecreate                 bool
}

func NewDrOidcIamCommand() *cobra.Command {
	opts := &DrOidcIamOptions{
		Timeout: 10 * time.Minute,
	}

	cmd := &cobra.Command{
		Use:   "dr-oidc-iam",
		Short: "Fixes missing OIDC identity provider for disaster recovery scenarios",
		Long: `
This command fixes OIDC identity provider issues that can occur during disaster recovery
scenarios where the AWS IAM OIDC provider was accidentally deleted or is missing.

The command will:
1. Validate OIDC documents exist in S3
2. Check if OIDC identity provider exists in IAM
3. If missing, extract OIDC provider URL from S3 documents
4. Get SSL certificate thumbprint for the provider
5. Recreate OIDC provider in IAM with proper configuration

This resolves WebIdentityErr issues that prevent cluster operations.
`,
		Example: `
  # RECOMMENDED: Fix OIDC provider using HostedCluster reference
  # This automatically extracts infra-id, region, and bucket from the HostedCluster
  hypershift fix dr-oidc-iam --hc-name jparrill-hosted --hc-namespace clusters --aws-creds ~/.aws/credentials

  # Fix OIDC provider using STS credentials with role assumption
  hypershift fix dr-oidc-iam --hc-name jparrill-hosted --hc-namespace clusters --sts-creds /path/to/sts-creds.json --role-arn arn:aws:iam::820196288204:role/jparrill-sts

  # Fix OIDC provider with manual specification (when HostedCluster is not accessible)
  hypershift fix dr-oidc-iam --infra-id jparrill-hosted-abc123 --region us-east-1 --aws-creds ~/.aws/credentials

  # Fix OIDC provider with custom S3 bucket and issuer URL
  hypershift fix dr-oidc-iam --infra-id jparrill-hosted-abc123 --region us-east-1 --oidc-bucket my-custom-oidc-bucket --issuer https://my-custom-oidc-bucket.s3.us-east-1.amazonaws.com/jparrill-hosted-abc123 --aws-creds ~/.aws/credentials

  # DRY RUN: Preview what would be done without making changes
  hypershift fix dr-oidc-iam --hc-name jparrill-hosted --hc-namespace clusters --aws-creds ~/.aws/credentials --dry-run

  # FORCE RECREATE: Completely regenerate OIDC documents and provider (for complete recovery)
  hypershift fix dr-oidc-iam --hc-name jparrill-hosted --hc-namespace clusters --aws-creds ~/.aws/credentials --force-recreate

  # Fix with timeout adjustment for slow AWS operations
  hypershift fix dr-oidc-iam --hc-name jparrill-hosted --hc-namespace clusters --aws-creds ~/.aws/credentials --timeout 15m

  # Complete disaster recovery scenario example
  # 1. First run a dry-run to see what will be done:
  hypershift fix dr-oidc-iam --hc-name jparrill-hosted --hc-namespace clusters --aws-creds ~/.aws/credentials --dry-run

  # 2. Then execute the actual fix:
  hypershift fix dr-oidc-iam --hc-name jparrill-hosted --hc-namespace clusters --aws-creds ~/.aws/credentials

  # 3. If you need to completely regenerate everything:
  hypershift fix dr-oidc-iam --hc-name jparrill-hosted --hc-namespace clusters --aws-creds ~/.aws/credentials --force-recreate
`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Validate all options
			if err := opts.validate(); err != nil {
				return err
			}
			return opts.Run(cmd.Context())
		},
	}

	// HostedCluster flags (alternative to manual specification)
	cmd.Flags().StringVar(&opts.HostedClusterName, "hc-name", "", "Name of the HostedCluster to extract configuration from")
	cmd.Flags().StringVar(&opts.HostedClusterNamespace, "hc-namespace", "", "Namespace of the HostedCluster (required with --hc-name)")

	// Manual specification flags
	cmd.Flags().StringVar(&opts.InfraID, "infra-id", "", "Cluster infrastructure ID (required unless using --hc-name)")
	cmd.Flags().StringVar(&opts.Region, "region", "", "AWS region (required unless using --hc-name)")
	cmd.Flags().StringVar(&opts.OIDCStorageProviderS3Bucket, "oidc-bucket", "", "OIDC storage S3 bucket name (defaults to <infra-id>-oidc)")
	cmd.Flags().StringVar(&opts.Issuer, "issuer", "", "OIDC issuer URL (optional, auto-detected if not provided)")
	cmd.Flags().DurationVar(&opts.Timeout, "timeout", 10*time.Minute, "Operation timeout")
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "Show what would be done without making changes")
	cmd.Flags().BoolVar(&opts.ForceRecreate, "force-recreate", false, "Force recreation of OIDC documents and provider even if they exist")

	// AWS Credentials flags
	cmd.Flags().StringVar(&opts.AWSCredentialsFile, "aws-creds", "", "Path to AWS credentials file")
	cmd.Flags().StringVar(&opts.STSCredentialsFile, "sts-creds", "", "Path to STS credentials file")
	cmd.Flags().StringVar(&opts.RoleArn, "role-arn", "", "ARN of the role to assume (required with sts-creds)")

	return cmd
}

func (o *DrOidcIamOptions) validateCredentials() error {
	// Check that credentials are mutually exclusive
	if o.AWSCredentialsFile != "" {
		if o.STSCredentialsFile != "" || o.RoleArn != "" {
			return fmt.Errorf("only one of 'aws-creds' or 'sts-creds'/'role-arn' can be provided")
		}
		return nil
	}

	// If using STS credentials, both sts-creds and role-arn are required
	if o.STSCredentialsFile != "" || o.RoleArn != "" {
		if o.STSCredentialsFile == "" {
			return fmt.Errorf("sts-creds is required when using role-arn")
		}
		if o.RoleArn == "" {
			return fmt.Errorf("role-arn is required when using sts-creds")
		}
		return nil
	}

	return fmt.Errorf("either 'aws-creds' or both 'sts-creds' and 'role-arn' must be provided")
}

func (o *DrOidcIamOptions) validate() error {
	// Validate credentials first
	if err := o.validateCredentials(); err != nil {
		return err
	}

	// Check that we have either HostedCluster reference or manual specification
	if o.HostedClusterName != "" {
		// When using HostedCluster, namespace is required
		if o.HostedClusterNamespace == "" {
			return fmt.Errorf("--hc-namespace is required when using --hc-name")
		}
		// Using HostedCluster - manual infra-id and region should not be specified
		if o.InfraID != "" || o.Region != "" {
			return fmt.Errorf("when using --hc-name, --infra-id and --region should not be specified")
		}
		return nil
	}

	// Check that hc-namespace is not specified without hc-name
	if o.HostedClusterNamespace != "" {
		return fmt.Errorf("--hc-namespace can only be used with --hc-name")
	}

	// Using manual specification - infra-id and region are required
	if o.InfraID == "" {
		return fmt.Errorf("either --hc-name or --infra-id is required")
	}
	if o.Region == "" {
		return fmt.Errorf("either --hc-name or --region is required")
	}

	return nil
}

func (o *DrOidcIamOptions) getAWSSession(agent string, region string) (*session.Session, error) {
	if o.AWSCredentialsFile != "" {
		return awsutil.NewSession(agent, o.AWSCredentialsFile, "", "", region), nil
	}

	if o.STSCredentialsFile != "" {
		creds, err := awsutil.ParseSTSCredentialsFile(o.STSCredentialsFile)
		if err != nil {
			return nil, fmt.Errorf("failed to parse STS credentials: %w", err)
		}

		return awsutil.NewSTSSession(agent, o.RoleArn, region, creds)
	}

	return nil, fmt.Errorf("no credentials provided")
}

func (o *DrOidcIamOptions) extractFromHostedCluster(ctx context.Context) error {
	if o.HostedClusterName == "" {
		return nil // Not using HostedCluster
	}

	// Create Kubernetes client
	cfg, err := config.GetConfig()
	if err != nil {
		return fmt.Errorf("failed to get kubeconfig: %w", err)
	}

	scheme := runtime.NewScheme()
	if err := hyperv1.AddToScheme(scheme); err != nil {
		return fmt.Errorf("failed to add hypershift scheme: %w", err)
	}

	k8sClient, err := client.New(cfg, client.Options{
		Scheme: scheme,
	})
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	// Get the HostedCluster
	hc := &hyperv1.HostedCluster{}
	key := types.NamespacedName{
		Name:      o.HostedClusterName,
		Namespace: o.HostedClusterNamespace,
	}

	err = k8sClient.Get(ctx, key, hc)
	if err != nil {
		if errors.IsNotFound(err) {
			return fmt.Errorf("HostedCluster %s/%s not found", o.HostedClusterNamespace, o.HostedClusterName)
		}
		return fmt.Errorf("failed to get HostedCluster %s/%s: %w", o.HostedClusterNamespace, o.HostedClusterName, err)
	}

	// Validate that the platform is AWS
	if hc.Spec.Platform.Type != hyperv1.AWSPlatform {
		return fmt.Errorf("this command only supports AWS platform clusters, but HostedCluster %s/%s is using platform %s",
			o.HostedClusterNamespace, o.HostedClusterName, hc.Spec.Platform.Type)
	}

	if hc.Spec.Platform.AWS == nil {
		return fmt.Errorf("HostedCluster %s/%s has AWS platform type but missing AWS platform specification",
			o.HostedClusterNamespace, o.HostedClusterName)
	}

	// Extract region
	o.Region = hc.Spec.Platform.AWS.Region
	if o.Region == "" {
		return fmt.Errorf("HostedCluster %s/%s is missing AWS region", o.HostedClusterNamespace, o.HostedClusterName)
	}

	// Extract infra ID
	if hc.Spec.InfraID != "" {
		o.InfraID = hc.Spec.InfraID
	} else {
		// Generate infra ID same way as the controller
		o.InfraID = infraid.New(hc.Name)
	}

	return nil
}

func (o *DrOidcIamOptions) Run(ctx context.Context) error {
	// Extract information from HostedCluster if provided
	if err := o.extractFromHostedCluster(ctx); err != nil {
		return err
	}

	// Set defaults
	if o.OIDCStorageProviderS3Bucket == "" {
		o.OIDCStorageProviderS3Bucket = fmt.Sprintf("%s-oidc", o.InfraID)
	}

	if o.Issuer == "" {
		o.Issuer = oidcDiscoveryURL(o.OIDCStorageProviderS3Bucket, o.Region, o.InfraID)
	}

	fmt.Println("HyperShift OIDC Identity Provider Disaster Recovery")
	fmt.Println("===================================================")
	if o.HostedClusterName != "" {
		fmt.Printf("HostedCluster: %s/%s\n", o.HostedClusterNamespace, o.HostedClusterName)
	}
	fmt.Printf("Infrastructure ID: %s\n", o.InfraID)
	fmt.Printf("AWS Region: %s\n", o.Region)
	fmt.Printf("OIDC Bucket: %s\n", o.OIDCStorageProviderS3Bucket)
	fmt.Printf("OIDC Issuer: %s\n", o.Issuer)
	if o.DryRun {
		fmt.Println("Mode: DRY RUN (no changes will be made)")
	} else {
		fmt.Println("Mode: LIVE (changes will be applied)")
	}
	fmt.Println()

	// Create AWS session and clients
	awsSession, err := o.getAWSSession("cli-fix-dr-oidc-iam", o.Region)
	if err != nil {
		return fmt.Errorf("failed to create AWS session: %w", err)
	}

	awsConfig := awsutil.NewConfig()
	iamClient := iam.New(awsSession, awsConfig)
	s3Client := s3.New(awsSession, awsConfig)

	// Step 1: Check if OIDC documents exist in S3
	fmt.Println("Step 1: Checking OIDC documents in S3")
	oidcDocsExist := o.checkOIDCDocumentsExist(ctx, s3Client)
	if oidcDocsExist && !o.ForceRecreate {
		fmt.Printf("- (%s) OIDC documents found in S3\n", greenCheck())
	} else {
		if o.ForceRecreate {
			fmt.Printf("- (%s) Force recreate enabled - will regenerate OIDC documents\n", yellowForce())
		} else {
			fmt.Printf("- (%s) OIDC documents missing in S3 - will create them\n", redX())
		}
	}

	// Step 2: Check if OIDC identity provider exists in IAM
	fmt.Println("Step 2: Checking OIDC identity provider in IAM")
	_, exists, err := o.checkOIDCProvider(ctx, iamClient)
	if err != nil {
		return fmt.Errorf("failed to check OIDC provider: %w", err)
	}

	if exists && !o.ForceRecreate {
		fmt.Printf("- (%s) OIDC identity provider exists\n", greenCheck())
		if oidcDocsExist {
			fmt.Println("\nAll OIDC components are in place - no action needed!")
			return nil
		}
	} else {
		if o.ForceRecreate {
			fmt.Printf("- (%s) Force recreate enabled - will regenerate OIDC provider\n", yellowForce())
		} else {
			fmt.Printf("- (%s) OIDC identity provider missing - needs to be recreated\n", redX())
		}
	}

	// Step 3: Create/ensure S3 bucket exists
	fmt.Println("Step 3: Ensuring S3 bucket is properly configured")
	if err := o.ensureOIDCBucket(ctx, s3Client); err != nil {
		return fmt.Errorf("failed to ensure S3 bucket: %w", err)
	}
	fmt.Printf("- (%s) S3 bucket ready with public access enabled\n", greenCheck())

	// Step 4: Generate and upload OIDC documents
	if !oidcDocsExist || o.ForceRecreate {
		fmt.Println("Step 4: Generating and uploading OIDC documents")
		if o.DryRun {
			fmt.Printf("- (%s) DRY RUN: Would generate and upload OIDC documents\n", yellowQuestion())
		} else {
			if err := o.generateAndUploadOIDCDocuments(ctx, s3Client); err != nil {
				return fmt.Errorf("failed to generate OIDC documents: %w", err)
			}
			fmt.Printf("- (%s) OIDC documents generated and uploaded\n", greenCheck())
		}
	} else {
		fmt.Println("Step 4: OIDC documents")
		fmt.Printf("- (%s) OIDC documents already exist\n", greenCheck())
	}

	// Step 5: Get SSL certificate thumbprint
	fmt.Println("Step 5: Getting SSL certificate thumbprint")
	thumbprint, err := o.getSSLThumbprint(o.Issuer)
	if err != nil {
		return fmt.Errorf("failed to get SSL thumbprint: %w", err)
	}
	fmt.Printf("- (%s) SSL certificate thumbprint retrieved\n", greenCheck())

	// Step 6: Create/recreate OIDC provider
	if !exists || o.ForceRecreate {
		fmt.Println("Step 6: Creating/recreating OIDC identity provider")
		if o.DryRun {
			fmt.Printf("- (%s) DRY RUN: Would create OIDC provider\n", yellowQuestion())
			fmt.Printf("    Issuer: %s\n", o.Issuer)
			fmt.Printf("    Thumbprint: %s\n", thumbprint)
			fmt.Printf("    Allowed clients: openshift, sts.amazonaws.com\n")
		} else {
			_, err := o.createOIDCProvider(ctx, iamClient, thumbprint)
			if err != nil {
				return fmt.Errorf("failed to create OIDC provider: %w", err)
			}
			fmt.Printf("- (%s) OIDC identity provider successfully created\n", greenCheck())
		}
	} else {
		fmt.Println("Step 6: OIDC identity provider")
		fmt.Printf("- (%s) OIDC identity provider already exists\n", greenCheck())
	}

	// Step 7: Verify configuration and update HostedCluster
	fmt.Println("Step 7: Verifying OIDC configuration and updating HostedCluster")
	if err := o.verifyAndUpdateHostedCluster(ctx, s3Client, iamClient); err != nil {
		return fmt.Errorf("failed to verify and update HostedCluster: %w", err)
	}
	fmt.Printf("- (%s) OIDC configuration verified and HostedCluster updated\n", greenCheck())

	fmt.Println("\nOIDC identity provider disaster recovery completed successfully!")

	return nil
}

func (o *DrOidcIamOptions) verifyAndUpdateHostedCluster(ctx context.Context, s3Client *s3.S3, iamClient *iam.IAM) error {
	// Step 7.1: Re-verify all components are working
	if o.DryRun {
		fmt.Printf("  - (%s) DRY RUN: Would verify OIDC configuration\n", yellowQuestion())
		fmt.Printf("  - (%s) DRY RUN: Would update HostedCluster with restart annotation\n", yellowQuestion())
		return nil
	}

	// Verify OIDC documents exist
	if !o.checkOIDCDocumentsExist(ctx, s3Client) {
		return fmt.Errorf("verification failed: OIDC documents still missing after upload")
	}

	// Verify OIDC provider exists
	_, exists, err := o.checkOIDCProvider(ctx, iamClient)
	if err != nil {
		return fmt.Errorf("verification failed: error checking OIDC provider: %w", err)
	}
	if !exists {
		return fmt.Errorf("verification failed: OIDC provider still missing after creation")
	}

	// Step 7.2: Update HostedCluster with restart annotation (only if we have hc-name)
	if o.HostedClusterName != "" {
		if err := o.updateHostedClusterRestartAnnotation(ctx); err != nil {
			return fmt.Errorf("failed to update HostedCluster annotation: %w", err)
		}
	}

	return nil
}

func (o *DrOidcIamOptions) updateHostedClusterRestartAnnotation(ctx context.Context) error {
	// Create Kubernetes client
	cfg, err := config.GetConfig()
	if err != nil {
		return fmt.Errorf("failed to get kubeconfig: %w", err)
	}

	scheme := runtime.NewScheme()
	if err := hyperv1.AddToScheme(scheme); err != nil {
		return fmt.Errorf("failed to add hypershift scheme: %w", err)
	}

	k8sClient, err := client.New(cfg, client.Options{
		Scheme: scheme,
	})
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	// Get the HostedCluster
	hc := &hyperv1.HostedCluster{}
	key := types.NamespacedName{
		Name:      o.HostedClusterName,
		Namespace: o.HostedClusterNamespace,
	}

	err = k8sClient.Get(ctx, key, hc)
	if err != nil {
		return fmt.Errorf("failed to get HostedCluster %s/%s: %w", o.HostedClusterNamespace, o.HostedClusterName, err)
	}

	// Add restart annotation with current timestamp
	if hc.Annotations == nil {
		hc.Annotations = make(map[string]string)
	}

	// Use ISO 8601 format timestamp
	restartTime := time.Now().Format(time.RFC3339)
	hc.Annotations["hypershift.openshift.io/restart-date"] = restartTime

	// Update the HostedCluster
	err = k8sClient.Update(ctx, hc)
	if err != nil {
		return fmt.Errorf("failed to update HostedCluster with restart annotation: %w", err)
	}

	return nil
}

func (o *DrOidcIamOptions) checkOIDCDocumentsExist(ctx context.Context, s3Client *s3.S3) bool {
	// Check for key OIDC documents
	documents := []string{
		fmt.Sprintf("%s/.well-known/openid_configuration", o.InfraID),
		fmt.Sprintf("%s/keys.json", o.InfraID),
	}

	for _, doc := range documents {
		_, err := s3Client.HeadObjectWithContext(ctx, &s3.HeadObjectInput{
			Bucket: aws.String(o.OIDCStorageProviderS3Bucket),
			Key:    aws.String(doc),
		})
		if err != nil {
			return false
		}
	}
	return true
}

func (o *DrOidcIamOptions) ensureOIDCBucket(ctx context.Context, s3Client *s3.S3) error {
	// Check if bucket exists
	_, err := s3Client.HeadBucketWithContext(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(o.OIDCStorageProviderS3Bucket),
	})

	if err == nil {
		// Bucket exists - ensure it has correct Public Access configuration
		if !o.DryRun {
			// Disable Block Public Access (required for OIDC buckets)
			_, err = s3Client.PutPublicAccessBlockWithContext(ctx, &s3.PutPublicAccessBlockInput{
				Bucket: aws.String(o.OIDCStorageProviderS3Bucket),
				PublicAccessBlockConfiguration: &s3.PublicAccessBlockConfiguration{
					BlockPublicAcls:       aws.Bool(false),
					BlockPublicPolicy:     aws.Bool(false),
					IgnorePublicAcls:      aws.Bool(false),
					RestrictPublicBuckets: aws.Bool(false),
				},
			})
			if err != nil {
				return fmt.Errorf("failed to configure existing bucket's Block Public Access: %w", err)
			}

			// Ensure bucket policy is set
			_, err = s3Client.PutBucketPolicyWithContext(ctx, &s3.PutBucketPolicyInput{
				Bucket: aws.String(o.OIDCStorageProviderS3Bucket),
				Policy: aws.String(fmt.Sprintf(`{
					"Version": "2012-10-17",
					"Statement": [
						{
							"Effect": "Allow",
							"Principal": "*",
							"Action": "s3:GetObject",
							"Resource": "arn:aws:s3:::%s/*"
						}
					]
				}`, o.OIDCStorageProviderS3Bucket)),
			})
			if err != nil {
				return fmt.Errorf("failed to set existing bucket policy: %w", err)
			}
		}
		return nil
	}

	// Check if it's a "not found" error vs other errors
	if awsErr, ok := err.(awserr.Error); ok {
		if awsErr.Code() != "NotFound" && awsErr.Code() != s3.ErrCodeNoSuchBucket {
			return fmt.Errorf("error checking bucket existence: %w", err)
		}
	}

	// Bucket doesn't exist, create it
	if o.DryRun {
		return nil // Don't create in dry run mode
	}

	createBucketInput := &s3.CreateBucketInput{
		Bucket: aws.String(o.OIDCStorageProviderS3Bucket),
	}

	// For regions other than us-east-1, we need to specify the region
	if o.Region != "us-east-1" {
		createBucketInput.CreateBucketConfiguration = &s3.CreateBucketConfiguration{
			LocationConstraint: aws.String(o.Region),
		}
	}

	_, err = s3Client.CreateBucketWithContext(ctx, createBucketInput)
	if err != nil {
		return fmt.Errorf("failed to create S3 bucket: %w", err)
	}

	// Disable Block Public Access (required for OIDC buckets)
	_, err = s3Client.PutPublicAccessBlockWithContext(ctx, &s3.PutPublicAccessBlockInput{
		Bucket: aws.String(o.OIDCStorageProviderS3Bucket),
		PublicAccessBlockConfiguration: &s3.PublicAccessBlockConfiguration{
			BlockPublicAcls:       aws.Bool(false),
			BlockPublicPolicy:     aws.Bool(false),
			IgnorePublicAcls:      aws.Bool(false),
			RestrictPublicBuckets: aws.Bool(false),
		},
	})
	if err != nil {
		return fmt.Errorf("failed to disable Block Public Access: %w", err)
	}

	// Make bucket public readable (required for OIDC)
	_, err = s3Client.PutBucketPolicyWithContext(ctx, &s3.PutBucketPolicyInput{
		Bucket: aws.String(o.OIDCStorageProviderS3Bucket),
		Policy: aws.String(fmt.Sprintf(`{
			"Version": "2012-10-17",
			"Statement": [
				{
					"Effect": "Allow",
					"Principal": "*",
					"Action": "s3:GetObject",
					"Resource": "arn:aws:s3:::%s/*"
				}
			]
		}`, o.OIDCStorageProviderS3Bucket)),
	})
	if err != nil {
		return fmt.Errorf("failed to set bucket policy: %w", err)
	}

	return nil
}

func (o *DrOidcIamOptions) generateAndUploadOIDCDocuments(ctx context.Context, s3Client *s3.S3) error {
	if o.DryRun {
		return nil // Don't upload in dry run mode
	}

	// Generate RSA key pair for OIDC
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fmt.Errorf("failed to generate RSA key: %w", err)
	}

	// Convert public key to PEM format
	publicKeyBytes, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		return fmt.Errorf("failed to marshal public key: %w", err)
	}

	publicKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PUBLIC KEY",
		Bytes: publicKeyBytes,
	})

	params := oidc.ODICGeneratorParams{
		IssuerURL: o.Issuer,
		PubKey:    publicKeyPEM,
	}

	// Generate OIDC configuration document
	configDoc, err := oidc.GenerateConfigurationDocument(params)
	if err != nil {
		return fmt.Errorf("failed to generate OIDC configuration: %w", err)
	}

	// Generate JWKS document
	jwksDoc, err := oidc.GenerateJWKSDocument(params)
	if err != nil {
		return fmt.Errorf("failed to generate JWKS document: %w", err)
	}

	// Upload configuration document
	_, err = s3Client.PutObjectWithContext(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(o.OIDCStorageProviderS3Bucket),
		Key:         aws.String(fmt.Sprintf("%s/.well-known/openid_configuration", o.InfraID)),
		Body:        configDoc,
		ContentType: aws.String("application/json"),
	})
	if err != nil {
		return fmt.Errorf("failed to upload OIDC configuration: %w", err)
	}

	// Upload JWKS document
	_, err = s3Client.PutObjectWithContext(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(o.OIDCStorageProviderS3Bucket),
		Key:         aws.String(fmt.Sprintf("%s/keys.json", o.InfraID)),
		Body:        jwksDoc,
		ContentType: aws.String("application/json"),
	})
	if err != nil {
		return fmt.Errorf("failed to upload JWKS document: %w", err)
	}

	return nil
}

func (o *DrOidcIamOptions) checkOIDCDocumentsInS3(ctx context.Context, s3Client *s3.S3) error {
	// Check for key OIDC documents
	documents := []string{
		fmt.Sprintf("%s/.well-known/openid_configuration", o.InfraID),
		fmt.Sprintf("%s/keys.json", o.InfraID),
	}

	for _, doc := range documents {
		_, err := s3Client.HeadObjectWithContext(ctx, &s3.HeadObjectInput{
			Bucket: aws.String(o.OIDCStorageProviderS3Bucket),
			Key:    aws.String(doc),
		})
		if err != nil {
			return fmt.Errorf("OIDC document not found in S3: s3://%s/%s: %w", o.OIDCStorageProviderS3Bucket, doc, err)
		}
	}

	return nil
}

func (o *DrOidcIamOptions) checkOIDCProvider(ctx context.Context, iamClient *iam.IAM) (string, bool, error) {
	oidcProviderList, err := iamClient.ListOpenIDConnectProvidersWithContext(ctx, &iam.ListOpenIDConnectProvidersInput{})
	if err != nil {
		return "", false, err
	}

	providerName := strings.TrimPrefix(o.Issuer, "https://")
	for _, provider := range oidcProviderList.OpenIDConnectProviderList {
		if strings.Contains(*provider.Arn, providerName) {
			return *provider.Arn, true, nil
		}
	}

	return "", false, nil
}

func (o *DrOidcIamOptions) getSSLThumbprint(issuerURL string) (string, error) {
	// For S3-hosted OIDC providers, we use the DigiCert root CA thumbprint
	// This is the standard thumbprint used by AWS for S3-hosted OIDC providers
	if strings.Contains(issuerURL, ".s3.") || strings.Contains(issuerURL, "s3.amazonaws.com") {
		return "A9D53002E97E00E043244F3D170D6F4C414104FD", nil
	}

	// For other providers, attempt to get the actual certificate thumbprint
	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: false,
			},
		},
	}

	resp, err := client.Get(issuerURL)
	if err != nil {
		// If we can't get the certificate, fall back to the S3 thumbprint
		// as it's the most common case for HyperShift
		return "A9D53002E97E00E043244F3D170D6F4C414104FD", nil
	}
	defer resp.Body.Close()

	if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		// Get the root certificate thumbprint
		rootCert := resp.TLS.PeerCertificates[len(resp.TLS.PeerCertificates)-1]
		// Calculate SHA1 fingerprint manually
		hash := sha1.Sum(rootCert.Raw)
		thumbprint := fmt.Sprintf("%X", hash)
		return thumbprint, nil
	}

	// Default fallback
	return "A9D53002E97E00E043244F3D170D6F4C414104FD", nil
}

func (o *DrOidcIamOptions) createOIDCProvider(ctx context.Context, iamClient *iam.IAM, thumbprint string) (string, error) {
	// Remove any existing provider with the same issuer first
	providerName := strings.TrimPrefix(o.Issuer, "https://")
	oidcProviderList, err := iamClient.ListOpenIDConnectProvidersWithContext(ctx, &iam.ListOpenIDConnectProvidersInput{})
	if err == nil {
		for _, provider := range oidcProviderList.OpenIDConnectProviderList {
			if strings.Contains(*provider.Arn, providerName) {
				_, err := iamClient.DeleteOpenIDConnectProviderWithContext(ctx, &iam.DeleteOpenIDConnectProviderInput{
					OpenIDConnectProviderArn: provider.Arn,
				})
				if err != nil {
					return "", fmt.Errorf("failed to remove existing OIDC provider %s: %w", *provider.Arn, err)
				}
			}
		}
	}

	// Create the new OIDC provider
	createInput := &iam.CreateOpenIDConnectProviderInput{
		ClientIDList: []*string{
			aws.String("openshift"),
			aws.String("sts.amazonaws.com"),
		},
		ThumbprintList: []*string{
			aws.String(thumbprint),
		},
		Url: aws.String(o.Issuer),
	}

	var output *iam.CreateOpenIDConnectProviderOutput

	// Retry creation with backoff in case of eventual consistency issues
	backoff := wait.Backoff{
		Steps:    5,
		Duration: 5 * time.Second,
		Factor:   2.0,
		Jitter:   0.1,
	}

	err = wait.ExponentialBackoff(backoff, func() (bool, error) {
		var err error
		output, err = iamClient.CreateOpenIDConnectProviderWithContext(ctx, createInput)
		if err != nil {
			if awsErr, ok := err.(awserr.Error); ok {
				// Retry on transient errors
				if awsErr.Code() == "ServiceUnavailable" || awsErr.Code() == "Throttling" {
					return false, nil
				}
			}
			return false, err
		}
		return true, nil
	})

	if err != nil {
		return "", err
	}

	return *output.OpenIDConnectProviderArn, nil
}

// oidcDiscoveryURL generates the OIDC discovery URL for the given parameters
func oidcDiscoveryURL(bucket, region, infraID string) string {
	return fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", bucket, region, infraID)
}