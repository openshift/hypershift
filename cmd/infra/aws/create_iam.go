package aws

import (
	"fmt"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/iam"
	"github.com/aws/aws-sdk-go/service/iam/iamiface"
	"github.com/spf13/cobra"
)

type CreateIAMOptions struct {
	Region             string
	AWSCredentialsFile string
	ProfileName        string
}

func NewCreateIAMCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "aws",
		Short: "Creates AWS instance profile for workers",
	}

	opts := CreateIAMOptions{
		Region:      "us-east-1",
		ProfileName: "hypershift-worker-profile",
	}

	cmd.Flags().StringVar(&opts.AWSCredentialsFile, "aws-creds", opts.AWSCredentialsFile, "Path to an AWS credentials file (required)")
	cmd.Flags().StringVar(&opts.ProfileName, "profile-name", opts.ProfileName, "Name of IAM instance profile to creeate")
	cmd.Flags().StringVar(&opts.Region, "region", opts.Region, "Region where cluster infra should be created")

	cmd.MarkFlagRequired("aws-creds")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return opts.Run()
	}

	return cmd
}

func (o *CreateIAMOptions) Run() error {
	if err := o.CreateIAM(); err != nil {
		return err
	}
	return nil
}

func (o *CreateIAMOptions) CreateIAM() error {
	var err error
	client, err := IAMClient(o.AWSCredentialsFile, o.Region)
	if err != nil {
		return err
	}
	return o.CreateWorkerInstanceProfile(client, o.ProfileName)
}

func IAMClient(creds, region string) (iamiface.IAMAPI, error) {
	awsConfig := &aws.Config{
		Region: aws.String(region),
	}
	awsConfig.Credentials = credentials.NewSharedCredentials(creds, "default")
	s, err := session.NewSession(awsConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create client session: %w", err)
	}
	return iam.New(s), nil
}
