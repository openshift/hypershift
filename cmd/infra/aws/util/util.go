package util

import (
	"context"
	"time"

	awsv2 "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/client"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/aws/session"
	smiddleware "github.com/aws/smithy-go/middleware"
)

func NewSession(agent string) *session.Session {
	awsSession := session.Must(session.NewSession())
	awsSession.Handlers.Build.PushBackNamed(request.NamedHandler{
		Name: "openshift.io/hypershift",
		Fn:   request.MakeAddToUserAgentHandler("openshift.io hypershift", agent),
	})
	return awsSession
}

func NewV2Config(agent string) (awsv2.Config, error) {
	return config.LoadDefaultConfig(context.Background(), config.WithAPIOptions([]func(*smiddleware.Stack) error{middleware.AddUserAgentKeyValue("openshift.io/hypershift", agent)}))
}

func NewConfig(credentialsFile, region string) *aws.Config {
	awsConfig := aws.NewConfig().
		WithRegion(region).
		WithCredentials(credentials.NewSharedCredentials(credentialsFile, "default"))
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
