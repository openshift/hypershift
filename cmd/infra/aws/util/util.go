package util

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/openshift/hypershift/cmd/util"

	// AWS SDK v2
	awsv2 "github.com/aws/aws-sdk-go-v2/aws"
	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	configv2 "github.com/aws/aws-sdk-go-v2/config"
	credentialsv2 "github.com/aws/aws-sdk-go-v2/credentials"
	// AWS SDK v1
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/client"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/smithy-go/middleware"

	"k8s.io/utils/ptr"

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
	_ = flags.MarkDeprecated("aws-creds", "please use '--sts-creds' with '--role-arn' instead")
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

func (opts *AWSCredentialsOptions) GetSessionV2(ctx context.Context, agent string, secretData *util.CredentialsSecretData, region string) (*awsv2.Config, error) {
	if opts.AWSCredentialsFile != "" {
		return NewSessionV2(ctx, agent, opts.AWSCredentialsFile, "", "", region), nil
	}

	if opts.STSCredentialsFile != "" {
		creds, err := ParseSTSCredentialsFileV2(opts.STSCredentialsFile)
		if err != nil {
			return nil, err
		}

		return NewSTSSessionV2(ctx, agent, opts.RoleArn, region, creds)
	}

	// Credentials from secret data
	if secretData != nil {
		v2Creds := awsv2.Credentials{
			AccessKeyID:     secretData.AWSAccessKeyID,
			SecretAccessKey: secretData.AWSSecretAccessKey,
			SessionToken:    secretData.AWSSessionToken,
		}

		return NewSTSSessionV2(ctx, agent, opts.RoleArn, region, &v2Creds)
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

func NewSessionV2(ctx context.Context, agent, credentialsFile, credKey, credSecretKey, region string) *awsv2.Config {
	var configOpts []func(*configv2.LoadOptions) error
	if credentialsFile != "" {
		configOpts = append(configOpts, configv2.WithSharedConfigFiles([]string{credentialsFile}))
	}
	if credKey != "" && credSecretKey != "" {
		credsProvider := credentialsv2.NewStaticCredentialsProvider(credKey, credSecretKey, "")
		configOpts = append(configOpts, configv2.WithCredentialsProvider(credsProvider))
	}
	if region != "" {
		configOpts = append(configOpts, configv2.WithRegion(region))
	}
	configOpts = append(configOpts, configv2.WithAPIOptions([]func(*middleware.Stack) error{
		awsmiddleware.AddUserAgentKeyValue("openshift.io hypershift", agent),
	}))

	cfg, _ := configv2.LoadDefaultConfig(ctx, configOpts...)
	return &cfg
}

// NewRoute53ConfigV2 creates a v2 retryer with more conservative retry timing for Route53.
func NewRoute53ConfigV2() func() awsv2.Retryer {
	return func() awsv2.Retryer {
		return retry.NewStandard(func(o *retry.StandardOptions) {
			o.MaxAttempts = 21                                              // 1 initial + 20 retries
			o.Backoff = retry.NewExponentialJitterBackoff(30 * time.Second) // Higher cap for Route53 throttling
		})
	}
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

// NewConfigV2 creates a v2 retryer function with the same retry configuration as NewConfig
func NewConfigV2() func() awsv2.Retryer {
	return func() awsv2.Retryer {
		return retry.NewStandard(func(o *retry.StandardOptions) {
			o.MaxAttempts = 11                                             // 1 initial + 10 retries (match v1's NumMaxRetries: 10)
			o.Backoff = retry.NewExponentialJitterBackoff(5 * time.Second) // Initial delay 5s (match v1's MinRetryDelay)
		})
	}
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
