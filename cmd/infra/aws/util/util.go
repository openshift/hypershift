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

func NewConfig(credentialsFile, region string) *aws.Config {
	return newConfig(credentials.NewSharedCredentials(credentialsFile, "default"), region)
}

func newConfig(creds *credentials.Credentials, region string) *aws.Config {
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

func NewRoute53Config(credentialsFile string) *aws.Config {
	awsConfig := NewConfig(credentialsFile, "us-east-1")
	awsConfig.Retryer = client.DefaultRetryer{
		NumMaxRetries:    10,
		MinRetryDelay:    5 * time.Second,
		MinThrottleDelay: 10 * time.Second,
	}
	return awsConfig
}

func NewAWSConfig(credentialsFile string, credKey string, credSecretKey string, region string) *aws.Config {

	if credentialsFile == "" {
		creds := credentials.NewStaticCredentials(credKey, credSecretKey, "")
		return newConfig(creds, region)
	}
	return NewConfig(credentialsFile, region)
}
