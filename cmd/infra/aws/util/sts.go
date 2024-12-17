package util

import (
	"encoding/json"
	"fmt"
	"os"

	supportawsutil "github.com/openshift/hypershift/support/awsutil"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/aws/session"

	"k8s.io/utils/ptr"
)

func NewSTSSession(agent, rolenArn, region string, assumeRoleCreds *credentials.Credentials) (*session.Session, error) {
	assumeRoleSession := session.Must(session.NewSession(aws.NewConfig().WithCredentials(assumeRoleCreds)))
	creds, err := supportawsutil.AssumeRole(assumeRoleSession, agent, rolenArn)
	if err != nil {
		return nil, err
	}

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

type Credentials struct {
	AccessKeyId     string `json:"AccessKeyId"`
	SecretAccessKey string `json:"SecretAccessKey"`
	SessionToken    string `json:"SessionToken"`
	Expiration      string `json:"Expiration"`
}
