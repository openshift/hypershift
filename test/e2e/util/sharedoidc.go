package util

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	awsinfra "github.com/openshift/hypershift/cmd/infra/aws"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/oidc"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	s3 "github.com/aws/aws-sdk-go/service/s3"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// setup a shared OIDC provider to be used by all HostedClusters
func SetupSharedOIDCProvider(opts *Options, artifactDir string) error {
	if opts.ConfigurableClusterOptions.AWSOidcS3BucketName == "" {
		return errors.New("please supply a public S3 bucket name with --e2e.aws-oidc-s3-bucket-name")
	}

	iamClient := GetIAMClient(opts.ConfigurableClusterOptions.AWSCredentialsFile, opts.ConfigurableClusterOptions.Region)
	s3Client := GetS3Client(opts.ConfigurableClusterOptions.AWSCredentialsFile, opts.ConfigurableClusterOptions.Region)

	providerID := SimpleNameGenerator.GenerateName("e2e-oidc-provider-")
	issuerURL := fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", opts.ConfigurableClusterOptions.AWSOidcS3BucketName, opts.ConfigurableClusterOptions.Region, providerID)

	key, err := certs.PrivateKey()
	if err != nil {
		return fmt.Errorf("failed generating a private key: %w", err)
	}

	keyBytes := certs.PrivateKeyToPem(key)
	publicKeyBytes, err := certs.PublicKeyToPem(&key.PublicKey)
	if err != nil {
		return fmt.Errorf("failed to generate public key from private key: %w", err)
	}

	// create openid configuration
	params := oidc.ODICGeneratorParams{
		IssuerURL: issuerURL,
		PubKey:    publicKeyBytes,
	}

	oidcGenerators := map[string]oidc.OIDCDocumentGeneratorFunc{
		"/.well-known/openid-configuration": oidc.GenerateConfigurationDocument,
		oidc.JWKSURI:                        oidc.GenerateJWKSDocument,
	}

	for path, generator := range oidcGenerators {
		bodyReader, err := generator(params)
		if err != nil {
			return fmt.Errorf("failed to generate OIDC document %s: %w", path, err)
		}
		_, err = s3Client.PutObject(&s3.PutObjectInput{
			Body:   bodyReader,
			Bucket: aws.String(opts.ConfigurableClusterOptions.AWSOidcS3BucketName),
			Key:    aws.String(providerID + path),
		})
		if err != nil {
			wrapped := fmt.Errorf("failed to upload %s to the %s s3 bucket", path, opts.ConfigurableClusterOptions.AWSOidcS3BucketName)
			if awsErr, ok := err.(awserr.Error); ok {
				// Generally, the underlying message from AWS has unique per-request
				// info not suitable for publishing as condition messages, so just
				// return the code.
				wrapped = fmt.Errorf("%w: aws returned an error: %s", wrapped, awsErr.Code())
			}
			return wrapped
		}
	}

	iamOptions := awsinfra.CreateIAMOptions{
		IssuerURL:      issuerURL,
		AdditionalTags: opts.AdditionalTags,
	}
	if err := iamOptions.ParseAdditionalTags(); err != nil {
		return fmt.Errorf("failed to parse additional tags: %w", err)
	}

	createLogFile := filepath.Join(artifactDir, "create-oidc-provider.log")
	createLog, err := os.Create(createLogFile)
	if err != nil {
		return fmt.Errorf("failed to create create log: %w", err)
	}
	createLogger := zap.New(zapcore.NewCore(zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()), zapcore.Lock(createLog), zap.DebugLevel))
	defer func() {
		if err := createLogger.Sync(); err != nil {
			fmt.Printf("failed to sync createLogger: %v\n", err)
		}
	}()

	if _, err := iamOptions.CreateOIDCProvider(iamClient, zapr.NewLogger(createLogger)); err != nil {
		return fmt.Errorf("failed to create OIDC provider: %w", err)
	}

	opts.IssuerURL = issuerURL
	opts.ServiceAccountSigningKey = keyBytes

	return nil
}

func CleanupSharedOIDCProvider(opts *Options, log logr.Logger) {
	iamClient := GetIAMClient(opts.ConfigurableClusterOptions.AWSCredentialsFile, opts.ConfigurableClusterOptions.Region)
	s3Client := GetS3Client(opts.ConfigurableClusterOptions.AWSCredentialsFile, opts.ConfigurableClusterOptions.Region)

	DestroyOIDCProvider(log, iamClient, opts.IssuerURL)
	CleanupOIDCBucketObjects(log, s3Client, opts.ConfigurableClusterOptions.AWSOidcS3BucketName, opts.IssuerURL)
}
