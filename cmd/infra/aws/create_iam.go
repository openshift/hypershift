package aws

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/iam"
	"github.com/aws/aws-sdk-go/service/iam/iamiface"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3iface"
	"github.com/spf13/cobra"
)

type CreateIAMOptions struct {
	Region             string
	AWSCredentialsFile string
	InfraID            string
	OutputFile         string
}

type CreateIAMOutput struct {
	Region                   string `json:"region"`
	InfraID                  string `json:"infraID"`
	IssuerURL                string `json:"issuerURL"`
	ServiceAccountSigningKey []byte `json:"serviceAccountSigningKey"`
	IngressRoleARN           string `json:"ingressRoleARN"`
	ImageRegistryRoleARN     string `json:"imageRegistryRoleARN"`
	AWSEBSCSIRoleARN         string `json:"awsEBSCSIRoleARN"`
}

func NewCreateIAMCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "aws",
		Short: "Creates AWS instance profile for workers",
	}

	opts := CreateIAMOptions{
		Region:             "us-east-1",
		AWSCredentialsFile: "",
		InfraID:            "",
	}

	cmd.Flags().StringVar(&opts.AWSCredentialsFile, "aws-creds", opts.AWSCredentialsFile, "Path to an AWS credentials file (required)")
	cmd.Flags().StringVar(&opts.InfraID, "infra-id", opts.InfraID, "Infrastructure ID to use for AWS resources.")
	cmd.Flags().StringVar(&opts.Region, "region", opts.Region, "Region where cluster infra should be created")
	cmd.Flags().StringVar(&opts.OutputFile, "output-file", opts.OutputFile, "Path to file that will contain output information from infra resources (optional)")

	cmd.MarkFlagRequired("aws-creds")
	cmd.MarkFlagRequired("infra-id")

	cmd.Run = func(cmd *cobra.Command, args []string) {
		if err := opts.CreateIAM(); err != nil {
			log.Error(err, "Error")
			os.Exit(1)
		}
	}

	return cmd
}

func (o *CreateIAMOptions) CreateIAM() error {
	var err error
	iamClient, err := IAMClient(o.AWSCredentialsFile, o.Region)
	if err != nil {
		return err
	}
	s3client, err := S3Client(o.AWSCredentialsFile, o.Region)
	if err != nil {
		return err
	}
	results, err := o.CreateOIDCResources(iamClient, s3client)
	if err != nil {
		return err
	}
	profileName := DefaultProfileName(o.InfraID)
	err = o.CreateWorkerInstanceProfile(iamClient, profileName)
	if err != nil {
		return err
	}
	log.Info("Created IAM profile", "name", profileName, "region", o.Region)

	// Write out stateful information
	out := os.Stdout
	if len(o.OutputFile) > 0 {
		var err error
		out, err = os.Create(o.OutputFile)
		if err != nil {
			return fmt.Errorf("cannot create output file: %w", err)
		}
		defer out.Close()
	}
	outputBytes, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize result: %w", err)
	}
	_, err = out.Write(outputBytes)
	if err != nil {
		return fmt.Errorf("failed to write result: %w", err)
	}
	return nil
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

func S3Client(creds, region string) (s3iface.S3API, error) {
	awsConfig := &aws.Config{
		Region: aws.String(region),
	}
	awsConfig.Credentials = credentials.NewSharedCredentials(creds, "default")
	s, err := session.NewSession(awsConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create client session: %w", err)
	}
	return s3.New(s), nil
}
