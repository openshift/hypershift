package util

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	awsinfra "github.com/openshift/hypershift/cmd/infra/aws"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/oidc"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/iam/iamiface"
	s3 "github.com/aws/aws-sdk-go/service/s3"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func oidcProviderClients(opts *Options) (iamiface.IAMAPI, *s3.S3) {
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

	iamClient := GetIAMClient(iamCredsFile, iamRegion)
	s3Client := GetS3Client(s3CredsFile, s3Region)

	return iamClient, s3Client
}

// setup a shared OIDC provider to be used by all HostedClusters
func SetupSharedOIDCProvider(opts *Options, artifactDir string) error {
	if opts.ConfigurableClusterOptions.AWSOidcS3BucketName == "" {
		return errors.New("please supply a public S3 bucket name with --e2e.aws-oidc-s3-bucket-name")
	}

	// If the HO oidc s3 credentials are set, use them, otherwise use the creds of the hosted cluster
	iamClient, s3Client := oidcProviderClients(opts)

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
			Body:        bodyReader,
			Bucket:      aws.String(opts.ConfigurableClusterOptions.AWSOidcS3BucketName),
			Key:         aws.String(providerID + path),
			ContentType: aws.String("application/json"),
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

	// Validate documents are publicly retrievable before creating the IAM OIDC provider.
	if err := verifyOIDCDocumentsPubliclyAccessible(issuerURL, s3Client, opts.ConfigurableClusterOptions.AWSOidcS3BucketName, providerID, artifactDir); err != nil {
		return fmt.Errorf("oidc discovery documents are not publicly accessible: %w", err)
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
	iamClient, s3Client := oidcProviderClients(opts)

	DestroyOIDCProvider(log, iamClient, opts.IssuerURL)
	CleanupOIDCBucketObjects(log, s3Client, opts.ConfigurableClusterOptions.AWSOidcS3BucketName, opts.IssuerURL)
}

// verifyOIDCDocumentsPubliclyAccessible attempts anonymous HTTP GETs to the OIDC discovery
// endpoints and, if they fail, records detailed diagnostics along with S3 public access settings
// into an artifact log file to aid troubleshooting.
func verifyOIDCDocumentsPubliclyAccessible(issuerURL string, s3Client *s3.S3, bucketName, providerID, artifactDir string) error {
	logFile := filepath.Join(artifactDir, "oidc-access-check.log")
	f, err := os.Create(logFile)
	if err != nil {
		return fmt.Errorf("failed to create access check log: %w", err)
	}
	defer func() { _ = f.Close() }()

	writef := func(format string, args ...interface{}) { _, _ = fmt.Fprintf(f, format+"\n", args...) }

	writef("issuerURL=%s", issuerURL)
	client := &http.Client{Timeout: 15 * time.Second}
	paths := []string{"/.well-known/openid-configuration", oidc.JWKSURI}
	var firstErr error
	for _, p := range paths {
		url := issuerURL + p
		writef("checking public GET %s", url)
		resp, err := client.Get(url)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			writef("HTTP GET error: %v", err)
			continue
		}
		func() {
			defer resp.Body.Close()
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
			writef("status=%d", resp.StatusCode)
			for k, v := range resp.Header {
				writef("header %s: %v", k, v)
			}
			writef("body_prefix=%q", string(body))
		}()
		if resp.StatusCode != http.StatusOK {
			if firstErr == nil {
				firstErr = fmt.Errorf("unexpected status %d for %s", resp.StatusCode, url)
			}
		}
	}

	if firstErr != nil {
		writef("collecting S3 diagnostics for bucket %q and providerID %q", bucketName, providerID)
		for _, p := range paths {
			key := providerID + p
			writef("HeadObject bucket=%s key=%s", bucketName, key)
			if _, err := s3Client.HeadObject(&s3.HeadObjectInput{Bucket: aws.String(bucketName), Key: aws.String(key)}); err != nil {
				writef("HeadObject error: %v", err)
			} else {
				writef("HeadObject succeeded")
			}
		}
		writef("GetPublicAccessBlock bucket=%s", bucketName)
		if pab, err := s3Client.GetPublicAccessBlock(&s3.GetPublicAccessBlockInput{Bucket: aws.String(bucketName)}); err != nil {
			writef("GetPublicAccessBlock error: %v", err)
		} else if pab != nil && pab.PublicAccessBlockConfiguration != nil {
			cfg := pab.PublicAccessBlockConfiguration
			writef("PublicAccessBlock: BlockPublicAcls=%v BlockPublicPolicy=%v IgnorePublicAcls=%v RestrictPublicBuckets=%v",
				aws.BoolValue(cfg.BlockPublicAcls), aws.BoolValue(cfg.BlockPublicPolicy), aws.BoolValue(cfg.IgnorePublicAcls), aws.BoolValue(cfg.RestrictPublicBuckets))
		}
		return firstErr
	}

	return nil
}
