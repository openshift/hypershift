package util

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	supportawsutil "github.com/openshift/hypershift/support/awsutil"

	awsv2 "github.com/aws/aws-sdk-go-v2/aws"
	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	configv2 "github.com/aws/aws-sdk-go-v2/config"
	v2credentials "github.com/aws/aws-sdk-go-v2/credentials"
	// AWS SDK v1
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/aws/session"
	stsv1 "github.com/aws/aws-sdk-go/service/sts"
	"github.com/aws/smithy-go/middleware"

	"k8s.io/utils/ptr"
)

func NewSTSSession(agent, rolenArn, region string, assumeRoleCreds *credentials.Credentials) (*session.Session, error) {
	assumeRoleSession := session.Must(session.NewSession(aws.NewConfig().WithCredentials(assumeRoleCreds)))

	// Use v1 STS to assume role
	stsClient := stsv1.New(assumeRoleSession)
	assumeRoleOutput, err := stsClient.AssumeRole(&stsv1.AssumeRoleInput{
		RoleArn:         aws.String(rolenArn),
		RoleSessionName: aws.String(agent),
	})
	if err != nil {
		return nil, err
	}

	// Create credentials from assumed role
	creds := credentials.NewStaticCredentials(
		*assumeRoleOutput.Credentials.AccessKeyId,
		*assumeRoleOutput.Credentials.SecretAccessKey,
		*assumeRoleOutput.Credentials.SessionToken,
	)

	awsSessionOpts := session.Options{
		Config: aws.Config{
			Credentials: creds,
		},
	}

	if region != "" {
		awsSessionOpts.Config.Region = ptr.To(region)
	}

	awsSession := session.Must(session.NewSessionWithOptions(awsSessionOpts))
	awsSession.Handlers.Build.PushBackNamed(request.NamedHandler{
		Name: "openshift.io/hypershift",
		Fn:   request.MakeAddToUserAgentHandler("openshift.io hypershift", agent),
	})
	return awsSession, nil
}

func NewSTSSessionV2(ctx context.Context, agent, roleArn, region string, assumeRoleCreds *awsv2.Credentials) (*awsv2.Config, error) {
	if assumeRoleCreds == nil {
		return nil, fmt.Errorf("assumeRoleCreds cannot be nil")
	}
	if roleArn == "" {
		return nil, fmt.Errorf("roleArn cannot be empty")
	}
	if agent == "" {
		return nil, fmt.Errorf("agent cannot be empty")
	}

	cfg, err := configv2.LoadDefaultConfig(ctx,
		configv2.WithCredentialsProvider(v2credentials.StaticCredentialsProvider{
			Value: *assumeRoleCreds,
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load v2 config: %w", err)
	}

	assumedCreds, err := supportawsutil.AssumeRole(ctx, cfg, agent, roleArn)
	if err != nil {
		return nil, err
	}

	var loadOptions []func(*configv2.LoadOptions) error
	loadOptions = append(loadOptions, configv2.WithCredentialsProvider(
		v2credentials.StaticCredentialsProvider{
			Value: *assumedCreds,
		},
	))
	if region != "" {
		loadOptions = append(loadOptions, configv2.WithRegion(region))
	}
	loadOptions = append(loadOptions, configv2.WithAPIOptions([]func(*middleware.Stack) error{
		awsmiddleware.AddUserAgentKeyValue("openshift.io hypershift", agent),
	}))
	finalCfg, err := configv2.LoadDefaultConfig(ctx, loadOptions...)
	if err != nil {
		return nil, fmt.Errorf("failed to load final config: %w", err)
	}

	return &finalCfg, nil
}

type STSCreds struct {
	Credentials Credentials `json:"Credentials"`
}

func ParseSTSCredentialsFile(credentialsFile string) (*credentials.Credentials, error) {
	var stsCreds STSCreds

	rawSTSCreds, err := os.ReadFile(credentialsFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read sts credentials file: %w", err)
	}
	err = json.Unmarshal(rawSTSCreds, &stsCreds)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal sts credentials: %w", err)
	}

	creds := credentials.NewStaticCredentials(
		stsCreds.Credentials.AccessKeyId,
		stsCreds.Credentials.SecretAccessKey,
		stsCreds.Credentials.SessionToken,
	)

	return creds, nil
}

// ParseSTSCredentialsFileV2 parses STS credentials file and returns v2 credentials
func ParseSTSCredentialsFileV2(credentialsFile string) (*awsv2.Credentials, error) {
	var stsCreds STSCreds

	rawSTSCreds, err := os.ReadFile(credentialsFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read sts credentials file: %w", err)
	}
	err = json.Unmarshal(rawSTSCreds, &stsCreds)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal sts credentials: %w", err)
	}

	creds := awsv2.Credentials{
		AccessKeyID:     stsCreds.Credentials.AccessKeyId,
		SecretAccessKey: stsCreds.Credentials.SecretAccessKey,
		SessionToken:    stsCreds.Credentials.SessionToken,
	}
	return &creds, nil
}

type Credentials struct {
	AccessKeyId     string `json:"AccessKeyId"`
	SecretAccessKey string `json:"SecretAccessKey"`
	SessionToken    string `json:"SessionToken"`
	Expiration      string `json:"Expiration"`
}
