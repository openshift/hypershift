package util

import (
	"errors"
	"fmt"
	"net"
	"time"

	"k8s.io/utils/ptr"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/client"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/openshift/hypershift/cmd/util"
	"github.com/spf13/pflag"
)

type AWSCredentialsOptions struct {
	AWSCredentialsFile string

	RoleArn            string
	STSCredentialsFile string
}

func (opts *AWSCredentialsOptions) Validate() error {
	if opts.AWSCredentialsFile != "" {
		if opts.STSCredentialsFile != "" || opts.RoleArn != "" {
			return fmt.Errorf("only one of 'aws-creds' or 'role-arn' and 'sts-creds' can be provided")
		}

		return nil
	}

	if err := util.ValidateRequiredOption("sts-creds", opts.STSCredentialsFile); err != nil {
		return err
	}
	if err := util.ValidateRequiredOption("role-arn", opts.RoleArn); err != nil {
		return err
	}
	return nil
}

func (opts *AWSCredentialsOptions) BindFlags(flags *pflag.FlagSet) {
	opts.BindProductFlags(flags)

	flags.StringVar(&opts.AWSCredentialsFile, "aws-creds", opts.AWSCredentialsFile, "Path to an AWS credentials file")
	flags.MarkDeprecated("aws-creds", "please use '--sts-creds' with '--role-arn' instead")
}

func (opts *AWSCredentialsOptions) BindVPCOwnerFlags(flags *pflag.FlagSet) {
	flags.StringVar(&opts.AWSCredentialsFile, "vpc-owner-aws-creds", opts.AWSCredentialsFile, "Path to VPC owner AWS credentials file")
}

func (opts *AWSCredentialsOptions) BindProductFlags(flags *pflag.FlagSet) {
	flags.StringVar(&opts.RoleArn, "role-arn", opts.RoleArn, "The ARN of the role to assume.")
	flags.StringVar(&opts.STSCredentialsFile, "sts-creds", opts.STSCredentialsFile, "Path to the STS credentials file to use when assuming the role. Can be generated with 'aws sts get-session-token --output json'")
}

func (opts *AWSCredentialsOptions) GetSession(agent string, secretData *util.CredentialsSecretData, region string) (*session.Session, error) {
	if opts.AWSCredentialsFile != "" {
		return NewSession(agent, opts.AWSCredentialsFile, "", "", region), nil
	}

	if opts.STSCredentialsFile != "" {
		creds, err := ParseSTSCredentialsFile(opts.STSCredentialsFile)
		if err != nil {
			return nil, err
		}

		return NewSTSSession(agent, opts.RoleArn, region, creds)
	}

	if secretData != nil {
		creds := credentials.NewStaticCredentials(
			secretData.AWSAccessKeyID,
			secretData.AWSSecretAccessKey,
			secretData.AWSSessionToken,
		)
		return NewSTSSession(agent, opts.RoleArn, region, creds)
	}

	return nil, errors.New("could not create AWS session, no credentials were given")
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

func ValidateVPCCIDR(in string) error {
	if in == "" {
		return nil
	}
	_, network, err := net.ParseCIDR(in)
	if err != nil {
		return fmt.Errorf("invalid CIDR (%s): %w", in, err)
	}
	if ones, _ := network.Mask.Size(); ones != 16 {
		return fmt.Errorf("only /16 size VPC CIDR supported (%s)", in)
	}
	return nil
}
