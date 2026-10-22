//go:build e2ev2

package tests

import (
	"context"
	"fmt"
	"os"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kms"
)

type hostedKMSKeyDescriber interface {
	DescribeKey(context.Context, *kms.DescribeKeyInput, ...func(*kms.Options)) (*kms.DescribeKeyOutput, error)
}

func configuredHostedEtcdKMSKeyARN(ctx context.Context, cluster *hyperv1.HostedCluster) (string, error) {
	input := os.Getenv("E2E_AWS_KMS_KEY_ALIAS")
	if input == "" {
		input = internal.GetEnvVarValue("E2E_AWS_KMS_KEY_ARN")
	}
	if input == "" {
		return "", nil
	}
	if cluster == nil || cluster.Spec.Platform.AWS == nil {
		return "", fmt.Errorf("resolving the etcd KMS key requires an AWS HostedCluster")
	}
	session := awsutil.NewSession(ctx, "e2ev2-etcd-kms", internal.GetEnvVarValue("AWS_GUEST_INFRA_CREDENTIALS_FILE"), "", "", cluster.Spec.Platform.AWS.Region)
	return resolveHostedEtcdKMSKeyARN(ctx, kms.NewFromConfig(*session), input)
}

func resolveHostedEtcdKMSKeyARN(ctx context.Context, client hostedKMSKeyDescriber, input string) (string, error) {
	if input == "" {
		return "", fmt.Errorf("an etcd KMS key ARN or alias is required")
	}
	output, err := client.DescribeKey(ctx, &kms.DescribeKeyInput{KeyId: aws.String(input)})
	if err != nil {
		return "", fmt.Errorf("resolving the configured etcd KMS key: %w", err)
	}
	if output == nil || output.KeyMetadata == nil || aws.ToString(output.KeyMetadata.Arn) == "" {
		return "", fmt.Errorf("KMS DescribeKey must return the configured key ARN")
	}
	return *output.KeyMetadata.Arn, nil
}
