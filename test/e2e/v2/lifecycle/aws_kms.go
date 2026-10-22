//go:build e2ev2

package lifecycle

import (
	"context"
	"fmt"

	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kms"
)

type kmsKeyDescriber interface {
	DescribeKey(context.Context, *kms.DescribeKeyInput, ...func(*kms.Options)) (*kms.DescribeKeyOutput, error)
}

func ResolveEtcdKMSKeyARN(ctx context.Context, credentialsFile, region, alias string) (string, error) {
	session := awsutil.NewSession(ctx, "e2ev2-etcd-kms", credentialsFile, "", "", region)
	return describeEtcdKMSKey(ctx, kms.NewFromConfig(*session), alias)
}

func describeEtcdKMSKey(ctx context.Context, client kmsKeyDescriber, alias string) (string, error) {
	if alias == "" {
		return "", fmt.Errorf("an etcd KMS key alias is required")
	}
	key, err := client.DescribeKey(ctx, &kms.DescribeKeyInput{KeyId: aws.String(alias)})
	if err != nil {
		return "", fmt.Errorf("resolving etcd KMS key alias: %w", err)
	}
	if key == nil || key.KeyMetadata == nil || aws.ToString(key.KeyMetadata.Arn) == "" {
		return "", fmt.Errorf("KMS DescribeKey must return an etcd key ARN")
	}
	return *key.KeyMetadata.Arn, nil
}
