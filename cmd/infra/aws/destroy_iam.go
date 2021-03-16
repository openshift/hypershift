package aws

import (
	"fmt"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/iam"
	"github.com/aws/aws-sdk-go/service/iam/iamiface"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3iface"
	"github.com/spf13/cobra"
)

type DestroyIAMOptions struct {
	Region             string
	AWSCredentialsFile string
	InfraID            string
}

func NewDestroyIAMCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "aws",
		Short: "Destroys AWS instance profile for workers",
	}

	opts := DestroyIAMOptions{
		Region:             "us-east-1",
		AWSCredentialsFile: "",
		InfraID:            "",
	}

	cmd.Flags().StringVar(&opts.AWSCredentialsFile, "aws-creds", opts.AWSCredentialsFile, "Path to an AWS credentials file (required)")
	cmd.Flags().StringVar(&opts.InfraID, "infra-id", opts.InfraID, "Infrastructure ID to use for AWS resources.")
	cmd.Flags().StringVar(&opts.Region, "region", opts.Region, "Region where cluster infra lives")

	cmd.MarkFlagRequired("aws-creds")
	cmd.MarkFlagRequired("infra-id")

	cmd.Run = func(cmd *cobra.Command, args []string) {
		if err := opts.DestroyIAM(); err != nil {
			log.Error(err, "Error")
			os.Exit(1)
		}
	}

	return cmd
}

func (o *DestroyIAMOptions) DestroyIAM() error {
	var err error
	iamClient, err := IAMClient(o.AWSCredentialsFile, o.Region)
	if err != nil {
		return err
	}
	s3Client, err := S3Client(o.AWSCredentialsFile, o.Region)
	if err != nil {
		return err
	}
	err = o.DestroyOIDCResources(iamClient, s3Client)
	if err != nil {
		return err
	}
	err = o.DestroyWorkerInstanceProfile(iamClient)
	if err != nil {
		return err
	}
	return nil
}

func (o *DestroyIAMOptions) DestroyOIDCResources(iamClient iamiface.IAMAPI, s3Client s3iface.S3API) error {
	bucketName := o.InfraID

	_, err := s3Client.DeleteObject(&s3.DeleteObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(discoveryURI),
	})
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			if aerr.Code() != s3.ErrCodeNoSuchBucket ||
				aerr.Code() != s3.ErrCodeNoSuchKey {
				log.Error(aerr, "Error deleting OIDC discovery document", bucketName, "key", discoveryURI)
				return aerr
			}
		} else {
			log.Error(err, "Error deleting OIDC discovery document", bucketName, "key", discoveryURI)
			return err
		}
	} else {
		log.Info("Deleted OIDC discovery document", "bucket", bucketName, "key", discoveryURI)
	}

	_, err = s3Client.DeleteObject(&s3.DeleteObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(jwksURI),
	})
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			if aerr.Code() != s3.ErrCodeNoSuchBucket ||
				aerr.Code() != s3.ErrCodeNoSuchKey {
				log.Error(aerr, "Error deleting JWKS document", bucketName, "key", jwksURI)
				return aerr
			}
		} else {
			log.Error(err, "Error deleting JWKS document", bucketName, "key", jwksURI)
			return err
		}
	} else {
		log.Info("Deleted JWKS document", "bucket", bucketName, "key", jwksURI)
	}

	_, err = s3Client.DeleteBucket(&s3.DeleteBucketInput{
		Bucket: aws.String(bucketName),
	})
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			if aerr.Code() != iam.ErrCodeNoSuchEntityException {
				log.Error(aerr, "Error deleting OIDC discovery endpoint", "bucket", bucketName)
				return aerr
			}
		} else {
			log.Error(err, "Error deleting OIDC discovery endpoint", "bucket", bucketName)
			return err
		}
	} else {
		log.Info("Deleted OIDC discovery endpoint", "bucket", bucketName)
	}

	oidcProviderList, err := iamClient.ListOpenIDConnectProviders(&iam.ListOpenIDConnectProvidersInput{})
	if err != nil {
		return err
	}

	for _, provider := range oidcProviderList.OpenIDConnectProviderList {
		if strings.Contains(*provider.Arn, bucketName) {
			_, err := iamClient.DeleteOpenIDConnectProvider(&iam.DeleteOpenIDConnectProviderInput{
				OpenIDConnectProviderArn: provider.Arn,
			})
			if err != nil {
				if aerr, ok := err.(awserr.Error); ok {
					if aerr.Code() != iam.ErrCodeNoSuchEntityException {
						log.Error(aerr, "Error deleting OIDC provider", "providerARN", provider.Arn)
						return aerr
					}
				} else {
					log.Error(err, "Error deleting OIDC provider", "providerARN", provider.Arn)
					return err
				}
			} else {
				log.Info("Deleted OIDC provider", "providerARN", provider.Arn)
			}
			break
		}
	}
	err = o.DestroyOIDCRole(iamClient, "openshift-ingress")
	err = o.DestroyOIDCRole(iamClient, "openshift-image-registry")
	err = o.DestroyOIDCRole(iamClient, "aws-ebs-csi-driver-operator")

	return nil
}

// CreateOIDCRole create an IAM Role with a trust policy for the OIDC provider
func (o *DestroyIAMOptions) DestroyOIDCRole(client iamiface.IAMAPI, name string) error {
	roleName := fmt.Sprintf("%s-%s", o.InfraID, name)
	_, err := client.DeleteRolePolicy(&iam.DeleteRolePolicyInput{
		PolicyName: aws.String(roleName),
		RoleName:   aws.String(roleName),
	})
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			if aerr.Code() != iam.ErrCodeNoSuchEntityException {
				log.Error(aerr, "Error deleting role policy", "role", roleName)
				return aerr
			}
		} else {
			log.Error(err, "Error deleting role policy", "role", roleName)
			return err
		}
	} else {
		log.Info("Deleted role policy", "role", roleName)
	}

	_, err = client.DeleteRole(&iam.DeleteRoleInput{
		RoleName: aws.String(roleName),
	})
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			if aerr.Code() != iam.ErrCodeNoSuchEntityException {
				log.Error(aerr, "Error deleting role", "role", roleName)
				return aerr
			}
		} else {
			log.Error(err, "Error deleting role", "role", roleName)
			return err
		}
	} else {
		log.Info("Deleted role", "role", roleName)
	}
	return nil
}

func (o *DestroyIAMOptions) DestroyWorkerInstanceProfile(client iamiface.IAMAPI) error {
	profileName := DefaultProfileName(o.InfraID)
	instanceProfile, err := existingInstanceProfile(client, profileName)
	if err != nil {
		return fmt.Errorf("cannot check for existing instance profile: %w", err)
	}
	if instanceProfile != nil {
		for _, role := range instanceProfile.Roles {
			_, err := client.RemoveRoleFromInstanceProfile(&iam.RemoveRoleFromInstanceProfileInput{
				InstanceProfileName: aws.String(profileName),
				RoleName:            role.RoleName,
			})
			if err != nil {
				return fmt.Errorf("cannot remove role %s from instance profile %s: %w", aws.StringValue(role.RoleName), profileName, err)
			}
			log.Info("Removed role from instance profile", "profile", profileName, "role", aws.StringValue(role.RoleName))
		}
		_, err := client.DeleteInstanceProfile(&iam.DeleteInstanceProfileInput{
			InstanceProfileName: aws.String(profileName),
		})
		if err != nil {
			return fmt.Errorf("cannot delete instance profile %s: %w", profileName, err)
		}
		log.Info("Deleted instance profile", "profile", profileName)
	}
	roleName := fmt.Sprintf("%s-role", profileName)
	policyName := fmt.Sprintf("%s-policy", profileName)
	role, err := existingRole(client, roleName)
	if err != nil {
		return fmt.Errorf("cannot check for existing role: %w", err)
	}
	if role != nil {
		hasPolicy, err := existingRolePolicy(client, roleName, policyName)
		if err != nil {
			return fmt.Errorf("cannot check for existing role policy: %w", err)
		}
		if hasPolicy {
			_, err := client.DeleteRolePolicy(&iam.DeleteRolePolicyInput{
				PolicyName: aws.String(policyName),
				RoleName:   aws.String(roleName),
			})
			if err != nil {
				return fmt.Errorf("cannot delete role policy %s from role %s: %w", policyName, roleName, err)
			}
			log.Info("Deleted role policy", "role", roleName, "policy", policyName)
		}
		_, err = client.DeleteRole(&iam.DeleteRoleInput{
			RoleName: aws.String(roleName),
		})
		if err != nil {
			return fmt.Errorf("cannot delete role %s: %w", roleName, err)
		}
		log.Info("Deleted role", "role", roleName)
	}
	return nil
}
