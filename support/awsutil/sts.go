package awsutil

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

var (
	// ErrEmptyCredentials indicates that the STS response did not contain credentials.
	ErrEmptyCredentials = errors.New("STS response contains empty credentials")
)

func AssumeRoleWithWebIdentity(ctx context.Context, cfg *aws.Config, roleSessionName, roleArn, token string) (*aws.Credentials, error) {
	client := sts.NewFromConfig(*cfg)
	resp, err := client.AssumeRoleWithWebIdentity(ctx, &sts.AssumeRoleWithWebIdentityInput{
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

	creds, err := credentials.NewStaticCredentialsProvider(
		aws.ToString(resp.Credentials.AccessKeyId),
		aws.ToString(resp.Credentials.SecretAccessKey),
		aws.ToString(resp.Credentials.SessionToken),
	).Retrieve(ctx)
	return &creds, err
}

func AssumeRole(ctx context.Context, cfg aws.Config, roleSessionName, roleArn string) (*aws.Credentials, error) {
	client := sts.NewFromConfig(cfg)
	resp, err := client.AssumeRole(ctx, &sts.AssumeRoleInput{
		RoleArn:         aws.String(roleArn),
		RoleSessionName: aws.String(roleSessionName),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to assume role: %w", err)
	}
	if resp.Credentials == nil {
		return nil, ErrEmptyCredentials
	}

	creds, err := credentials.NewStaticCredentialsProvider(
		aws.ToString(resp.Credentials.AccessKeyId),
		aws.ToString(resp.Credentials.SecretAccessKey),
		aws.ToString(resp.Credentials.SessionToken),
	).Retrieve(ctx)
	return &creds, err
}
