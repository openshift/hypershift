package credentialprovider

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ecr"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubelet/pkg/apis/credentialprovider/v1alpha1"
)

var ecrPattern = regexp.MustCompile(`^(\d{12})\.dkr\.ecr(\-fips)?\.([a-zA-Z0-9][a-zA-Z0-9-_]*)\.(amazonaws\.com(\.cn)?|sc2s\.sgov\.gov|c2s\.ic\.gov)$`)

type ECRRepo struct {
	RegistryID string
	Region     string
	Registry   string
}

type ECRDockerCredentialProvider interface {
	GetECRCredentials(ctx context.Context, ecrRepo *ECRRepo) (*v1alpha1.CredentialProviderResponse, error)
	ParseECRRepoURL(image string) (*ECRRepo, error)
}

type ecrDockerCredentialProviderImpl struct{}

func NewECRDockerCredentialProvider() ECRDockerCredentialProvider {
	return &ecrDockerCredentialProviderImpl{}
}

func (e *ecrDockerCredentialProviderImpl) GetECRCredentials(ctx context.Context, ecrRepo *ECRRepo) (*v1alpha1.CredentialProviderResponse, error) {
	registryID, region, registry := ecrRepo.RegistryID, ecrRepo.Region, ecrRepo.Registry

	sess, err := session.NewSessionWithOptions(session.Options{
		Config:            aws.Config{Region: aws.String(region)},
		SharedConfigState: session.SharedConfigEnable,
	})
	ecrProvider := ecr.New(sess)

	if err != nil {
		return nil, err
	}

	output, err := ecrProvider.GetAuthorizationToken(&ecr.GetAuthorizationTokenInput{
		RegistryIds: []*string{aws.String(registryID)},
	})
	if err != nil {
		return nil, err
	}

	if output == nil {
		return nil, errors.New("response output from ECR was nil")
	}

	if len(output.AuthorizationData) == 0 {
		return nil, errors.New("authorization data was empty")
	}

	data := output.AuthorizationData[0]
	if data.AuthorizationToken == nil {
		return nil, errors.New("authorization token in response was nil")
	}

	decodedToken, err := base64.StdEncoding.DecodeString(aws.StringValue(data.AuthorizationToken))
	if err != nil {
		return nil, err
	}

	parts := strings.SplitN(string(decodedToken), ":", 2)
	if len(parts) != 2 {
		return nil, errors.New("error parsing username and password from authorization token")
	}

	var cacheDuration *metav1.Duration
	expiresAt := data.ExpiresAt
	if expiresAt == nil {
		// explicitly set cache duration to 0 if expiresAt was nil so that
		// kubelet does not cache it in-memory
		cacheDuration = &metav1.Duration{Duration: 0}
	} else {
		// halving duration in order to compensate for the time loss between
		// the token creation and passing it all the way to kubelet.
		duration := time.Duration((expiresAt.Unix() - time.Now().Unix()) / 2)
		if duration > 0 {
			cacheDuration = &metav1.Duration{Duration: duration}
		}
	}

	return &v1alpha1.CredentialProviderResponse{
		CacheKeyType:  v1alpha1.RegistryPluginCacheKeyType,
		CacheDuration: cacheDuration,
		Auth: map[string]v1alpha1.AuthConfig{
			registry: {
				Username: parts[0],
				Password: parts[1],
			},
		},
	}, nil

}

// parseRepoURL parses and splits the registry URL
// returns (registryID, region, registry).
// <registryID>.dkr.ecr(-fips).<region>.amazonaws.com(.cn)
func (e *ecrDockerCredentialProviderImpl) ParseECRRepoURL(image string) (*ECRRepo, error) {
	if !strings.Contains(image, "https://") {
		image = "https://" + image
	}
	parsed, err := url.Parse(image)
	if err != nil {
		return nil, fmt.Errorf("error parsing image %s: %v", image, err)
	}

	splitURL := ecrPattern.FindStringSubmatch(parsed.Hostname())
	if len(splitURL) < 4 {
		return nil, fmt.Errorf("%s is not a valid ECR repository URL", parsed.Hostname())
	}

	return &ECRRepo{
		RegistryID: splitURL[1],
		Region:     splitURL[2],
		Registry:   parsed.Hostname(),
	}, nil
}
