package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/iam"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	"github.com/openshift/hypershift/cmd/log"
	"github.com/openshift/hypershift/cmd/util"
)

type CreateIAMOptions struct {
	Region                          string
	AWSCredentialsFile              string
	AWSKey                          string
	AWSSecretKey                    string
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

	additionalIAMTags []*iam.Tag
}

type CreateIAMOutput struct {
	Region      string           `json:"region"`
	ProfileName string           `json:"profileName"`
	InfraID     string           `json:"infraID"`
	IssuerURL   string           `json:"issuerURL"`
	Roles       hyperv1.AWSRoles `json:"roles"`
	KMSKeyARN   string           `json:"kmsKeyARN"`

	KubeCloudControllerRoleARN  string `json:"kubeCloudControllerRoleARN"`
	NodePoolManagementRoleARN   string `json:"nodePoolManagementRoleARN"`
	ControlPlaneOperatorRoleARN string `json:"controlPlaneOperatorRoleARN"`
	KMSProviderRoleARN          string `json:"kmsProviderRoleARN"`
}

func NewCreateIAMCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "aws",
		Short:        "Creates AWS instance profile for workers",
		SilenceUsage: true,
	}

	opts := CreateIAMOptions{
		Region:             "us-east-1",
		AWSCredentialsFile: "",
		InfraID:            "",
	}

	cmd.Flags().StringVar(&opts.AWSCredentialsFile, "aws-creds", opts.AWSCredentialsFile, "Path to an AWS credentials file (required)")
	cmd.Flags().StringVar(&opts.InfraID, "infra-id", opts.InfraID, "Infrastructure ID to use for AWS resources.")
	cmd.Flags().StringVar(&opts.OIDCStorageProviderS3BucketName, "oidc-storage-provider-s3-bucket-name", "", "The name of the bucket in which the OIDC discovery document is stored")
	cmd.Flags().StringVar(&opts.OIDCStorageProviderS3Region, "oidc-storage-provider-s3-region", "", "The region of the bucket in which the OIDC discovery document is stored")
	cmd.Flags().StringVar(&opts.Region, "region", opts.Region, "Region where cluster infra should be created")
	cmd.Flags().StringVar(&opts.OutputFile, "output-file", opts.OutputFile, "Path to file that will contain output information from infra resources (optional)")
	cmd.Flags().StringVar(&opts.PublicZoneID, "public-zone-id", opts.PublicZoneID, "The id of the clusters public route53 zone")
	cmd.Flags().StringVar(&opts.PrivateZoneID, "private-zone-id", opts.PrivateZoneID, "The id of the cluters private route53 zone")
	cmd.Flags().StringVar(&opts.LocalZoneID, "local-zone-id", opts.LocalZoneID, "The id of the clusters local route53 zone")
	cmd.Flags().StringVar(&opts.KMSKeyARN, "kms-key-arn", opts.KMSKeyARN, "The ARN of the KMS key to use for Etcd encryption. If not supplied, etcd encryption will default to using a generated AESCBC key.")
	cmd.Flags().StringSliceVar(&opts.AdditionalTags, "additional-tags", opts.AdditionalTags, "Additional tags to set on AWS resources")

	cmd.MarkFlagRequired("aws-creds")
	cmd.MarkFlagRequired("infra-id")
	cmd.MarkFlagRequired("public-zone-id")
	cmd.MarkFlagRequired("private-zone-id")
	cmd.MarkFlagRequired("local-zone-id")
	cmd.MarkFlagRequired("oidc-bucket-name")
	cmd.MarkFlagRequired("oidc-bucket-region")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		client, err := util.GetClient()
		if err != nil {
			log.Log.Error(err, "failed to create client")
			return err
		}
		if err := opts.Run(cmd.Context(), client); err != nil {
			log.Log.Error(err, "Failed to create infrastructure")
			return err
		}
		return nil
	}

	return cmd
}

func (o *CreateIAMOptions) Run(ctx context.Context, client crclient.Client) error {
	results, err := o.CreateIAM(ctx, client)
	if err != nil {
		return err
	}
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

func (o *CreateIAMOptions) CreateIAM(ctx context.Context, client crclient.Client) (*CreateIAMOutput, error) {
	var err error
	if err = o.parseAdditionalTags(); err != nil {
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
		errs = append(errs, errors.New("mandatory --oidc-storage-provider-s3-bucket-name could not be discovered from the cluster's ConfigMap in 'kube-public' and wasn't excplicitly passed either"))
	}
	if o.OIDCStorageProviderS3Region == "" {
		errs = append(errs, errors.New("mandatory --oidc-storage-provider-s3-region could not be discovered from cluster's  ConfigMap in 'kube-public' and wasn't explicitly passed either"))
	}
	if err := utilerrors.NewAggregate(errs); err != nil {
		return nil, err
	}

	o.IssuerURL = oidcDiscoveryURL(o.OIDCStorageProviderS3BucketName, o.OIDCStorageProviderS3Region, o.InfraID)
	log.Log.Info("Detected Issuer URL", "issuer", o.IssuerURL)

	awsSession := awsutil.NewSession("cli-create-iam", o.AWSCredentialsFile, o.AWSKey, o.AWSSecretKey, o.Region)
	awsConfig := awsutil.NewConfig()
	iamClient := iam.New(awsSession, awsConfig)

	results, err := o.CreateOIDCResources(iamClient)
	if err != nil {
		return nil, err
	}
	profileName := DefaultProfileName(o.InfraID)
	results.ProfileName = profileName
	results.KMSKeyARN = o.KMSKeyARN
	err = o.CreateWorkerInstanceProfile(iamClient, profileName)
	if err != nil {
		return nil, err
	}
	log.Log.Info("Created IAM profile", "name", profileName, "region", o.Region)

	return results, nil
}

func (o *CreateIAMOptions) parseAdditionalTags() error {
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
