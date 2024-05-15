package util

import (
	"encoding/json"
	"fmt"
	"github.com/aws/aws-sdk-go/service/sts"
	"os"
	"time"

	utilpointer "k8s.io/utils/pointer"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/client"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/aws/session"
)

func NewSession(agent, credentialsFile, credKey, credSecretKey, region string) *session.Session {
	sessionOpts := session.Options{}
	if credentialsFile != "" {
		sessionOpts.SharedConfigFiles = append(sessionOpts.SharedConfigFiles, credentialsFile)
	}
	if credKey != "" && credSecretKey != "" {
		sessionOpts.Config.Credentials = credentials.NewStaticCredentials(credKey, credSecretKey, "")
	}
	if region != "" {
		sessionOpts.Config.Region = utilpointer.String(region)
	}
	awsSession := session.Must(session.NewSessionWithOptions(sessionOpts))
	awsSession.Handlers.Build.PushBackNamed(request.NamedHandler{
		Name: "openshift.io/hypershift",
		Fn:   request.MakeAddToUserAgentHandler("openshift.io hypershift", agent),
	})
	return awsSession
}

func NewStsSession(agent, stsCredentialsFile, credKey, credSecretKey, sessionToken, roleArn, region string) (*session.Session, error) {
	stsSessionOpts := session.Options{}

	if credKey != "" && credSecretKey != "" && sessionToken != "" {
		stsSessionOpts.Config.Credentials = credentials.NewStaticCredentials(credKey, credSecretKey, sessionToken)
	}

	var stsCreds struct {
		Credentials Credentials `json:"Credentials"`
	}

	if stsCredentialsFile != "" {
		rawStsCreds, err := os.ReadFile(stsCredentialsFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read sts credentials file: %w", err)
		}
		err = json.Unmarshal(rawStsCreds, &stsCreds)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal sts credentials: %w", err)
		}
		stsSessionOpts.Config.Credentials = credentials.NewStaticCredentials(stsCreds.Credentials.AccessKeyId, stsCreds.Credentials.SecretAccessKey, stsCreds.Credentials.SessionToken)
	}

	mySession := session.Must(session.NewSessionWithOptions(stsSessionOpts))
	stsClient := sts.New(mySession)

	role, err := stsClient.AssumeRole(&sts.AssumeRoleInput{
		RoleArn:         aws.String(roleArn),
		RoleSessionName: aws.String(agent),
	})

	if err != nil {
		return nil, err
	}

	creds := credentials.NewStaticCredentials(
		*role.Credentials.AccessKeyId,
		*role.Credentials.SecretAccessKey,
		*role.Credentials.SessionToken,
	)

	awsSessionOpts := session.Options{
		Config: aws.Config{
			Credentials: creds,
		},
	}

	if region != "" {
		awsSessionOpts.Config.Region = utilpointer.String(region)
	}

	awsSession := session.Must(session.NewSessionWithOptions(awsSessionOpts))
	awsSession.Handlers.Build.PushBackNamed(request.NamedHandler{
		Name: "openshift.io/hypershift",
		Fn:   request.MakeAddToUserAgentHandler("openshift.io hypershift", agent),
	})
	return awsSession, nil
}

// NewAWSRoute53Config generates an AWS config with slightly different Retryer timings
func NewAWSRoute53Config() *aws.Config {
	awsRoute53Config := NewConfig()
	awsRoute53Config.Retryer = client.DefaultRetryer{
		NumMaxRetries:    10,
		MinRetryDelay:    5 * time.Second,
		MinThrottleDelay: 10 * time.Second,
	}
	return awsRoute53Config
}

// NewConfig creates a new config.
func NewConfig() *aws.Config {

	awsConfig := aws.NewConfig()
	awsConfig.Retryer = client.DefaultRetryer{
		NumMaxRetries:    10,
		MinRetryDelay:    5 * time.Second,
		MinThrottleDelay: 5 * time.Second,
	}
	return awsConfig
}

type Credentials struct {
	AccessKeyId     string `json:"AccessKeyId"`
	SecretAccessKey string `json:"SecretAccessKey"`
	SessionToken    string `json:"SessionToken"`
	Expiration      string `json:"Expiration"`
}
