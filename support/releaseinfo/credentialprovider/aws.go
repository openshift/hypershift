package credentialprovider

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ecr"
)

var ecrPattern = regexp.MustCompile(`^(\d{12})\.dkr\.ecr(\-fips)?\.([a-zA-Z0-9][a-zA-Z0-9-_]*)\.(amazonaws\.com(\.cn)?|sc2s\.sgov\.gov|c2s\.ic\.gov)$`)

type ECRRepo struct {
	RegistryID string
	Region     string
	Registry   string
}

type ECRDockerCredentialProvider interface {
	GetECRCredentials(ctx context.Context, ecrRepo *ECRRepo) (string, error)
	ParseECRRepoURL(image string) (*ECRRepo, error)
}

type ecrDockerCredentialProviderImpl struct{}

func NewECRDockerCredentialProvider() ECRDockerCredentialProvider {
	return &ecrDockerCredentialProviderImpl{}
}

func (e *ecrDockerCredentialProviderImpl) GetECRCredentials(ctx context.Context, ecrRepo *ECRRepo) (string, error) {
	registryID, region := ecrRepo.RegistryID, ecrRepo.Region

	sess, err := session.NewSessionWithOptions(session.Options{
		Config:            aws.Config{Region: aws.String(region)},
		SharedConfigState: session.SharedConfigEnable,
	})
	ecrProvider := ecr.New(sess)

	if err != nil {
		return "", err
	}

	output, err := ecrProvider.GetAuthorizationToken(&ecr.GetAuthorizationTokenInput{
		RegistryIds: []*string{aws.String(registryID)},
	})
	if err != nil {
		return "", err
	}

	if output == nil {
		return "", errors.New("response output from ECR was nil")
	}

	if len(output.AuthorizationData) == 0 {
		return "", errors.New("authorization data was empty")
	}

	data := output.AuthorizationData[0]
	if data.AuthorizationToken == nil {
		return "", errors.New("authorization token in response was nil")
	}

	return aws.StringValue(data.AuthorizationToken), nil

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
