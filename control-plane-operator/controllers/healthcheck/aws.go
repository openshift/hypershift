package healthcheck

import (
	"context"
	"crypto/sha1"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/iam"
	"github.com/aws/aws-sdk-go/service/iam/iamiface"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func awsHealthCheckIdentityProvider(ctx context.Context, c client.Client, hcp *hyperv1.HostedControlPlane) error {
	log := ctrl.LoggerFrom(ctx).WithName("aws-health-check-identity-provider")

	ec2Client, awsSession := hostedcontrolplane.GetEC2Client()
	if ec2Client == nil {
		log.Info("EC2 client is nil, skipping AWS health check")
		return nil
	}

	// We try to interact with cloud provider to see validate is operational.
	if _, err := ec2Client.DescribeVpcEndpointsWithContext(ctx, &ec2.DescribeVpcEndpointsInput{}); err != nil {
		if awsErr, ok := err.(awserr.Error); ok {
			// When awsErr.Code() is WebIdentityErr it's likely to be an external issue, e.g. the idp resource was deleted.
			// We don't set awsErr.Message() in the condition as it might contain aws requests IDs that would make the condition be updated in loop.
			if awsErr.Code() == "WebIdentityErr" {
				// Try to validate and recreate OIDC identity provider
				if err := validateAndRecreateOIDCIdentityProvider(ctx, c, hcp, awsSession); err != nil {
					// Check if the error indicates that the identity provider is not ready yet
					if strings.Contains(err.Error(), "identity provider not ready") {
						condition := metav1.Condition{
							Type:               string(hyperv1.ValidAWSIdentityProvider),
							ObservedGeneration: hcp.Generation,
							Status:             metav1.ConditionUnknown,
							Message:            "Identity provider is not ready yet",
							Reason:             hyperv1.StatusUnknownReason,
						}
						meta.SetStatusCondition(&hcp.Status.Conditions, condition)
						log.Info("Identity provider not ready yet, will retry")
						return nil // Return nil to avoid flooding logs with errors
					}
					condition := metav1.Condition{
						Type:               string(hyperv1.ValidAWSIdentityProvider),
						ObservedGeneration: hcp.Generation,
						Status:             metav1.ConditionFalse,
						Message:            fmt.Sprintf("WebIdentityErr: %s. OIDC validation failed: %v", awsErr.Code(), err),
						Reason:             hyperv1.InvalidIdentityProvider,
					}
					meta.SetStatusCondition(&hcp.Status.Conditions, condition)
					return fmt.Errorf("error health checking AWS identity provider: %s %s. OIDC validation failed: %w", awsErr.Code(), awsErr.Message(), err)
				}
				// If OIDC validation succeeded, continue with normal flow
			} else {
				condition := metav1.Condition{
					Type:               string(hyperv1.ValidAWSIdentityProvider),
					ObservedGeneration: hcp.Generation,
					Status:             metav1.ConditionUnknown,
					Message:            awsErr.Code(),
					Reason:             hyperv1.AWSErrorReason,
				}
				meta.SetStatusCondition(&hcp.Status.Conditions, condition)
				return fmt.Errorf("error health checking AWS identity provider: %s %s", awsErr.Code(), awsErr.Message())
			}
		} else {
			condition := metav1.Condition{
				Type:               string(hyperv1.ValidAWSIdentityProvider),
				ObservedGeneration: hcp.Generation,
				Status:             metav1.ConditionUnknown,
				Message:            err.Error(),
				Reason:             hyperv1.StatusUnknownReason,
			}
			meta.SetStatusCondition(&hcp.Status.Conditions, condition)
			return fmt.Errorf("error health checking AWS identity provider: %w", err)
		}
	}

	condition := metav1.Condition{
		Type:               string(hyperv1.ValidAWSIdentityProvider),
		ObservedGeneration: hcp.Generation,
		Status:             metav1.ConditionTrue,
		Message:            hyperv1.AllIsWellMessage,
		Reason:             hyperv1.AsExpectedReason,
	}
	meta.SetStatusCondition(&hcp.Status.Conditions, condition)

	return nil
}

// validateAndRecreateOIDCIdentityProvider validates that the OIDC identity provider exists in IAM
// and that the OIDC documents in S3 are accessible. If the identity provider doesn't exist,
// it attempts to recreate it.
func validateAndRecreateOIDCIdentityProvider(ctx context.Context, c client.Client, hcp *hyperv1.HostedControlPlane, awsSession *session.Session) error {
	log := ctrl.LoggerFrom(ctx).WithName("oidc-validation")

	if hcp.Spec.IssuerURL == "" {
		return fmt.Errorf("HostedControlPlane has no IssuerURL configured")
	}

	// Log the start of OIDC validation
	log.Info("Starting OIDC identity provider validation", "namespace", hcp.Namespace, "name", hcp.Name, "issuerURL", hcp.Spec.IssuerURL)

	// First, validate that the OIDC documents in S3 are accessible
	// This doesn't require AWS credentials and can help identify the root cause
	log.Info("Validating OIDC documents in S3 first")
	if err := validateOIDCDocumentsInS3(ctx, hcp.Spec.IssuerURL); err != nil {
		return fmt.Errorf("failed to validate OIDC documents in S3: %w", err)
	}

	// Extract the OIDC identity provider ARN from the IssuerURL
	// The IssuerURL format is typically: https://hypershift-ci-1-oidc.s3.us-east-1.amazonaws.com/cluster-infra-id
	// We need to extract the bucket name and construct the OIDC identity provider ARN
	oidcProviderArn, err := extractOIDCProviderArnFromIssuerURL(hcp.Spec.IssuerURL)
	if err != nil {
		return fmt.Errorf("failed to extract OIDC provider ARN from IssuerURL %s: %w", hcp.Spec.IssuerURL, err)
	}

	log.Info("Extracted OIDC provider URL", "oidcProviderURL", oidcProviderArn)

	// Create IAM client
	iamClient := iam.New(awsSession)

	// Check if the OIDC identity provider exists
	log.Info("Checking if OIDC identity provider exists in IAM")
	exists, err := checkOIDCIdentityProviderExists(ctx, iamClient, oidcProviderArn)
	if err != nil {
		// Check if this is a WebIdentityErr which indicates credentials are not available
		if awsErr, ok := err.(awserr.Error); ok && awsErr.Code() == "WebIdentityErr" {
			log.Info("WebIdentityErr encountered during OIDC validation - attempting automatic recovery using S3 credentials")

			// Try automatic recovery using S3 credentials from hypershift-operator
			if recoveryErr := autoRecoverOIDCIdentityProviderWithS3Credentials(ctx, c, hcp, oidcProviderArn); recoveryErr != nil {
				log.Error(recoveryErr, "Failed to auto-recover OIDC identity provider with S3 credentials", "originalError", err, "recoveryError", recoveryErr)
				return fmt.Errorf("identity provider not ready: %w", recoveryErr)
			}

			log.Info("Successfully recovered OIDC identity provider using S3 credentials")
			return nil
		}
		return fmt.Errorf("failed to check if OIDC identity provider exists: %w", err)
	}

	if !exists {
		log.Info("OIDC identity provider does not exist, attempting to recreate")
		// Try to recreate the OIDC identity provider
		if err := recreateOIDCIdentityProvider(ctx, iamClient, hcp.Spec.IssuerURL, oidcProviderArn); err != nil {
			// Check if this is a WebIdentityErr which indicates credentials are not available
			if awsErr, ok := err.(awserr.Error); ok && awsErr.Code() == "WebIdentityErr" {
				log.Info("WebIdentityErr encountered during OIDC recreation - attempting automatic recovery using S3 credentials")

				// Try automatic recovery using S3 credentials from hypershift-operator
				if recoveryErr := autoRecoverOIDCIdentityProviderWithS3Credentials(ctx, c, hcp, oidcProviderArn); recoveryErr != nil {
					log.Error(recoveryErr, "Failed to auto-recover OIDC identity provider with S3 credentials", "originalError", err, "recoveryError", recoveryErr)
					return fmt.Errorf("identity provider not ready: %w", err)
				}

				log.Info("Successfully recovered OIDC identity provider using S3 credentials")
				return nil
			}
			return fmt.Errorf("failed to recreate OIDC identity provider: %w", err)
		}
		log.Info("Successfully recreated OIDC identity provider")
	} else {
		log.Info("OIDC identity provider already exists")
	}

	log.Info("OIDC validation completed successfully", "namespace", hcp.Namespace, "name", hcp.Name)
	return nil
}

// extractOIDCProviderArnFromIssuerURL extracts the OIDC provider ARN from the IssuerURL
// TODO(AI): create tests
func extractOIDCProviderArnFromIssuerURL(issuerURL string) (string, error) {
	// Parse the IssuerURL to extract bucket name and region
	// Format: https://bucket-name.s3.region.amazonaws.com/path
	if !strings.HasPrefix(issuerURL, "https://") {
		return "", fmt.Errorf("invalid IssuerURL format: must start with https://")
	}

	// Remove https:// prefix
	url := strings.TrimPrefix(issuerURL, "https://")

	// Remove any path from the URL (e.g., /cluster-infra-id)
	if idx := strings.Index(url, "/"); idx != -1 {
		url = url[:idx]
	}

	// Split by .s3. to get bucket name and region
	parts := strings.Split(url, ".s3.")
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid IssuerURL format: expected bucket-name.s3.region.amazonaws.com")
	}

	bucketName := parts[0]
	regionAndDomain := parts[1]

	// Extract region from regionAndDomain (e.g., "us-east-1.amazonaws.com")
	regionParts := strings.Split(regionAndDomain, ".")
	if len(regionParts) < 3 {
		return "", fmt.Errorf("invalid IssuerURL format: cannot extract region")
	}
	// Check that it ends with amazonaws.com
	if len(regionParts) < 3 || regionParts[len(regionParts)-2] != "amazonaws" || regionParts[len(regionParts)-1] != "com" {
		return "", fmt.Errorf("invalid IssuerURL format: cannot extract region")
	}
	region := regionParts[0]

	// Construct the OIDC provider ARN
	// Format: arn:aws:iam::ACCOUNT-ID:oidc-provider/bucket-name.s3.region.amazonaws.com
	// We need to get the account ID from the AWS session, but for now we'll construct a pattern
	// that can be used to list and match OIDC providers
	oidcProviderURL := fmt.Sprintf("%s.s3.%s.amazonaws.com", bucketName, region)

	return oidcProviderURL, nil
}

// checkOIDCIdentityProviderExists checks if the OIDC identity provider exists in IAM
func checkOIDCIdentityProviderExists(ctx context.Context, iamClient iamiface.IAMAPI, oidcProviderURL string) (bool, error) {
	log := ctrl.LoggerFrom(ctx).WithName("oidc-check")

	// List OIDC identity providers
	input := &iam.ListOpenIDConnectProvidersInput{}

	log.Info("Listing OIDC identity providers in IAM")
	result, err := iamClient.ListOpenIDConnectProvidersWithContext(ctx, input)
	if err != nil {
		log.Error(err, "Failed to list OIDC identity providers")
		return false, fmt.Errorf("failed to list OIDC identity providers: %w", err)
	}

	log.Info("Found OIDC identity providers in IAM", "count", len(result.OpenIDConnectProviderList))

	// Check if our OIDC provider URL exists
	for i, provider := range result.OpenIDConnectProviderList {
		log.V(1).Info("Checking provider", "index", i+1, "arn", *provider.Arn)

		// Get the provider details to check the URL
		providerInput := &iam.GetOpenIDConnectProviderInput{
			OpenIDConnectProviderArn: provider.Arn,
		}

		providerDetails, err := iamClient.GetOpenIDConnectProviderWithContext(ctx, providerInput)
		if err != nil {
			log.V(1).Info("Failed to get details for provider", "arn", *provider.Arn, "error", err)
			// If we can't get details, continue to next provider
			continue
		}

		// Check if the URL matches
		if providerDetails.Url != nil && *providerDetails.Url == oidcProviderURL {
			log.Info("Found matching OIDC provider", "arn", *provider.Arn, "url", *providerDetails.Url)
			return true, nil
		} else if providerDetails.Url != nil {
			log.V(1).Info("Provider URL does not match", "arn", *provider.Arn, "url", *providerDetails.Url, "expectedURL", oidcProviderURL)
		}
	}

	log.Info("No matching OIDC provider found", "expectedURL", oidcProviderURL)
	return false, nil
}

// recreateOIDCIdentityProvider recreates the OIDC identity provider in IAM
func recreateOIDCIdentityProvider(ctx context.Context, iamClient iamiface.IAMAPI, issuerURL, oidcProviderURL string) error {
	log := ctrl.LoggerFrom(ctx).WithName("oidc-recreate")
	log.Info("Recreating OIDC identity provider", "oidcProviderURL", oidcProviderURL)

	// Extract the path from the IssuerURL for the thumbprint
	// The path should be something like "/cluster-infra-id"
	urlParts := strings.Split(issuerURL, ".amazonaws.com")
	if len(urlParts) != 2 {
		return fmt.Errorf("invalid IssuerURL format: cannot extract path")
	}
	path := urlParts[1]
	if path == "" {
		path = "/"
	}

	log.Info("Extracted path from IssuerURL", "path", path)

	// Get the thumbprint for the OIDC provider
	log.Info("Getting OIDC thumbprint")
	thumbprint, err := getOIDCThumbprint(ctx, issuerURL)
	if err != nil {
		log.Error(err, "Failed to get OIDC thumbprint")
		return fmt.Errorf("failed to get OIDC thumbprint: %w", err)
	}

	log.Info("Using thumbprint", "thumbprint", thumbprint)

	// Create the OIDC identity provider
	input := &iam.CreateOpenIDConnectProviderInput{
		Url:            aws.String(oidcProviderURL),
		ThumbprintList: []*string{aws.String(thumbprint)},
		ClientIDList:   []*string{aws.String("sts.amazonaws.com")},
	}

	log.Info("Creating OIDC identity provider", "oidcProviderURL", oidcProviderURL)
	_, err = iamClient.CreateOpenIDConnectProviderWithContext(ctx, input)
	if err != nil {
		if awsErr, ok := err.(awserr.Error); ok {
			if awsErr.Code() == iam.ErrCodeEntityAlreadyExistsException {
				// Provider already exists, which is fine
				log.Info("OIDC identity provider already exists")
				return nil
			}
		}
		return fmt.Errorf("failed to create OIDC identity provider: %w", err)
	}

	log.Info("Successfully created OIDC identity provider")
	return nil
}

// OIDCConfiguration represents the OIDC discovery document
type OIDCConfiguration struct {
	JWKSURI string `json:"jwks_uri"`
}

// getOIDCThumbprint gets the thumbprint for the OIDC provider by fetching the certificate
// TODO(AI): create tests using mockgen
func getOIDCThumbprint(ctx context.Context, issuerURL string) (string, error) {
	log := ctrl.LoggerFrom(ctx).WithName("oidc-thumbprint")

	// For S3-based OIDC providers, we need to fetch the certificate from the well-known endpoint
	wellKnownURL := issuerURL + "/.well-known/openid-configuration"
	log.Info("Fetching OIDC configuration", "wellKnownURL", wellKnownURL)
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	req, err := http.NewRequestWithContext(ctx, "GET", wellKnownURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch OIDC configuration: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to fetch OIDC configuration: status %d", resp.StatusCode)
	}

	// Read the response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read OIDC configuration response: %w", err)
	}

	log.Info("Successfully fetched OIDC configuration")

	// Parse the OIDC configuration JSON
	var oidcConfig OIDCConfiguration
	if err := json.Unmarshal(body, &oidcConfig); err != nil {
		return "", fmt.Errorf("failed to parse OIDC configuration JSON: %w", err)
	}

	if oidcConfig.JWKSURI == "" {
		return "", fmt.Errorf("no JWKS URI found in OIDC configuration")
	}

	log.Info("Found JWKS URI", "jwksURI", oidcConfig.JWKSURI)

	// For S3-based OIDC providers, we need to get the certificate from the HTTPS connection
	// to the issuer URL itself, not from the JWKS endpoint
	thumbprint, err := getCertificateThumbprint(ctx, issuerURL)
	if err != nil {
		return "", fmt.Errorf("failed to get certificate thumbprint: %w", err)
	}

	log.Info("Calculated thumbprint", "thumbprint", thumbprint)
	return thumbprint, nil
}

// getCertificateThumbprint gets the SHA-1 thumbprint of the certificate from the issuer URL
func getCertificateThumbprint(ctx context.Context, issuerURL string) (string, error) {
	log := ctrl.LoggerFrom(ctx).WithName("certificate-thumbprint")

	// Parse the URL to get the hostname
	if !strings.HasPrefix(issuerURL, "https://") {
		return "", fmt.Errorf("issuer URL must use HTTPS")
	}

	hostname := strings.TrimPrefix(issuerURL, "https://")
	// Remove any path from the hostname
	if idx := strings.Index(hostname, "/"); idx != -1 {
		hostname = hostname[:idx]
	}

	log.Info("Getting certificate thumbprint", "hostname", hostname)

	// Create a TLS connection to get the certificate
	dialer := &net.Dialer{
		Timeout: 30 * time.Second,
	}
	conn, err := tls.DialWithDialer(dialer, "tcp", hostname+":443", &tls.Config{
		ServerName: hostname,
	})
	if err != nil {
		return "", fmt.Errorf("failed to establish TLS connection: %w", err)
	}
	defer conn.Close()

	// Get the certificate chain
	state := conn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return "", fmt.Errorf("no certificates found")
	}

	// Use the first certificate in the chain (the leaf certificate)
	cert := state.PeerCertificates[0]

	// Calculate SHA-1 thumbprint
	thumbprint := sha1.Sum(cert.Raw)
	thumbprintHex := fmt.Sprintf("%x", thumbprint)

	log.Info("Certificate details", "subject", cert.Subject, "issuer", cert.Issuer, "thumbprint", thumbprintHex)

	return thumbprintHex, nil
}

// validateOIDCDocumentsInS3 validates that the OIDC documents in S3 are accessible
func validateOIDCDocumentsInS3(ctx context.Context, issuerURL string) error {
	log := ctrl.LoggerFrom(ctx).WithName("oidc-s3-validation")

	// Check if the OIDC configuration document is accessible
	wellKnownURL := issuerURL + "/.well-known/openid-configuration"

	log.Info("Validating OIDC documents in S3", "wellKnownURL", wellKnownURL)

	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	req, err := http.NewRequestWithContext(ctx, "GET", wellKnownURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to fetch OIDC configuration: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to fetch OIDC configuration: status %d", resp.StatusCode)
	}

	log.Info("Successfully validated OIDC documents in S3")
	return nil
}

// autoRecoverOIDCIdentityProviderWithS3Credentials attempts to recover the OIDC identity provider
// by using S3 credentials from the hypershift-operator secret to recreate the OIDC provider in IAM.
// This handles the case where the OIDC provider was deleted but the S3 documents still exist.
func autoRecoverOIDCIdentityProviderWithS3Credentials(ctx context.Context, c client.Client, hcp *hyperv1.HostedControlPlane, oidcProviderURL string) error {
	log := ctrl.LoggerFrom(ctx).WithName("oidc-auto-recovery")
	log.Info("Starting automatic OIDC recovery using S3 credentials")

	// Step 1: Get S3 credentials from hypershift-operator secret
	s3Creds, err := getS3CredentialsFromSecret(ctx, c, "hypershift", "hypershift-operator-oidc-provider-s3-credentials")
	if err != nil {
		return fmt.Errorf("failed to get S3 credentials from hypershift-operator: %w", err)
	}

	log.Info("Successfully obtained S3 credentials from hypershift-operator")

	iamClient := iam.New(s3Creds)

	// Step 3: Recreate the OIDC identity provider using the S3 credentials
	log.Info("Recreating OIDC identity provider using S3 credentials", "oidcProviderURL", oidcProviderURL)
	if err := recreateOIDCIdentityProvider(ctx, iamClient, hcp.Spec.IssuerURL, oidcProviderURL); err != nil {
		return fmt.Errorf("failed to recreate OIDC identity provider with S3 credentials: %w", err)
	}

	log.Info("Successfully recreated OIDC identity provider using S3 credentials")
	return nil
}

// getS3CredentialsFromSecret retrieves and parses S3 credentials from a Kubernetes secret
// and returns an AWS session that can be used to create IAM clients.
func getS3CredentialsFromSecret(ctx context.Context, c client.Client, namespace, secretName string) (*session.Session, error) {
	log := ctrl.LoggerFrom(ctx).WithName("s3-credentials")

	// Get the secret containing S3 credentials
	secret := &corev1.Secret{}
	secretKey := client.ObjectKey{Namespace: namespace, Name: secretName}
	if err := c.Get(ctx, secretKey, secret); err != nil {
		return nil, fmt.Errorf("failed to get secret %s/%s: %w", namespace, secretName, err)
	}

	log.Info("Retrieved S3 credentials secret", "secret", secretName)

	// Parse the credentials file
	credentialsData, exists := secret.Data["credentials"]
	if !exists {
		return nil, fmt.Errorf("secret %s/%s does not contain 'credentials' key", namespace, secretName)
	}

	// Parse the AWS credentials file format
	accessKeyID, secretAccessKey, err := parseAWSCredentialsFile(string(credentialsData))
	if err != nil {
		return nil, fmt.Errorf("failed to parse AWS credentials: %w", err)
	}

	log.Info("Successfully parsed AWS credentials from secret")

	// Create AWS session with static credentials
	staticCreds := credentials.NewStaticCredentials(accessKeyID, secretAccessKey, "")
	awsConfig := aws.NewConfig().WithCredentials(staticCreds)

	// Get region from secret or use default
	if region, exists := secret.Data["region"]; exists {
		awsConfig.WithRegion(string(region))
	} else {
		// Default to us-east-1 if no region specified
		awsConfig.WithRegion("us-east-1")
	}

	sess, err := session.NewSession(awsConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create AWS session: %w", err)
	}

	return sess, nil
}

// parseAWSCredentialsFile parses AWS credentials file format and extracts access key and secret key.
// Expected format:
// [default]
// aws_access_key_id = ACCESS_KEY
// aws_secret_access_key = SECRET_KEY
// TODO(AI): create tests
func parseAWSCredentialsFile(credentialsContent string) (string, string, error) {
	lines := strings.Split(credentialsContent, "\n")
	var accessKeyID, secretAccessKey string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "aws_access_key_id") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				accessKeyID = strings.TrimSpace(parts[1])
			}
		} else if strings.HasPrefix(line, "aws_secret_access_key") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				secretAccessKey = strings.TrimSpace(parts[1])
			}
		}
	}

	if accessKeyID == "" || secretAccessKey == "" {
		return "", "", fmt.Errorf("failed to parse AWS credentials: missing access key or secret key")
	}

	return accessKeyID, secretAccessKey, nil
}
