package util

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	supportawsutil "github.com/openshift/hypershift/support/awsutil"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/smithy-go/middleware"
)

func NewSTSSession(ctx context.Context, agent, roleArn, region string, assumeRoleCreds *aws.Credentials) (*aws.Config, error) {
	if assumeRoleCreds == nil {
		return nil, fmt.Errorf("assumeRoleCreds cannot be nil")
	}
	if roleArn == "" {
		return nil, fmt.Errorf("roleArn cannot be empty")
	}
	if agent == "" {
		return nil, fmt.Errorf("agent cannot be empty")
	}

	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithCredentialsProvider(credentials.StaticCredentialsProvider{
			Value: *assumeRoleCreds,
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	assumedCreds, err := supportawsutil.AssumeRole(ctx, cfg, agent, roleArn)
	if err != nil {
		return nil, err
	}

	var loadOptions []func(*config.LoadOptions) error
	loadOptions = append(loadOptions, config.WithCredentialsProvider(
		credentials.StaticCredentialsProvider{
			Value: *assumedCreds,
		},
	))
	if region != "" {
		loadOptions = append(loadOptions, config.WithRegion(region))
	}
	loadOptions = append(loadOptions, config.WithAPIOptions([]func(*middleware.Stack) error{
		awsmiddleware.AddUserAgentKeyValue("openshift.io hypershift", agent),
	}))
	finalCfg, err := config.LoadDefaultConfig(ctx, loadOptions...)
	if err != nil {
		return nil, fmt.Errorf("failed to load final config: %w", err)
	}

	return &finalCfg, nil
}

type STSCreds struct {
	Credentials Credentials `json:"Credentials"`
}

// ParseSTSCredentialsFile parses an STS credentials file and returns credentials.
func ParseSTSCredentialsFile(credentialsFile string) (*aws.Credentials, error) {
	var stsCreds STSCreds

	rawSTSCreds, err := os.ReadFile(credentialsFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read sts credentials file: %w", err)
	}
	err = json.Unmarshal(rawSTSCreds, &stsCreds)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal sts credentials: %w", err)
	}

	creds := aws.Credentials{
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
