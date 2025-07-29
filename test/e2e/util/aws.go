package util

import (
	"errors"
	"fmt"
	"strings"

	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	"github.com/openshift/hypershift/support/oidc"
	"github.com/openshift/hypershift/support/util"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/iam"
	"github.com/aws/aws-sdk-go/service/iam/iamiface"
	"github.com/aws/aws-sdk-go/service/kms"
	"github.com/aws/aws-sdk-go/service/s3"

	"github.com/go-logr/logr"
)

func GetKMSKeyArn(awsCreds, awsRegion, alias string) (*string, error) {
	if alias == "" {
		return aws.String(""), nil
	}

	awsSession := awsutil.NewSession("e2e-kms", awsCreds, "", "", awsRegion)
	awsConfig := awsutil.NewConfig()
	kmsClient := kms.New(awsSession, awsConfig)

	input := &kms.DescribeKeyInput{
		KeyId: aws.String(alias),
	}
	out, err := kmsClient.DescribeKey(input)
	if err != nil {
		return nil, err
	}
	if out.KeyMetadata == nil {
		return nil, fmt.Errorf("KMS key with alias %v doesn't exist", alias)
	}

	return out.KeyMetadata.Arn, nil
}

func GetDefaultSecurityGroup(awsCreds, awsRegion, sgID string) (*ec2.SecurityGroup, error) {
	awsSession := awsutil.NewSession("e2e-ec2", awsCreds, "", "", awsRegion)
	awsConfig := awsutil.NewConfig()
	ec2Client := ec2.New(awsSession, awsConfig)

	describeSGResult, err := ec2Client.DescribeSecurityGroups(&ec2.DescribeSecurityGroupsInput{
		GroupIds: []*string{aws.String(sgID)},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get security group: %w", err)
	}
	if len(describeSGResult.SecurityGroups) == 0 {
		return nil, fmt.Errorf("no security group found with ID %s", sgID)
	}
	return describeSGResult.SecurityGroups[0], nil
}

func GetS3Client(awsCreds, awsRegion string) *s3.S3 {
	awsSession := awsutil.NewSession("e2e-s3", awsCreds, "", "", awsRegion)
	awsConfig := awsutil.NewConfig()
	return s3.New(awsSession, awsConfig)
}

func GetIAMClient(awsCreds, awsRegion string) iamiface.IAMAPI {
	awsSession := awsutil.NewSession("e2e-iam", awsCreds, "", "", awsRegion)
	awsConfig := awsutil.NewConfig()
	return iam.New(awsSession, awsConfig)
}

func PutRolePolicy(awsCreds, awsRegion, roleARN string, policy string) (func() error, error) {
	iamClient := GetIAMClient(awsCreds, awsRegion)
	roleName := roleARN[strings.LastIndex(roleARN, "/")+1:]
	policyName := util.HashSimple(policy)

	_, err := iamClient.PutRolePolicy(&iam.PutRolePolicyInput{
		RoleName:       aws.String(roleName),
		PolicyName:     aws.String(policyName),
		PolicyDocument: aws.String(policy),
	})
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			if aerr.Code() == iam.ErrCodeNoSuchEntityException {
				return nil, fmt.Errorf("role %s doesn't exist", roleARN)
			}
		}
		return nil, fmt.Errorf("failed to put role policy: %w", err)
	}

	cleanupFunc := func() error {
		_, err := iamClient.DeleteRolePolicy(&iam.DeleteRolePolicyInput{
			RoleName:   aws.String(roleName),
			PolicyName: aws.String(policyName),
		})
		if err != nil {
			if aerr, ok := err.(awserr.Error); ok {
				if aerr.Code() == iam.ErrCodeNoSuchEntityException {
					return nil
				}
			}
			return fmt.Errorf("failed to delete role policy: %w", err)
		}
		return nil
	}

	return cleanupFunc, nil
}

func DestroyOIDCProvider(log logr.Logger, iamClient iamiface.IAMAPI, issuerURL string) {
	oidcProviderList, err := iamClient.ListOpenIDConnectProviders(&iam.ListOpenIDConnectProvidersInput{})
	if err != nil {
		log.Error(err, "failed to list OIDC providers")
		return
	}

	providerName := strings.TrimPrefix(issuerURL, "https://")
	for _, provider := range oidcProviderList.OpenIDConnectProviderList {
		if strings.Contains(*provider.Arn, providerName) {
			_, err := iamClient.DeleteOpenIDConnectProvider(&iam.DeleteOpenIDConnectProviderInput{
				OpenIDConnectProviderArn: provider.Arn,
			})
			if err != nil {
				if aerr, ok := err.(awserr.Error); ok {
					if aerr.Code() != iam.ErrCodeNoSuchEntityException {
						log.Error(aerr, "Error deleting OIDC provider", "providerARN", provider.Arn)
						return
					}
				} else {
					log.Error(err, "Error deleting OIDC provider", "providerARN", provider.Arn)
					return
				}
			} else {
				log.Info("Deleted OIDC provider", "providerARN", provider.Arn)
			}
			break
		}
	}

}

func CleanupOIDCBucketObjects(log logr.Logger, s3Client *s3.S3, bucketName, issuerURL string) {
	providerID := issuerURL[strings.LastIndex(issuerURL, "/")+1:]

	objectsToDelete := []*s3.ObjectIdentifier{
		{
			Key: aws.String(providerID + "/.well-known/openid-configuration"),
		},
		{
			Key: aws.String(providerID + oidc.JWKSURI),
		},
	}

	if _, err := s3Client.DeleteObjects(&s3.DeleteObjectsInput{
		Bucket: aws.String(bucketName),
		Delete: &s3.Delete{Objects: objectsToDelete},
	}); err != nil {
		if awsErr := awserr.Error(nil); !errors.As(err, &awsErr) || awsErr.Code() != s3.ErrCodeNoSuchBucket {
			log.Error(awsErr, "failed to delete OIDC objects from S3 bucket", "bucketName", bucketName)
		}
	}
}
