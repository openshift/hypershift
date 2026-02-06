package awsutil

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	credentialsv2 "github.com/aws/aws-sdk-go-v2/credentials"
	stsv2 "github.com/aws/aws-sdk-go-v2/service/sts"
)

var (
	// ErrEmptyCredentials indicates that the STS response did not contain credentials.
	ErrEmptyCredentials = errors.New("STS response contains empty credentials")
)

func AssumeRoleWithWebIdentity(ctx context.Context, cfg *aws.Config, roleSessionName, roleArn, token string) (*aws.Credentials, error) {
	client := stsv2.NewFromConfig(*cfg)
	resp, err := client.AssumeRoleWithWebIdentity(ctx, &stsv2.AssumeRoleWithWebIdentityInput{
		RoleArn:          aws.String(roleArn),
		RoleSessionName:  aws.String(roleSessionName),
		WebIdentityToken: aws.String(token),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to assume role with web identity: %w", err)
	}
	if resp.Credentials == nil {
		return nil, ErrEmptyCredentials
	}

	credentials, err := credentialsv2.NewStaticCredentialsProvider(
		aws.ToString(resp.Credentials.AccessKeyId),
		aws.ToString(resp.Credentials.SecretAccessKey),
		aws.ToString(resp.Credentials.SessionToken),
	).Retrieve(ctx)
	return &credentials, err
}

func AssumeRole(ctx context.Context, cfg aws.Config, roleSessionName, roleArn string) (*aws.Credentials, error) {
	client := stsv2.NewFromConfig(cfg)
	resp, err := client.AssumeRole(ctx, &stsv2.AssumeRoleInput{
		RoleArn:         aws.String(roleArn),
		RoleSessionName: aws.String(roleSessionName),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to assume role: %w", err)
	}
	if resp.Credentials == nil {
		return nil, ErrEmptyCredentials
	}

	credentials, err := credentialsv2.NewStaticCredentialsProvider(
		aws.ToString(resp.Credentials.AccessKeyId),
		aws.ToString(resp.Credentials.SecretAccessKey),
		aws.ToString(resp.Credentials.SessionToken),
	).Retrieve(ctx)
	return &credentials, err
}
