package util

import (
	"fmt"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/kms"
	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
)

const (
	KMS_KEY_ALIAS = "alias/hypershift-ci"
)

func GetKMSKeyArn(awsCreds, awsRegion string) (*string, error) {
	awsSession := awsutil.NewSession("e2e-kms", awsCreds, "", "", awsRegion)
	awsConfig := awsutil.NewConfig()
	kmsClient := kms.New(awsSession, awsConfig)

	input := &kms.DescribeKeyInput{
		KeyId: aws.String(KMS_KEY_ALIAS),
	}
	out, err := kmsClient.DescribeKey(input)
	if err != nil {
		return nil, err
	}
	if out.KeyMetadata == nil {
		return nil, fmt.Errorf("KMS key with alias %v doesn't exist", KMS_KEY_ALIAS)
	}

	return out.KeyMetadata.Arn, nil
}
