package fix

import (
	"context"
	"crypto/sha1" //nolint:gosec // SHA-1 is required for AWS OIDC provider thumbprints
	"crypto/tls"
	stderrors "errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/support/infraid"
	"github.com/openshift/hypershift/support/oidc"

	awsv2 "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	"github.com/spf13/cobra"
)

const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"

	// oidcConfigPath is the well-known OIDC configuration discovery path.
	// Must use a dash (not underscore) per RFC 8414 and to match the controller.
	oidcConfigPath = "/.well-known/openid-configuration"

	// serviceAccountSigningKeySecret is the name of the secret containing the
	// service account signing key in the hosted control plane namespace.
	serviceAccountSigningKeySecret = "sa-signing-key"

	// serviceSignerPublicKey is the key name for the public key inside the
	// service account signing key secret.
	serviceSignerPublicKey = "service-account.pub"

	// digiCertS3RootCAThumbprint is the root CA thumbprint for S3 (DigiCert).
	// AWS requires a thumbprint for OIDC provider creation even though it is
	// ignored for S3-hosted providers.
	digiCertS3RootCAThumbprint = "A9D53002E97E00E043244F3D170D6F4C414104FD"

	// restartDateAnnotation is the annotation used to schedule a rolling restart.
	restartDateAnnotation = "hypershift.openshift.io/restart-date"
)

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
	InfraID                     string
	Region                      string
	OIDCStorageProviderS3Bucket string
	Issuer                      string

	// AWS Credentials options
	AWSCredentialsFile string
	STSCredentialsFile string
	RoleArn            string
	Timeout            time.Duration
	DryRun             bool
	ForceRecreate      bool
	RestartDelay       time.Duration

	// oidcBucketRegion is the AWS region where the OIDC S3 bucket lives.
	// It may differ from the cluster region. Extracted from the issuer URL.
	oidcBucketRegion string
}

func NewDrOidcIamCommand() *cobra.Command {
	opts := &DrOidcIamOptions{
		Timeout: 10 * time.Minute,
	}

	cmd := &cobra.Command{
		Use:          "dr-oidc-iam",
		Short:        "Fixes missing OIDC identity provider for disaster recovery scenarios",
		SilenceUsage: true,
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

  # DRY RUN: Preview what would be done without making changes
  hypershift fix dr-oidc-iam --hc-name jparrill-hosted --hc-namespace clusters --aws-creds ~/.aws/credentials --dry-run

  # FORCE RECREATE: Completely regenerate OIDC documents and provider (for complete recovery)
  hypershift fix dr-oidc-iam --hc-name jparrill-hosted --hc-namespace clusters --aws-creds ~/.aws/credentials --force-recreate
`,
		RunE: func(cmd *cobra.Command, args []string) error {
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
	cmd.Flags().DurationVar(&opts.RestartDelay, "restart-delay", 5*time.Minute, "Delay before triggering a HostedCluster rolling restart to allow OIDC reconciliation to complete")

	// AWS Credentials flags
	cmd.Flags().StringVar(&opts.AWSCredentialsFile, "aws-creds", "", "Path to AWS credentials file")
	cmd.Flags().StringVar(&opts.STSCredentialsFile, "sts-creds", "", "Path to STS credentials file")
	cmd.Flags().StringVar(&opts.RoleArn, "role-arn", "", "ARN of the role to assume (required with sts-creds)")

	return cmd
}

func (o *DrOidcIamOptions) validateCredentials() error {
	if o.AWSCredentialsFile != "" {
		if o.STSCredentialsFile != "" || o.RoleArn != "" {
			return fmt.Errorf("only one of 'aws-creds' or 'sts-creds'/'role-arn' can be provided")
		}
		return nil
	}

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
	if err := o.validateCredentials(); err != nil {
		return err
	}

	if o.HostedClusterName != "" {
		if o.HostedClusterNamespace == "" {
			return fmt.Errorf("--hc-namespace is required when using --hc-name")
		}
		if o.InfraID != "" || o.Region != "" {
			return fmt.Errorf("when using --hc-name, --infra-id and --region should not be specified")
		}
		return nil
	}

	if o.HostedClusterNamespace != "" {
		return fmt.Errorf("--hc-namespace can only be used with --hc-name")
	}

	if o.InfraID == "" || o.Region == "" {
		return fmt.Errorf("--infra-id and --region are required when --hc-name is not set")
	}

	return nil
}

func (o *DrOidcIamOptions) getAWSConfig(ctx context.Context, agent string, region string) (*awsv2.Config, error) {
	if o.AWSCredentialsFile != "" {
		if _, err := os.Stat(o.AWSCredentialsFile); err != nil {
			return nil, fmt.Errorf("failed to read AWS credentials file %s: %w", o.AWSCredentialsFile, err)
		}
		return awsutil.NewSessionV2(ctx, agent, o.AWSCredentialsFile, "", "", region), nil
	}

	if o.STSCredentialsFile != "" {
		creds, err := awsutil.ParseSTSCredentialsFileV2(o.STSCredentialsFile)
		if err != nil {
			return nil, fmt.Errorf("failed to parse STS credentials: %w", err)
		}

		return awsutil.NewSTSSessionV2(ctx, agent, o.RoleArn, region, creds)
	}

	return nil, fmt.Errorf("no credentials provided")
}

// newK8sClient creates a controller-runtime Kubernetes client configured with the HyperShift API scheme.
func newK8sClient() (client.Client, error) {
	cfg, err := config.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get kubeconfig: %w", err)
	}

	scheme := runtime.NewScheme()
	if err := hyperv1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("failed to add hypershift scheme: %w", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("failed to add core scheme: %w", err)
	}

	k8sClient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}
	return k8sClient, nil
}

// extractFromHostedCluster populates InfraID, Region, Issuer, and
// OIDCStorageProviderS3Bucket from the HostedCluster resource.
func (o *DrOidcIamOptions) extractFromHostedCluster(ctx context.Context, k8sClient client.Client) error {
	if o.HostedClusterName == "" {
		return nil
	}

	hc := &hyperv1.HostedCluster{}
	key := types.NamespacedName{
		Name:      o.HostedClusterName,
		Namespace: o.HostedClusterNamespace,
	}

	if err := k8sClient.Get(ctx, key, hc); err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("HostedCluster %s/%s not found", o.HostedClusterNamespace, o.HostedClusterName)
		}
		return fmt.Errorf("failed to get HostedCluster %s/%s: %w", o.HostedClusterNamespace, o.HostedClusterName, err)
	}

	if hc.Spec.Platform.Type != hyperv1.AWSPlatform {
		return fmt.Errorf("this command only supports AWS platform clusters, but HostedCluster %s/%s is using platform %s",
			o.HostedClusterNamespace, o.HostedClusterName, hc.Spec.Platform.Type)
	}
	if hc.Spec.Platform.AWS == nil {
		return fmt.Errorf("HostedCluster %s/%s has AWS platform type but missing AWS platform specification",
			o.HostedClusterNamespace, o.HostedClusterName)
	}

	o.Region = hc.Spec.Platform.AWS.Region
	if o.Region == "" {
		return fmt.Errorf("HostedCluster %s/%s is missing AWS region", o.HostedClusterNamespace, o.HostedClusterName)
	}

	if hc.Spec.InfraID != "" {
		o.InfraID = hc.Spec.InfraID
	} else {
		o.InfraID = infraid.New(hc.Name)
	}

	// Extract issuer URL from the HostedCluster spec.
	if o.Issuer == "" && hc.Spec.IssuerURL != "" {
		o.Issuer = hc.Spec.IssuerURL
	}

	// Extract the OIDC bucket name from the issuer URL if not explicitly set.
	// Issuer URL format: https://<bucket>.s3.<region>.amazonaws.com/<infraID>
	if o.OIDCStorageProviderS3Bucket == "" && o.Issuer != "" {
		bucket := extractBucketFromIssuerURL(o.Issuer)
		if bucket != "" {
			o.OIDCStorageProviderS3Bucket = bucket
		}
	}

	// The OIDC S3 bucket may be in a different region than the cluster.
	// Extract the bucket region from the issuer URL.
	if o.oidcBucketRegion == "" && o.Issuer != "" {
		if r := extractRegionFromIssuerURL(o.Issuer); r != "" {
			o.oidcBucketRegion = r
		}
	}

	return nil
}

// extractBucketFromIssuerURL parses a bucket name from an S3-style OIDC issuer URL.
// Expected format: https://<bucket>.s3.<region>.amazonaws.com/<infraID>
func extractBucketFromIssuerURL(issuerURL string) string {
	trimmed := strings.TrimPrefix(issuerURL, "https://")
	trimmed = strings.TrimPrefix(trimmed, "http://")
	idx := strings.Index(trimmed, ".s3.")
	if idx <= 0 {
		return ""
	}
	return trimmed[:idx]
}

// extractRegionFromIssuerURL parses the S3 bucket region from an OIDC issuer URL.
// Expected format: https://<bucket>.s3.<region>.amazonaws.com/<infraID>
func extractRegionFromIssuerURL(issuerURL string) string {
	trimmed := strings.TrimPrefix(issuerURL, "https://")
	trimmed = strings.TrimPrefix(trimmed, "http://")
	s3Idx := strings.Index(trimmed, ".s3.")
	if s3Idx <= 0 {
		return ""
	}
	// After ".s3." we have "<region>.amazonaws.com/..."
	afterS3 := trimmed[s3Idx+4:]
	dotIdx := strings.Index(afterS3, ".amazonaws.com")
	if dotIdx <= 0 {
		return ""
	}
	return afterS3[:dotIdx]
}

// getServiceAccountPublicKey retrieves the existing service account signing public key
// from the sa-signing-key secret in the hosted control plane namespace.
func (o *DrOidcIamOptions) getServiceAccountPublicKey(ctx context.Context, k8sClient client.Client) ([]byte, error) {
	if o.HostedClusterName == "" {
		return nil, fmt.Errorf("--hc-name is required to retrieve the service account signing key from the cluster")
	}

	hcpNamespace := manifests.HostedControlPlaneNamespace(o.HostedClusterNamespace, o.HostedClusterName)

	secret := &corev1.Secret{}
	key := types.NamespacedName{
		Namespace: hcpNamespace,
		Name:      serviceAccountSigningKeySecret,
	}
	if err := k8sClient.Get(ctx, key, secret); err != nil {
		return nil, fmt.Errorf("failed to get service account signing key secret %q: %w", key, err)
	}

	pubKey, ok := secret.Data[serviceSignerPublicKey]
	if !ok || len(pubKey) == 0 {
		return nil, fmt.Errorf("service account signing key secret %q is missing the %q key", key, serviceSignerPublicKey)
	}

	return pubKey, nil
}

func (o *DrOidcIamOptions) Run(ctx context.Context) error {
	// Create the k8s client once and reuse it across all operations.
	var k8sClient client.Client
	if o.HostedClusterName != "" {
		var err error
		k8sClient, err = newK8sClient()
		if err != nil {
			return err
		}
	}

	if err := o.extractFromHostedCluster(ctx, k8sClient); err != nil {
		return err
	}

	// Apply the timeout to the context.
	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()

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

	// IAM is a global service; the cluster region is fine.
	iamCfg, err := o.getAWSConfig(ctx, "cli-fix-dr-oidc-iam", o.Region)
	if err != nil {
		return fmt.Errorf("failed to create AWS config: %w", err)
	}
	retryerFn := awsutil.NewConfigV2()
	iamClient := iam.NewFromConfig(*iamCfg, func(o *iam.Options) {
		o.Retryer = retryerFn()
	})

	// The OIDC S3 bucket may live in a different region than the cluster.
	s3Region := o.Region
	if o.oidcBucketRegion != "" {
		s3Region = o.oidcBucketRegion
	}
	s3Cfg, err := o.getAWSConfig(ctx, "cli-fix-dr-oidc-iam-s3", s3Region)
	if err != nil {
		return fmt.Errorf("failed to create AWS S3 config: %w", err)
	}
	s3Client := s3.NewFromConfig(*s3Cfg, func(o *s3.Options) {
		o.Retryer = retryerFn()
	})

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
	providerARN, exists, err := o.checkOIDCProvider(ctx, iamClient)
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

	// Step 4: Generate and upload OIDC documents using the EXISTING cluster key
	if !oidcDocsExist || o.ForceRecreate {
		fmt.Println("Step 4: Generating and uploading OIDC documents")
		if o.DryRun {
			fmt.Printf("- (%s) DRY RUN: Would generate and upload OIDC documents using existing cluster signing key\n", yellowQuestion())
		} else {
			if err := o.generateAndUploadOIDCDocuments(ctx, k8sClient, s3Client); err != nil {
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
	thumbprint, err := o.getSSLThumbprint(ctx, o.Issuer)
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
			if err := o.deleteOIDCProviderIfExists(ctx, iamClient, providerARN); err != nil {
				return fmt.Errorf("failed to delete existing OIDC provider: %w", err)
			}
			if _, err := o.createOIDCProvider(ctx, iamClient, thumbprint); err != nil {
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
	if err := o.verifyAndUpdateHostedCluster(ctx, k8sClient, s3Client, iamClient); err != nil {
		return fmt.Errorf("failed to verify and update HostedCluster: %w", err)
	}
	fmt.Printf("- (%s) OIDC configuration verified and HostedCluster updated\n", greenCheck())

	fmt.Println("\nOIDC identity provider disaster recovery completed successfully!")

	return nil
}

func (o *DrOidcIamOptions) verifyAndUpdateHostedCluster(ctx context.Context, k8sClient client.Client, s3Client *s3.Client, iamClient *iam.Client) error {
	if o.DryRun {
		fmt.Printf("  - (%s) DRY RUN: Would verify OIDC configuration\n", yellowQuestion())
		fmt.Printf("  - (%s) DRY RUN: Would update HostedCluster with restart annotation\n", yellowQuestion())
		return nil
	}

	if !o.checkOIDCDocumentsExist(ctx, s3Client) {
		return fmt.Errorf("verification failed: OIDC documents still missing after upload")
	}

	_, exists, err := o.checkOIDCProvider(ctx, iamClient)
	if err != nil {
		return fmt.Errorf("verification failed: error checking OIDC provider: %w", err)
	}
	if !exists {
		return fmt.Errorf("verification failed: OIDC provider still missing after creation")
	}

	if o.HostedClusterName != "" {
		hasCrashLoop, err := o.hasCrashLoopBackOffPods(ctx, k8sClient)
		if err != nil {
			fmt.Printf("  WARNING: could not check for CrashLoopBackOff pods: %v\n", err)
		}
		if hasCrashLoop {
			if err := o.updateHostedClusterRestartAnnotation(ctx, k8sClient); err != nil {
				return fmt.Errorf("failed to update HostedCluster annotation: %w", err)
			}
		} else {
			fmt.Printf("  - No pods in CrashLoopBackOff detected, skipping rolling restart\n")
		}
	}

	return nil
}

// hasCrashLoopBackOffPods checks if any pods in the HCP namespace are in CrashLoopBackOff.
func (o *DrOidcIamOptions) hasCrashLoopBackOffPods(ctx context.Context, k8sClient client.Client) (bool, error) {
	hcpNamespace := manifests.HostedControlPlaneNamespace(o.HostedClusterNamespace, o.HostedClusterName)

	podList := &corev1.PodList{}
	if err := k8sClient.List(ctx, podList, client.InNamespace(hcpNamespace)); err != nil {
		return false, fmt.Errorf("failed to list pods in %s: %w", hcpNamespace, err)
	}

	for _, pod := range podList.Items {
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.State.Waiting != nil && cs.State.Waiting.Reason == "CrashLoopBackOff" {
				return true, nil
			}
		}
	}
	return false, nil
}

func (o *DrOidcIamOptions) updateHostedClusterRestartAnnotation(ctx context.Context, k8sClient client.Client) error {
	hc := &hyperv1.HostedCluster{}
	key := types.NamespacedName{
		Name:      o.HostedClusterName,
		Namespace: o.HostedClusterNamespace,
	}

	if err := k8sClient.Get(ctx, key, hc); err != nil {
		return fmt.Errorf("failed to get HostedCluster %s/%s: %w", o.HostedClusterNamespace, o.HostedClusterName, err)
	}

	if hc.Annotations == nil {
		hc.Annotations = make(map[string]string)
	}

	restartTime := time.Now().UTC().Add(o.RestartDelay).Format(time.RFC3339)
	hc.Annotations[restartDateAnnotation] = restartTime
	fmt.Printf("  - Scheduled rolling restart at %s UTC (in %s)\n", restartTime, o.RestartDelay)

	if err := k8sClient.Update(ctx, hc); err != nil {
		return fmt.Errorf("failed to update HostedCluster with restart annotation: %w", err)
	}

	return nil
}

// checkOIDCDocumentsExist checks whether both OIDC documents (discovery config
// and JWKS) exist at the correct S3 paths.
func (o *DrOidcIamOptions) checkOIDCDocumentsExist(ctx context.Context, s3Client *s3.Client) bool {
	documents := []string{
		o.InfraID + oidcConfigPath,
		o.InfraID + oidc.JWKSURI,
	}

	for _, doc := range documents {
		_, err := s3Client.HeadObject(ctx, &s3.HeadObjectInput{
			Bucket: awsv2.String(o.OIDCStorageProviderS3Bucket),
			Key:    awsv2.String(doc),
		})
		if err != nil {
			return false
		}
	}
	return true
}

// configureBucketPublicAccess sets public read access on an S3 bucket by
// disabling the Block Public Access settings and applying a read-all policy.
func configureBucketPublicAccess(ctx context.Context, s3Client *s3.Client, bucketName string) error {
	_, err := s3Client.PutPublicAccessBlock(ctx, &s3.PutPublicAccessBlockInput{
		Bucket: awsv2.String(bucketName),
		PublicAccessBlockConfiguration: &s3types.PublicAccessBlockConfiguration{
			BlockPublicAcls:       awsv2.Bool(false),
			BlockPublicPolicy:     awsv2.Bool(false),
			IgnorePublicAcls:      awsv2.Bool(false),
			RestrictPublicBuckets: awsv2.Bool(false),
		},
	})
	if err != nil {
		return fmt.Errorf("failed to configure Block Public Access: %w", err)
	}

	_, err = s3Client.PutBucketPolicy(ctx, &s3.PutBucketPolicyInput{
		Bucket: awsv2.String(bucketName),
		Policy: awsv2.String(fmt.Sprintf(`{
			"Version": "2012-10-17",
			"Statement": [
				{
					"Effect": "Allow",
					"Principal": "*",
					"Action": "s3:GetObject",
					"Resource": "arn:aws:s3:::%s/*"
				}
			]
		}`, bucketName)),
	})
	if err != nil {
		return fmt.Errorf("failed to set bucket policy: %w", err)
	}

	return nil
}

func (o *DrOidcIamOptions) ensureOIDCBucket(ctx context.Context, s3Client *s3.Client) error {
	_, err := s3Client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: awsv2.String(o.OIDCStorageProviderS3Bucket),
	})

	if err == nil {
		if !o.DryRun {
			return configureBucketPublicAccess(ctx, s3Client, o.OIDCStorageProviderS3Bucket)
		}
		return nil
	}

	var notFound *s3types.NotFound
	var noSuchBucket *s3types.NoSuchBucket
	if !stderrors.As(err, &notFound) && !stderrors.As(err, &noSuchBucket) {
		// Also check smithy API error codes for headBucket which returns generic errors
		var apiErr smithy.APIError
		if stderrors.As(err, &apiErr) {
			if apiErr.ErrorCode() != "NotFound" && apiErr.ErrorCode() != "NoSuchBucket" {
				return fmt.Errorf("error checking bucket existence: %w", err)
			}
		} else {
			return fmt.Errorf("error checking bucket existence: %w", err)
		}
	}

	if o.DryRun {
		return nil
	}

	createBucketInput := &s3.CreateBucketInput{
		Bucket: awsv2.String(o.OIDCStorageProviderS3Bucket),
	}

	// Use the OIDC bucket region for LocationConstraint, not the cluster region.
	bucketRegion := o.oidcBucketRegion
	if bucketRegion == "" {
		bucketRegion = o.Region
	}
	if bucketRegion != "us-east-1" {
		createBucketInput.CreateBucketConfiguration = &s3types.CreateBucketConfiguration{
			LocationConstraint: s3types.BucketLocationConstraint(bucketRegion),
		}
	}

	_, err = s3Client.CreateBucket(ctx, createBucketInput)
	if err != nil {
		return fmt.Errorf("failed to create S3 bucket: %w", err)
	}

	return configureBucketPublicAccess(ctx, s3Client, o.OIDCStorageProviderS3Bucket)
}

// generateAndUploadOIDCDocuments retrieves the existing service account
// public key from the cluster and uses it to generate and upload OIDC
// discovery and JWKS documents to S3.
func (o *DrOidcIamOptions) generateAndUploadOIDCDocuments(ctx context.Context, k8sClient client.Client, s3Client *s3.Client) error {
	if o.DryRun {
		return nil
	}

	// Retrieve the existing public key from the cluster instead of
	// generating a new one. Generating a new key pair would break token
	// verification because the KAS signs tokens with the private key that
	// is already stored in the cluster.
	pubKey, err := o.getServiceAccountPublicKey(ctx, k8sClient)
	if err != nil {
		return fmt.Errorf("failed to retrieve service account public key from cluster: %w", err)
	}

	params := oidc.OIDCGeneratorParams{
		IssuerURL: o.Issuer,
		PubKey:    pubKey,
	}

	// Use the same document generators and S3 paths as the controller
	// (see hostedcluster_controller.go oidcDocumentGenerators()).
	generators := map[string]oidc.OIDCDocumentGeneratorFunc{
		oidcConfigPath: oidc.GenerateConfigurationDocument,
		oidc.JWKSURI:   oidc.GenerateJWKSDocument,
	}

	for path, generator := range generators {
		bodyReader, err := generator(params)
		if err != nil {
			return fmt.Errorf("failed to generate OIDC document %s: %w", path, err)
		}
		_, err = s3Client.PutObject(ctx, &s3.PutObjectInput{
			Bucket:      awsv2.String(o.OIDCStorageProviderS3Bucket),
			Key:         awsv2.String(o.InfraID + path),
			Body:        bodyReader,
			ContentType: awsv2.String("application/json"),
		})
		if err != nil {
			return fmt.Errorf("failed to upload OIDC document %s: %w", path, err)
		}
	}

	return nil
}

func (o *DrOidcIamOptions) checkOIDCProvider(ctx context.Context, iamClient *iam.Client) (string, bool, error) {
	oidcProviderList, err := iamClient.ListOpenIDConnectProviders(ctx, &iam.ListOpenIDConnectProvidersInput{})
	if err != nil {
		return "", false, err
	}

	providerName := strings.TrimPrefix(o.Issuer, "https://")
	expectedSuffix := "/" + providerName
	for _, provider := range oidcProviderList.OpenIDConnectProviderList {
		if provider.Arn != nil && strings.HasSuffix(*provider.Arn, expectedSuffix) {
			return *provider.Arn, true, nil
		}
	}

	return "", false, nil
}

func (o *DrOidcIamOptions) getSSLThumbprint(ctx context.Context, issuerURL string) (string, error) {
	// For S3-hosted OIDC providers, we use the DigiCert root CA thumbprint.
	// AWS requires a thumbprint for OIDC provider creation even though it is
	// ignored for S3-hosted providers.
	if strings.Contains(issuerURL, ".s3.") || strings.Contains(issuerURL, "s3.amazonaws.com") {
		return digiCertS3RootCAThumbprint, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, issuerURL, nil)
	if err != nil {
		return digiCertS3RootCAThumbprint, nil
	}

	httpClient := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: false,
				MinVersion:         tls.VersionTLS12,
			},
		},
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		// If we can't reach the issuer, fall back to the S3 thumbprint
		// as it's the most common case for HyperShift.
		fmt.Printf("  WARNING: could not reach issuer %s, falling back to S3 thumbprint: %v\n", issuerURL, err)
		return digiCertS3RootCAThumbprint, nil
	}
	defer resp.Body.Close()

	if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		rootCert := resp.TLS.PeerCertificates[len(resp.TLS.PeerCertificates)-1]
		hash := sha1.Sum(rootCert.Raw) //nolint:gosec // SHA-1 is required by AWS for OIDC thumbprints
		thumbprint := fmt.Sprintf("%X", hash)
		return thumbprint, nil
	}

	fmt.Printf("  WARNING: no TLS certificates found for issuer %s, falling back to S3 thumbprint\n", issuerURL)
	return digiCertS3RootCAThumbprint, nil
}

// deleteOIDCProviderIfExists removes an existing OIDC provider by ARN.
func (o *DrOidcIamOptions) deleteOIDCProviderIfExists(ctx context.Context, iamClient *iam.Client, providerARN string) error {
	if providerARN == "" {
		return nil
	}
	_, err := iamClient.DeleteOpenIDConnectProvider(ctx, &iam.DeleteOpenIDConnectProviderInput{
		OpenIDConnectProviderArn: awsv2.String(providerARN),
	})
	if err != nil {
		return fmt.Errorf("failed to remove existing OIDC provider %s: %w", providerARN, err)
	}
	return nil
}

func (o *DrOidcIamOptions) createOIDCProvider(ctx context.Context, iamClient *iam.Client, thumbprint string) (string, error) {
	createInput := &iam.CreateOpenIDConnectProviderInput{
		ClientIDList: []string{
			"openshift",
			"sts.amazonaws.com",
		},
		ThumbprintList: []string{
			thumbprint,
		},
		Url: awsv2.String(o.Issuer),
	}

	var output *iam.CreateOpenIDConnectProviderOutput

	backoff := wait.Backoff{
		Steps:    5,
		Duration: 5 * time.Second,
		Factor:   2.0,
		Jitter:   0.1,
	}

	err := wait.ExponentialBackoff(backoff, func() (bool, error) {
		var createErr error
		output, createErr = iamClient.CreateOpenIDConnectProvider(ctx, createInput)
		if createErr != nil {
			var apiErr smithy.APIError
			if stderrors.As(createErr, &apiErr) {
				if apiErr.ErrorCode() == "ServiceUnavailable" || apiErr.ErrorCode() == "Throttling" {
					return false, nil
				}
			}
			return false, createErr
		}
		return true, nil
	})

	if err != nil {
		return "", err
	}

	return *output.OpenIDConnectProviderArn, nil
}

// oidcDiscoveryURL generates the OIDC discovery URL for the given parameters.
func oidcDiscoveryURL(bucket, region, infraID string) string {
	return fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", bucket, region, infraID)
}
