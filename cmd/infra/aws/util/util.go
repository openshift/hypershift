package util

import (
	"fmt"
	"time"

	"k8s.io/utils/ptr"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/client"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/openshift/hypershift/cmd/util"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type AWSCredentialsOptions struct {
	AWSCredentialsFile string

	RoleArn            string
	StsCredentialsFile string
}

func (opts *AWSCredentialsOptions) Validate() error {
	if opts.AWSCredentialsFile != "" {
		if opts.StsCredentialsFile != "" || opts.RoleArn != "" {
			return fmt.Errorf("only one of 'aws-creds' or 'role-arn' and 'sts-creds' can be provided")
		}

		return nil
	}

	if err := util.ValidateRequiredOption("sts-creds", opts.StsCredentialsFile); err != nil {
		return err
	}
	if err := util.ValidateRequiredOption("role-arn", opts.RoleArn); err != nil {
		return err
	}
	return nil
}

func (opts *AWSCredentialsOptions) BindFlags(flags *pflag.FlagSet) {
	flags.StringVar(&opts.AWSCredentialsFile, "aws-creds", opts.AWSCredentialsFile, "Path to an AWS credentials file")
	flags.StringVar(&opts.RoleArn, "role-arn", opts.RoleArn, "The ARN of the role to assume.")
	flags.StringVar(&opts.StsCredentialsFile, "sts-creds", opts.StsCredentialsFile, "Path to STS credentials file to use when assuming the role")

	flags.MarkDeprecated("aws-creds", "please use '--sts-creds; with' --role-arn' instead")
}

func (opts *AWSCredentialsOptions) BindProductFlags(flags *pflag.FlagSet) {
	flags.StringVar(&opts.RoleArn, "role-arn", opts.RoleArn, "The ARN of the role to assume. (Required)")
	flags.StringVar(&opts.StsCredentialsFile, "sts-creds", opts.StsCredentialsFile, "STS credentials file to use when assuming the role. (Required)")

	cobra.MarkFlagRequired(flags, "role-arn")
	cobra.MarkFlagRequired(flags, "sts-creds")
}

func (opts *AWSCredentialsOptions) GetSession(agent string, secretData *util.CredentialsSecretData, region string) (*session.Session, error) {
	if opts.AWSCredentialsFile != "" {
		return NewSession(agent, opts.AWSCredentialsFile, "", "", region), nil
	}

	if opts.StsCredentialsFile != "" {
		creds, err := ParseSTSCredentialsFile(opts.StsCredentialsFile)
		if err != nil {
			return nil, err
		}

		return NewStsSession(agent, opts.RoleArn, region, creds)
	}

	if secretData != nil {
		creds := credentials.NewStaticCredentials(
			secretData.AWSAccessKeyID,
			secretData.AWSSecretAccessKey,
			secretData.AWSSessionToken,
		)
		return NewStsSession(agent, opts.RoleArn, region, creds)
	}

	return nil, fmt.Errorf("either --aws-creds or --sts-creds or --secret-creds flag must be set")
}

func NewSession(agent, credentialsFile, credKey, credSecretKey, region string) *session.Session {
	sessionOpts := session.Options{}
	if credentialsFile != "" {
		sessionOpts.SharedConfigFiles = append(sessionOpts.SharedConfigFiles, credentialsFile)
	}
	if credKey != "" && credSecretKey != "" {
		sessionOpts.Config.Credentials = credentials.NewStaticCredentials(credKey, credSecretKey, "")
	}
	if region != "" {
		sessionOpts.Config.Region = ptr.To(region)
	}
	awsSession := session.Must(session.NewSessionWithOptions(sessionOpts))
	awsSession.Handlers.Build.PushBackNamed(request.NamedHandler{
		Name: "openshift.io/hypershift",
		Fn:   request.MakeAddToUserAgentHandler("openshift.io hypershift", agent),
	})
	return awsSession
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
