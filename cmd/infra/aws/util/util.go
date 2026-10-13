package util

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/openshift/hypershift/cmd/util"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/smithy-go/middleware"

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

func (opts *AWSCredentialsOptions) GetSession(ctx context.Context, agent string, secretData *util.CredentialsSecretData, region string) (*aws.Config, error) {
	if opts.AWSCredentialsFile != "" {
		return NewSession(ctx, agent, opts.AWSCredentialsFile, "", "", region), nil
	}

	if opts.STSCredentialsFile != "" {
		creds, err := ParseSTSCredentialsFile(opts.STSCredentialsFile)
		if err != nil {
			return nil, err
		}

		return NewSTSSession(ctx, agent, opts.RoleArn, region, creds)
	}

	// Credentials from secret data
	if secretData != nil {
		v2Creds := aws.Credentials{
			AccessKeyID:     secretData.AWSAccessKeyID,
			SecretAccessKey: secretData.AWSSecretAccessKey,
			SessionToken:    secretData.AWSSessionToken,
		}

		return NewSTSSession(ctx, agent, opts.RoleArn, region, &v2Creds)
	}

	return nil, errors.New("could not create AWS session, no credentials were given")
}

func NewSession(ctx context.Context, agent, credentialsFile, credKey, credSecretKey, region string) *aws.Config {
	var configOpts []func(*config.LoadOptions) error
	// If no credentials file is explicitly provided, fall back to AWS_SHARED_CREDENTIALS_FILE.
	// This matches the v1 SDK behavior when AWS_SDK_LOAD_CONFIG=1 is set: the env-var file is
	// treated as a shared config file (not just a credentials file), so settings such as
	// role_arn and web_identity_token_file are correctly processed.
	if credentialsFile == "" {
		credentialsFile = os.Getenv("AWS_SHARED_CREDENTIALS_FILE")
	}
	if credentialsFile != "" {
		configOpts = append(configOpts, config.WithSharedConfigFiles([]string{credentialsFile}))
		configOpts = append(configOpts, config.WithSharedCredentialsFiles([]string{credentialsFile}))
	}
	if credKey != "" && credSecretKey != "" {
		credsProvider := credentials.NewStaticCredentialsProvider(credKey, credSecretKey, "")
		configOpts = append(configOpts, config.WithCredentialsProvider(credsProvider))
	}
	if region != "" {
		configOpts = append(configOpts, config.WithRegion(region))
	}
	configOpts = append(configOpts, config.WithAPIOptions([]func(*middleware.Stack) error{
		awsmiddleware.AddUserAgentKeyValue("openshift.io hypershift", agent),
	}))

	cfg, _ := config.LoadDefaultConfig(ctx, configOpts...)
	return &cfg
}

// NewRoute53Config creates a retryer with more conservative retry timing for Route53.
func NewRoute53Config() func() aws.Retryer {
	return func() aws.Retryer {
		return retry.NewStandard(func(o *retry.StandardOptions) {
			o.MaxAttempts = 21                                              // 1 initial + 20 retries
			o.Backoff = retry.NewExponentialJitterBackoff(30 * time.Second) // Higher cap for Route53 throttling
		})
	}
}

// NewConfig creates a retryer function with standard retry configuration.
func NewConfig() func() aws.Retryer {
	return func() aws.Retryer {
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
