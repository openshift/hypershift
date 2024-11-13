package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	"github.com/openshift/hypershift/cmd/log"
	"github.com/openshift/hypershift/cmd/util"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/iam"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
)

type CreateIAMOptions struct {
	Region                          string
	AWSCredentialsOpts              awsutil.AWSCredentialsOptions
	OIDCStorageProviderS3BucketName string
	OIDCStorageProviderS3Region     string
	PublicZoneID                    string
	PrivateZoneID                   string
	LocalZoneID                     string
	InfraID                         string
	IssuerURL                       string
	OutputFile                      string
	KMSKeyARN                       string
	AdditionalTags                  []string
	VPCOwnerCredentialsOpts         awsutil.AWSCredentialsOptions
	PrivateZonesInClusterAccount    bool

	CredentialsSecretData *util.CredentialsSecretData

	additionalIAMTags []*iam.Tag
}

type CreateIAMOutput struct {
	Region             string              `json:"region"`
	ProfileName        string              `json:"profileName"`
	InfraID            string              `json:"infraID"`
	IssuerURL          string              `json:"issuerURL"`
	Roles              hyperv1.AWSRolesRef `json:"roles"`
	KMSKeyARN          string              `json:"kmsKeyARN"`
	KMSProviderRoleARN string              `json:"kmsProviderRoleARN"`

	SharedIngressRoleARN      string `json:"sharedIngressRoleARN,omitempty"`
	SharedControlPlaneRoleARN string `json:"sharedControlPlaneRoleARN,omitempty"`
}

func NewCreateIAMCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "aws",
		Short:        "Creates AWS instance profile for workers",
		SilenceUsage: true,
	}

	opts := CreateIAMOptions{
		Region:  "us-east-1",
		InfraID: "",
	}

	cmd.Flags().StringVar(&opts.InfraID, "infra-id", opts.InfraID, "Infrastructure ID to use for AWS resources.")
	cmd.Flags().StringVar(&opts.OIDCStorageProviderS3BucketName, "oidc-storage-provider-s3-bucket-name", "", "The name of the bucket in which the OIDC discovery document is stored")
	cmd.Flags().StringVar(&opts.OIDCStorageProviderS3Region, "oidc-storage-provider-s3-region", "", "The region of the bucket in which the OIDC discovery document is stored")
	cmd.Flags().StringVar(&opts.IssuerURL, "oidc-issuer-url", "", "The OIDC provider issuer URL")
	cmd.Flags().StringVar(&opts.Region, "region", opts.Region, "Region where cluster infra should be created")
	cmd.Flags().StringVar(&opts.OutputFile, "output-file", opts.OutputFile, "Path to file that will contain output information from infra resources (optional)")
	cmd.Flags().StringVar(&opts.PublicZoneID, "public-zone-id", opts.PublicZoneID, "The id of the clusters public route53 zone")
	cmd.Flags().StringVar(&opts.PrivateZoneID, "private-zone-id", opts.PrivateZoneID, "The id of the cluters private route53 zone")
	cmd.Flags().StringVar(&opts.LocalZoneID, "local-zone-id", opts.LocalZoneID, "The id of the clusters local route53 zone")
	cmd.Flags().StringVar(&opts.KMSKeyARN, "kms-key-arn", opts.KMSKeyARN, "The ARN of the KMS key to use for Etcd encryption. If not supplied, etcd encryption will default to using a generated AESCBC key.")
	cmd.Flags().StringSliceVar(&opts.AdditionalTags, "additional-tags", opts.AdditionalTags, "Additional tags to set on AWS resources")
	cmd.Flags().BoolVar(&opts.PrivateZonesInClusterAccount, "private-zones-in-cluster-account", opts.PrivateZonesInClusterAccount, "In shared VPC infrastructure, create private hosted zones in cluster account")

	opts.AWSCredentialsOpts.BindFlags(cmd.Flags())
	opts.VPCOwnerCredentialsOpts.BindVPCOwnerFlags(cmd.Flags())

	_ = cmd.MarkFlagRequired("infra-id")
	_ = cmd.MarkFlagRequired("public-zone-id")
	_ = cmd.MarkFlagRequired("private-zone-id")
	_ = cmd.MarkFlagRequired("local-zone-id")
	_ = cmd.MarkFlagRequired("oidc-bucket-name")
	_ = cmd.MarkFlagRequired("oidc-bucket-region")

	logger := log.Log
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		err := opts.AWSCredentialsOpts.Validate()
		if err != nil {
			return err
		}
		client, err := util.GetClient()
		if err != nil {
			logger.Error(err, "failed to create client")
			return err
		}
		if err := opts.Run(cmd.Context(), client, logger); err != nil {
			logger.Error(err, "Failed to create infrastructure")
			return err
		}
		return nil
	}

	return cmd
}

func (o *CreateIAMOptions) Run(ctx context.Context, client crclient.Client, logger logr.Logger) error {
	results, err := o.CreateIAM(ctx, client, logger)
	if err != nil {
		return err
	}
	return o.Output(results)
}

func (o *CreateIAMOptions) Output(results *CreateIAMOutput) error {
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

func (o *CreateIAMOptions) CreateIAM(ctx context.Context, client crclient.Client, logger logr.Logger) (*CreateIAMOutput, error) {
	var err error
	if err = o.ParseAdditionalTags(); err != nil {
		return nil, err
	}
	if o.OIDCStorageProviderS3BucketName == "" || o.OIDCStorageProviderS3Region == "" {
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Namespace: "kube-public", Name: "oidc-storage-provider-s3-config"},
		}
		if err := client.Get(ctx, crclient.ObjectKeyFromObject(cm), cm); err != nil {
			return nil, fmt.Errorf("failed to discover OIDC bucket configuration: failed to get the %s/%s configmap: %w", cm.Namespace, cm.Name, err)
		}
		// Set both, doesn't make sense to only get one from the configmap
		o.OIDCStorageProviderS3BucketName = cm.Data["name"]
		o.OIDCStorageProviderS3Region = cm.Data["region"]
	}

	var errs []error
	if o.OIDCStorageProviderS3BucketName == "" {
		errs = append(errs, errors.New("mandatory --oidc-storage-provider-s3-bucket-name could not be discovered from the cluster's ConfigMap in 'kube-public' and wasn't explicitly passed either"))
	}
	if o.OIDCStorageProviderS3Region == "" {
		errs = append(errs, errors.New("mandatory --oidc-storage-provider-s3-region could not be discovered from cluster's  ConfigMap in 'kube-public' and wasn't explicitly passed either"))
	}
	if err := utilerrors.NewAggregate(errs); err != nil {
		return nil, err
	}

	awsSession, err := o.AWSCredentialsOpts.GetSession("cli-create-iam", o.CredentialsSecretData, o.Region)
	if err != nil {
		return nil, err
	}

	sharedVPC := false
	if o.VPCOwnerCredentialsOpts.AWSCredentialsFile != "" {
		sharedVPC = true
	}

	awsConfig := awsutil.NewConfig()
	iamClient := iam.New(awsSession, awsConfig)

	results, err := o.CreateOIDCResources(iamClient, logger, sharedVPC)
	if err != nil {
		return nil, err
	}

	if sharedVPC {
		vpcOwnerAWSSession, err := o.VPCOwnerCredentialsOpts.GetSession("cli-create-iam", nil, o.Region)
		if err != nil {
			return nil, err
		}
		vpcOwnerIAMClient := iam.New(vpcOwnerAWSSession, awsConfig)

		route53RoleClient := vpcOwnerIAMClient
		if o.PrivateZonesInClusterAccount {
			route53RoleClient = iamClient
		}
		if results.SharedIngressRoleARN, err = o.CreateSharedVPCRoute53Role(route53RoleClient, logger, results.Roles.IngressARN, results.Roles.ControlPlaneOperatorARN); err != nil {
			return nil, err
		}
		if results.SharedControlPlaneRoleARN, err = o.CreateSharedVPCEndpointRole(vpcOwnerIAMClient, logger, results.Roles.ControlPlaneOperatorARN); err != nil {
			return nil, err
		}
	}

	profileName := DefaultProfileName(o.InfraID)
	results.ProfileName = profileName
	results.KMSKeyARN = o.KMSKeyARN
	err = o.CreateWorkerInstanceProfile(iamClient, profileName, logger)
	if err != nil {
		return nil, err
	}
	logger.Info("Created IAM profile", "name", profileName, "region", o.Region)

	return results, nil
}

func (o *CreateIAMOptions) ParseAdditionalTags() error {
	parsed, err := util.ParseAWSTags(o.AdditionalTags)
	if err != nil {
		return err
	}
	for k, v := range parsed {
		o.additionalIAMTags = append(o.additionalIAMTags, &iam.Tag{
			Key:   aws.String(k),
			Value: aws.String(v),
		})
	}
	return nil
}

func oidcDiscoveryURL(bucketName, region, infraID string) string {
	if bucketName == "" || region == "" || infraID == "" {
		panic(fmt.Sprintf("bucket: %q, region: %q, infraID: %q", bucketName, region, infraID))
	}
	return fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", bucketName, region, infraID)
}
