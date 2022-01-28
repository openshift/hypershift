package util

import (
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/client"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/aws/session"
)

func NewSession(agent string) *session.Session {
	awsSession := session.Must(session.NewSession())
	awsSession.Handlers.Build.PushBackNamed(request.NamedHandler{
		Name: "openshift.io/hypershift",
		Fn:   request.MakeAddToUserAgentHandler("openshift.io hypershift", agent),
	})
	return awsSession
}

// NewAWSRoute53Config generates an AWS config with slightly different Retryer timings
func NewAWSRoute53Config(credentialsFile string, credKey string, credSecretKey string) *aws.Config {

	awsRoute53Config := NewConfig(credentialsFile, credKey, credSecretKey, "us-east-1")
	awsRoute53Config.Retryer = client.DefaultRetryer{
		NumMaxRetries:    10,
		MinRetryDelay:    5 * time.Second,
		MinThrottleDelay: 10 * time.Second,
	}
	return awsRoute53Config
}

// NewConfig allows support for a CredentialsFile or StaticCredential (UserKey & UserSecretKey)
// [Default] if all values are provided is to use credentialsFile
// This allows methods to be used by the CLI or vendored for other Go code
func NewConfig(credentialsFile string, credKey string, credSecretKey string, region string) *aws.Config {

	// Will be empty when using credentialsFile
	creds := credentials.NewStaticCredentials(credKey, credSecretKey, "")
	if credentialsFile != "" {
		creds = credentials.NewSharedCredentials(credentialsFile, "default")
	}
	awsConfig := aws.NewConfig().
		WithRegion(region).
		WithCredentials(creds)
	awsConfig.Retryer = client.DefaultRetryer{
		NumMaxRetries:    10,
		MinRetryDelay:    5 * time.Second,
		MinThrottleDelay: 5 * time.Second,
	}
	return awsConfig
}
