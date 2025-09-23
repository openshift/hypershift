package framework

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/go-logr/zapr"
	awsinfra "github.com/openshift/hypershift/cmd/infra/aws"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/oidc"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// setupAWSResources sets up shared AWS resources for testing
func (f *Framework) setupAWSResources(ctx context.Context) error {
	f.logger.Info("Setting up AWS resources")

	// Setup shared OIDC provider if S3 bucket is specified
	if f.opts.AWSOidcS3BucketName != "" {
		if err := f.setupAWSSharedOIDCProvider(ctx); err != nil {
			return fmt.Errorf("failed to setup shared OIDC provider: %w", err)
		}
	}

	f.logger.Info("AWS resources setup completed")
	return nil
}

// cleanupAWSResources cleans up shared AWS resources
func (f *Framework) cleanupAWSResources(ctx context.Context) error {
	f.logger.Info("Cleaning up AWS resources")

	// Cleanup shared OIDC provider
	if f.sharedResources.OIDCProviderURL != "" {
		if err := f.cleanupAWSSharedOIDCProvider(ctx); err != nil {
			f.logger.Error(err, "Failed to cleanup shared OIDC provider")
			return err
		}
	}

	f.logger.Info("AWS resources cleanup completed")
	return nil
}

// setupAWSSharedOIDCProvider creates a shared OIDC provider for AWS tests
func (f *Framework) setupAWSSharedOIDCProvider(ctx context.Context) error {
	f.logger.Info("Setting up shared AWS OIDC provider", "bucket", f.opts.AWSOidcS3BucketName)

	// Get AWS clients
	iamClient := e2eutil.GetIAMClient(f.opts.AWSCredentialsFile, f.opts.AWSRegion)
	s3Client := e2eutil.GetS3Client(f.opts.AWSCredentialsFile, f.opts.AWSRegion)

	// Generate provider ID and issuer URL
	providerID := e2eutil.SimpleNameGenerator.GenerateName("e2e-oidc-provider-")
	issuerURL := fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s",
		f.opts.AWSOidcS3BucketName, f.opts.AWSRegion, providerID)

	// Generate private key for signing
	key, err := certs.PrivateKey()
	if err != nil {
		return fmt.Errorf("failed to generate private key: %w", err)
	}

	keyBytes := certs.PrivateKeyToPem(key)
	publicKeyBytes, err := certs.PublicKeyToPem(&key.PublicKey)
	if err != nil {
		return fmt.Errorf("failed to generate public key: %w", err)
	}

	// Create OIDC configuration documents
	params := oidc.ODICGeneratorParams{
		IssuerURL: issuerURL,
		PubKey:    publicKeyBytes,
	}

	oidcGenerators := map[string]oidc.OIDCDocumentGeneratorFunc{
		"/.well-known/openid-configuration": oidc.GenerateConfigurationDocument,
		oidc.JWKSURI:                        oidc.GenerateJWKSDocument,
	}

	// Upload OIDC documents to S3
	for path, generator := range oidcGenerators {
		bodyReader, err := generator(params)
		if err != nil {
			return fmt.Errorf("failed to generate OIDC document %s: %w", path, err)
		}

		_, err = s3Client.PutObject(&s3.PutObjectInput{
			Body:   bodyReader,
			Bucket: aws.String(f.opts.AWSOidcS3BucketName),
			Key:    aws.String(providerID + path),
		})
		if err != nil {
			return fmt.Errorf("failed to upload OIDC document %s to S3: %w", path, err)
		}
	}

	// Create IAM OIDC provider
	iamOptions := awsinfra.CreateIAMOptions{
		IssuerURL: issuerURL,
	}

	// Create a logger for the IAM operations
	zapLogger := zap.New(zapcore.NewCore(
		zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()),
		zapcore.Lock(zapcore.AddSync(&loggerWriter{f.logger})),
		zap.DebugLevel,
	))

	providerARN, err := iamOptions.CreateOIDCProvider(iamClient, zapr.NewLogger(zapLogger))
	if err != nil {
		return fmt.Errorf("failed to create IAM OIDC provider: %w", err)
	}

	// Store the shared resources
	f.sharedResources.OIDCProviderURL = issuerURL
	f.sharedResources.OIDCSigningKey = keyBytes
	f.sharedResources.OIDCProviderARN = providerARN

	f.logger.Info("Shared AWS OIDC provider created successfully",
		"issuerURL", issuerURL, "providerARN", providerARN)

	return nil
}

// cleanupAWSSharedOIDCProvider cleans up the shared OIDC provider
func (f *Framework) cleanupAWSSharedOIDCProvider(ctx context.Context) error {
	f.logger.Info("Cleaning up shared AWS OIDC provider", "issuerURL", f.sharedResources.OIDCProviderURL)

	// Get AWS clients
	iamClient := e2eutil.GetIAMClient(f.opts.AWSCredentialsFile, f.opts.AWSRegion)
	s3Client := e2eutil.GetS3Client(f.opts.AWSCredentialsFile, f.opts.AWSRegion)

	// Cleanup IAM OIDC provider
	e2eutil.DestroyOIDCProvider(f.logger, iamClient, f.sharedResources.OIDCProviderURL)

	// Cleanup S3 objects
	e2eutil.CleanupOIDCBucketObjects(f.logger, s3Client, f.opts.AWSOidcS3BucketName, f.sharedResources.OIDCProviderURL)

	f.logger.Info("Shared AWS OIDC provider cleanup completed")
	return nil
}

// loggerWriter adapts logr.Logger to io.Writer for zap
type loggerWriter struct {
	logger interface {
		Info(msg string, keysAndValues ...interface{})
	}
}

func (w *loggerWriter) Write(p []byte) (n int, err error) {
	w.logger.Info(string(p))
	return len(p), nil
}