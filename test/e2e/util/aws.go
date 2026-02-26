package util

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	"github.com/openshift/hypershift/support/awsapi"
	"github.com/openshift/hypershift/support/oidc"
	"github.com/openshift/hypershift/support/util"

	awsv2 "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/kms"
	"github.com/aws/aws-sdk-go/service/sqs"

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

func GetS3Client(ctx context.Context, awsCreds, awsRegion string) awsapi.S3API {
	awsSessionv2 := awsutil.NewSessionV2(ctx, "e2e-s3", awsCreds, "", "", awsRegion)
	awsConfigv2 := awsutil.NewConfigV2()
	return s3.NewFromConfig(*awsSessionv2, func(o *s3.Options) {
		o.Retryer = awsConfigv2()
	})
}

func GetIAMClient(ctx context.Context, awsCreds, awsRegion string) awsapi.IAMAPI {
	awsSession := awsutil.NewSessionV2(ctx, "e2e-iam", awsCreds, "", "", awsRegion)
	awsConfig := awsutil.NewConfigV2()
	return iam.NewFromConfig(*awsSession, func(o *iam.Options) {
		o.Retryer = awsConfig()
	})
}

func GetSQSClient(awsCreds, awsRegion string) *sqs.SQS {
	awsSession := awsutil.NewSession("e2e-sqs", awsCreds, "", "", awsRegion)
	awsConfig := awsutil.NewConfig()
	return sqs.New(awsSession, awsConfig)
}

func PutRolePolicy(ctx context.Context, awsCreds, awsRegion, roleARN string, policy string) (func() error, error) {
	iamClient := GetIAMClient(ctx, awsCreds, awsRegion)
	roleName := roleARN[strings.LastIndex(roleARN, "/")+1:]
	policyName := util.HashSimple(policy)

	_, err := iamClient.PutRolePolicy(ctx, &iam.PutRolePolicyInput{
		RoleName:       awsv2.String(roleName),
		PolicyName:     awsv2.String(policyName),
		PolicyDocument: awsv2.String(policy),
	})
	if err != nil {
		var nse *iamtypes.NoSuchEntityException
		if errors.As(err, &nse) {
			return nil, fmt.Errorf("role %s doesn't exist", roleARN)
		}
		return nil, fmt.Errorf("failed to put role policy: %w", err)
	}

	cleanupFunc := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		_, err := iamClient.DeleteRolePolicy(ctx, &iam.DeleteRolePolicyInput{
			RoleName:   awsv2.String(roleName),
			PolicyName: awsv2.String(policyName),
		})
		if err != nil {
			var nse *iamtypes.NoSuchEntityException
			if errors.As(err, &nse) {
				return nil
			}
			return fmt.Errorf("failed to delete role policy: %w", err)
		}
		return nil
	}

	return cleanupFunc, nil
}

func DestroyOIDCProvider(ctx context.Context, log logr.Logger, iamClient awsapi.IAMAPI, issuerURL string) {
	oidcProviderList, err := iamClient.ListOpenIDConnectProviders(ctx, &iam.ListOpenIDConnectProvidersInput{})
	if err != nil {
		log.Error(err, "failed to list OIDC providers")
		return
	}

	providerName := strings.TrimPrefix(issuerURL, "https://")
	for _, provider := range oidcProviderList.OpenIDConnectProviderList {
		if strings.Contains(*provider.Arn, providerName) {
			_, err := iamClient.DeleteOpenIDConnectProvider(ctx, &iam.DeleteOpenIDConnectProviderInput{
				OpenIDConnectProviderArn: provider.Arn,
			})
			if err != nil {
				var nse *iamtypes.NoSuchEntityException
				if !errors.As(err, &nse) {
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

func CleanupOIDCBucketObjects(ctx context.Context, log logr.Logger, s3Client awsapi.S3API, bucketName, issuerURL string) {
	providerID := issuerURL[strings.LastIndex(issuerURL, "/")+1:]

	objectsToDelete := []s3types.ObjectIdentifier{
		{
			Key: awsv2.String(providerID + "/.well-known/openid-configuration"),
		},
		{
			Key: awsv2.String(providerID + oidc.JWKSURI),
		},
	}

	if _, err := s3Client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
		Bucket: awsv2.String(bucketName),
		Delete: &s3types.Delete{Objects: objectsToDelete},
	}); err != nil {
		var nsbErr *s3types.NoSuchBucket
		if !errors.As(err, &nsbErr) {
			log.Error(err, "failed to delete OIDC objects from S3 bucket", "bucketName", bucketName)
		}
	}
}

// CreateCapacityReservation creates an EC2 capacity reservation and returns its ID and a cleanup function
// that cancels the reservation. The caller is responsible for calling the cleanup function.
func CreateCapacityReservation(ctx context.Context, awsCreds, awsRegion, instanceType, availabilityZone string, instanceCount int64) (string, func() error, error) {
	awsSession := awsutil.NewSession("e2e-capacity-reservation", awsCreds, "", "", awsRegion)
	awsConfig := awsutil.NewConfig()
	ec2Client := ec2.New(awsSession, awsConfig)

	result, err := ec2Client.CreateCapacityReservationWithContext(ctx, &ec2.CreateCapacityReservationInput{
		InstanceType:          aws.String(instanceType),
		InstancePlatform:      aws.String("Linux/UNIX"),
		AvailabilityZone:      aws.String(availabilityZone),
		InstanceCount:         aws.Int64(instanceCount),
		InstanceMatchCriteria: aws.String("targeted"),
		EndDateType:           aws.String("unlimited"),
	})
	if err != nil {
		return "", nil, fmt.Errorf("failed to create capacity reservation: %w", err)
	}

	crID := aws.StringValue(result.CapacityReservation.CapacityReservationId)
	cleanupFunc := func() error {
		cancelCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		_, err := ec2Client.CancelCapacityReservationWithContext(cancelCtx, &ec2.CancelCapacityReservationInput{
			CapacityReservationId: aws.String(crID),
		})
		if err != nil {
			return fmt.Errorf("failed to cancel capacity reservation %s: %w", crID, err)
		}
		return nil
	}

	return crID, cleanupFunc, nil
}
