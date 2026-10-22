//go:build e2ev2

package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	cmdutil "github.com/openshift/hypershift/cmd/util"
	supportawsutil "github.com/openshift/hypershift/support/awsutil"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/oidc"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/google/uuid"
)

const SharedOIDCStateFileName = "aws-shared-oidc.json"

type SharedOIDCOptions struct {
	Bucket             string
	IAMCredentialsFile string
	IAMRegion          string
	S3CredentialsFile  string
	S3Region           string
	AdditionalTags     []string
	ProwJobID          string
}

type SharedOIDCInputs struct {
	IssuerURL      string
	SigningKeyFile string
}

type sharedOIDCState struct {
	Options    SharedOIDCOptions
	ProviderID string
	IssuerURL  string
}

type sharedOIDCIAM interface {
	CreateOpenIDConnectProvider(context.Context, *iam.CreateOpenIDConnectProviderInput, ...func(*iam.Options)) (*iam.CreateOpenIDConnectProviderOutput, error)
	ListOpenIDConnectProviders(context.Context, *iam.ListOpenIDConnectProvidersInput, ...func(*iam.Options)) (*iam.ListOpenIDConnectProvidersOutput, error)
	DeleteOpenIDConnectProvider(context.Context, *iam.DeleteOpenIDConnectProviderInput, ...func(*iam.Options)) (*iam.DeleteOpenIDConnectProviderOutput, error)
}

type sharedOIDCS3 interface {
	PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	DeleteObject(context.Context, *s3.DeleteObjectInput, ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
}

type sharedOIDCClientFactory func(context.Context, SharedOIDCOptions) (sharedOIDCIAM, sharedOIDCS3, error)

func sharedOIDCClients(ctx context.Context, options SharedOIDCOptions) (sharedOIDCIAM, sharedOIDCS3, error) {
	return sharedOIDCClientsWithLoader(ctx, options, config.LoadDefaultConfig)
}

func sharedOIDCClientsWithLoader(ctx context.Context, options SharedOIDCOptions, load func(context.Context, ...func(*config.LoadOptions) error) (aws.Config, error)) (sharedOIDCIAM, sharedOIDCS3, error) {
	iamConfig, err := load(ctx, sharedOIDCConfigOptions(options.IAMRegion, options.IAMCredentialsFile)...)
	if err != nil {
		return nil, nil, fmt.Errorf("loading shared OIDC IAM configuration: %w", err)
	}
	s3Config, err := load(ctx, sharedOIDCConfigOptions(options.S3Region, options.S3CredentialsFile)...)
	if err != nil {
		return nil, nil, fmt.Errorf("loading shared OIDC S3 configuration: %w", err)
	}
	return iam.NewFromConfig(iamConfig), s3.NewFromConfig(s3Config), nil
}

func sharedOIDCConfigOptions(region, credentialsFile string) []func(*config.LoadOptions) error {
	options := []func(*config.LoadOptions) error{config.WithRegion(region)}
	if credentialsFile == "" {
		credentialsFile = os.Getenv("AWS_SHARED_CREDENTIALS_FILE")
	}
	if credentialsFile != "" {
		options = append(options, config.WithSharedConfigFiles([]string{credentialsFile}), config.WithSharedCredentialsFiles([]string{credentialsFile}))
	}
	return options
}

func SetupSharedOIDC(ctx context.Context, sharedDir string, options SharedOIDCOptions) (SharedOIDCInputs, error) {
	return setupSharedOIDC(ctx, sharedDir, options, sharedOIDCClients)
}

func setupSharedOIDC(ctx context.Context, sharedDir string, options SharedOIDCOptions, clients sharedOIDCClientFactory) (inputs SharedOIDCInputs, resultErr error) {
	if options.S3CredentialsFile == "" {
		options.S3CredentialsFile = options.IAMCredentialsFile
	}
	if options.S3Region == "" {
		options.S3Region = options.IAMRegion
	}
	if options.Bucket == "" || options.IAMRegion == "" {
		return inputs, fmt.Errorf("automatic shared AWS OIDC requires AWS_OIDC_S3_BUCKET_NAME and HYPERSHIFT_AWS_REGION")
	}
	providerID := "e2e-oidc-provider-" + uuid.NewString()
	tags, err := cmdutil.ParseAWSTags(options.AdditionalTags)
	if err != nil {
		return inputs, fmt.Errorf("parsing shared OIDC tags: %w", err)
	}
	maps.Copy(tags, map[string]string{supportawsutil.HypershiftSourceTagKey: "e2e", supportawsutil.HypershiftInfraIDTagKey: providerID})
	if options.ProwJobID != "" {
		tags[supportawsutil.HypershiftProwJobIDTagKey] = options.ProwJobID
	}
	iamTags := make([]iamtypes.Tag, 0, len(tags))
	for _, key := range slices.Sorted(maps.Keys(tags)) {
		iamTags = append(iamTags, iamtypes.Tag{Key: aws.String(key), Value: aws.String(tags[key])})
	}
	state := sharedOIDCState{Options: options, ProviderID: providerID, IssuerURL: fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", options.Bucket, options.S3Region, providerID)}
	key, err := certs.PrivateKey()
	if err != nil {
		return inputs, err
	}
	publicKey, err := certs.PublicKeyToPem(&key.PublicKey)
	if err != nil {
		return inputs, err
	}
	iamClient, s3Client, err := clients(ctx, options)
	if err != nil {
		return inputs, err
	}
	if err := os.MkdirAll(sharedDir, 0700); err != nil {
		return inputs, err
	}
	stateBytes, err := json.Marshal(state)
	if err != nil {
		return inputs, err
	}
	file, err := os.OpenFile(filepath.Join(sharedDir, SharedOIDCStateFileName), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return inputs, fmt.Errorf("reserving shared OIDC ownership state: %w", err)
	}
	_, writeErr := file.Write(stateBytes)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return inputs, errors.Join(fmt.Errorf("persisting shared OIDC ownership state: %w", err), os.Remove(filepath.Join(sharedDir, SharedOIDCStateFileName)))
	}
	defer func() {
		if resultErr != nil {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
			defer cancel()
			resultErr = errors.Join(resultErr, cleanupSharedOIDC(cleanupCtx, sharedDir, clients))
		}
	}()
	inputs = SharedOIDCInputs{IssuerURL: state.IssuerURL, SigningKeyFile: filepath.Join(sharedDir, providerID+".pem")}
	if err := os.WriteFile(inputs.SigningKeyFile, certs.PrivateKeyToPem(key), 0600); err != nil {
		return inputs, fmt.Errorf("writing shared OIDC signing key: %w", err)
	}
	params := oidc.OIDCGeneratorParams{IssuerURL: state.IssuerURL, PubKey: publicKey}
	for _, document := range sharedOIDCDocuments() {
		body, err := document.generate(params)
		if err != nil {
			return inputs, err
		}
		if _, err := s3Client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(options.Bucket), Key: aws.String(providerID + document.path), Body: body, ContentType: aws.String("application/json")}); err != nil {
			return inputs, fmt.Errorf("publishing shared OIDC document %s: %w", document.path, err)
		}
	}
	provider, err := iamClient.CreateOpenIDConnectProvider(ctx, &iam.CreateOpenIDConnectProviderInput{Url: aws.String(state.IssuerURL), ClientIDList: []string{"openshift", "sts.amazonaws.com"}, ThumbprintList: []string{"A9D53002E97E00E043244F3D170D6F4C414104FD"}, Tags: iamTags})
	if err != nil {
		return inputs, fmt.Errorf("creating shared OIDC provider: %w", err)
	}
	if provider == nil || aws.ToString(provider.OpenIDConnectProviderArn) == "" {
		return inputs, fmt.Errorf("shared OIDC provider creation returned no ARN")
	}
	return inputs, nil
}

type sharedOIDCDocument struct {
	path     string
	generate oidc.OIDCDocumentGeneratorFunc
}

func sharedOIDCDocuments() []sharedOIDCDocument {
	return []sharedOIDCDocument{{"/.well-known/openid-configuration", oidc.GenerateConfigurationDocument}, {oidc.JWKSURI, oidc.GenerateJWKSDocument}}
}

func CleanupSharedOIDC(ctx context.Context, sharedDir string) error {
	return cleanupSharedOIDC(ctx, sharedDir, sharedOIDCClients)
}

func cleanupSharedOIDC(ctx context.Context, sharedDir string, clients sharedOIDCClientFactory) error {
	data, err := os.ReadFile(filepath.Join(sharedDir, SharedOIDCStateFileName))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	state := sharedOIDCState{}
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("decoding shared OIDC ownership state: %w", err)
	}
	if !strings.HasPrefix(state.ProviderID, "e2e-oidc-provider-") || filepath.Base(state.ProviderID) != state.ProviderID || state.Options.Bucket == "" || state.IssuerURL != fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", state.Options.Bucket, state.Options.S3Region, state.ProviderID) {
		return fmt.Errorf("invalid shared OIDC ownership identity")
	}
	iamClient, s3Client, err := clients(ctx, state.Options)
	if err != nil {
		return err
	}
	providerARN := ""
	providers, err := iamClient.ListOpenIDConnectProviders(ctx, &iam.ListOpenIDConnectProvidersInput{})
	if err != nil {
		return fmt.Errorf("recovering shared OIDC provider identity: %w", err)
	}
	for _, provider := range providers.OpenIDConnectProviderList {
		if strings.HasSuffix(aws.ToString(provider.Arn), ":oidc-provider/"+strings.TrimPrefix(state.IssuerURL, "https://")) {
			providerARN = aws.ToString(provider.Arn)
			break
		}
	}
	if providerARN != "" {
		_, err := iamClient.DeleteOpenIDConnectProvider(ctx, &iam.DeleteOpenIDConnectProviderInput{OpenIDConnectProviderArn: aws.String(providerARN)})
		var apiError smithy.APIError
		if err != nil && !(errors.As(err, &apiError) && apiError.ErrorCode() == "NoSuchEntity") {
			return fmt.Errorf("deleting shared OIDC provider: %w", err)
		}
	}
	var failures []error
	for _, document := range sharedOIDCDocuments() {
		if _, err := s3Client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(state.Options.Bucket), Key: aws.String(state.ProviderID + document.path)}); err != nil {
			var apiError smithy.APIError
			if !(errors.As(err, &apiError) && apiError.ErrorCode() == "NoSuchBucket") {
				failures = append(failures, fmt.Errorf("deleting shared OIDC document %s: %w", document.path, err))
			}
		}
	}
	if err := errors.Join(failures...); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(sharedDir, state.ProviderID+".pem")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Remove(filepath.Join(sharedDir, SharedOIDCStateFileName))
}

func CleanupSharedOIDCIfUnused(ctx context.Context, sharedDir string, client crclient.Client) error {
	return cleanupSharedOIDCIfUnused(ctx, sharedDir, client, CleanupSharedOIDC)
}

func cleanupSharedOIDCIfUnused(ctx context.Context, sharedDir string, client crclient.Client, cleanup func(context.Context, string) error) error {
	if _, err := os.Stat(filepath.Join(sharedDir, SharedOIDCStateFileName)); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	manifest, err := ReadManifest(sharedDir)
	if err != nil {
		return err
	}
	for _, cluster := range manifest.Clusters {
		err := client.Get(ctx, crclient.ObjectKey{Namespace: cluster.Namespace, Name: cluster.Name}, &hyperv1.HostedCluster{})
		if err == nil {
			return nil
		}
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("checking shared OIDC consumer %s/%s: %w", cluster.Namespace, cluster.Name, err)
		}
	}
	return cleanup(ctx, sharedDir)
}
