package awsutil

import (
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/sts"
)

func AssumeRoleWithWebIdentity(sess *session.Session, roleSessionName, roleArn, token string) (*credentials.Credentials, error) {
	svc := sts.New(sess)
	input := &sts.AssumeRoleWithWebIdentityInput{
		RoleArn:          aws.String(roleArn),
		WebIdentityToken: aws.String(token),
		RoleSessionName:  aws.String(roleSessionName),
	}

	resp, err := svc.AssumeRoleWithWebIdentity(input)
	if err != nil {
		return nil, err
	}
	if resp.Credentials == nil {
		return nil, credentials.ErrStaticCredentialsEmpty

	}

	return credentials.NewStaticCredentials(
		*resp.Credentials.AccessKeyId,
		*resp.Credentials.SecretAccessKey,
		*resp.Credentials.SessionToken,
	), nil
}
