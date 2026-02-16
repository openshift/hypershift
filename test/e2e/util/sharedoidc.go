package util

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	awsinfra "github.com/openshift/hypershift/cmd/infra/aws"
	"github.com/openshift/hypershift/support/awsapi"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/oidc"

	awsv2 "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func oidcProviderClients(ctx context.Context, opts *Options) (awsapi.IAMAPI, awsapi.S3API) {
	iamCredsFile := opts.ConfigurableClusterOptions.AWSCredentialsFile
	s3CredsFile := opts.HOInstallationOptions.AWSOidcS3Credentials
	if s3CredsFile == "" {
		s3CredsFile = iamCredsFile
	}

	iamRegion := opts.ConfigurableClusterOptions.Region
	s3Region := opts.HOInstallationOptions.AWSOidcS3Region
	if s3Region == "" {
		s3Region = iamRegion
	}

	iamClient := GetIAMClient(ctx, iamCredsFile, iamRegion)
	s3Client := GetS3Client(ctx, s3CredsFile, s3Region)

	return iamClient, s3Client
}

// setup a shared OIDC provider to be used by all HostedClusters
func SetupSharedOIDCProvider(opts *Options, artifactDir string) error {
	if opts.ConfigurableClusterOptions.AWSOidcS3BucketName == "" {
		return errors.New("please supply a public S3 bucket name with --e2e.aws-oidc-s3-bucket-name")
	}

	ctx := context.Background()
	iamClient, s3Client := oidcProviderClients(ctx, opts)

	s3Region := opts.HOInstallationOptions.AWSOidcS3Region
	if s3Region == "" {
		s3Region = opts.ConfigurableClusterOptions.Region
	}

	providerID := SimpleNameGenerator.GenerateName("e2e-oidc-provider-")
	issuerURL := fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", opts.ConfigurableClusterOptions.AWSOidcS3BucketName, s3Region, providerID)

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
	params := oidc.OIDCGeneratorParams{
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
		_, err = s3Client.PutObject(ctx, &s3.PutObjectInput{
			Body:   bodyReader,
			Bucket: awsv2.String(opts.ConfigurableClusterOptions.AWSOidcS3BucketName),
			Key:    awsv2.String(providerID + path),
		})
		if err != nil {
			wrapped := fmt.Errorf("failed to upload %s to the %s s3 bucket: %w", path, opts.ConfigurableClusterOptions.AWSOidcS3BucketName, err)
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

	if _, err := iamOptions.CreateOIDCProvider(ctx, iamClient, zapr.NewLogger(createLogger)); err != nil {
		return fmt.Errorf("failed to create OIDC provider: %w", err)
	}

	opts.IssuerURL = issuerURL
	opts.ServiceAccountSigningKey = keyBytes

	return nil
}

func CleanupSharedOIDCProvider(opts *Options, log logr.Logger) {
	ctx := context.Background()
	iamClient, s3Client := oidcProviderClients(ctx, opts)

	DestroyOIDCProvider(ctx, log, iamClient, opts.IssuerURL)
	CleanupOIDCBucketObjects(ctx, log, s3Client, opts.ConfigurableClusterOptions.AWSOidcS3BucketName, opts.IssuerURL)
}
