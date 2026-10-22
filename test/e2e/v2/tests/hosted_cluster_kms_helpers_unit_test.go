//go:build e2ev2

package tests

import (
	"context"
	"errors"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
)

type hostedKMSDescriberFunc func(context.Context, *kms.DescribeKeyInput, ...func(*kms.Options)) (*kms.DescribeKeyOutput, error)

func (describer hostedKMSDescriberFunc) DescribeKey(ctx context.Context, input *kms.DescribeKeyInput, options ...func(*kms.Options)) (*kms.DescribeKeyOutput, error) {
	return describer(ctx, input, options...)
}

func TestResolveHostedEtcdKMSKeyARN(t *testing.T) {
	const keyARN = "arn:aws:kms:us-east-1:123456789012:key/00000000-0000-0000-0000-000000000000"
	for _, testCase := range []struct {
		name, input string
		output      *kms.DescribeKeyOutput
		lookupError error
		wantError   bool
	}{
		{name: "When the supplied alias resolves, it should return the key ARN rather than the alias", input: "alias/hypershift-etcd", output: &kms.DescribeKeyOutput{KeyMetadata: &kmstypes.KeyMetadata{Arn: aws.String(keyARN)}}},
		{name: "When the supplied key is already an ARN, it should verify and return that ARN", input: keyARN, output: &kms.DescribeKeyOutput{KeyMetadata: &kmstypes.KeyMetadata{Arn: aws.String(keyARN)}}},
		{name: "When the configured key is empty, it should reject it before AWS lookup", wantError: true},
		{name: "When AWS denies discovery, it should preserve the error", input: "alias/hypershift-etcd", lookupError: errors.New("access denied"), wantError: true},
		{name: "When AWS returns no output, it should reject it", input: "alias/hypershift-etcd", wantError: true},
		{name: "When AWS returns no key metadata, it should reject it", input: "alias/hypershift-etcd", output: &kms.DescribeKeyOutput{}, wantError: true},
		{name: "When AWS returns metadata without an ARN, it should reject it", input: "alias/hypershift-etcd", output: &kms.DescribeKeyOutput{KeyMetadata: &kmstypes.KeyMetadata{}}, wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			called := false
			describer := hostedKMSDescriberFunc(func(_ context.Context, input *kms.DescribeKeyInput, _ ...func(*kms.Options)) (*kms.DescribeKeyOutput, error) {
				called = true
				if aws.ToString(input.KeyId) != testCase.input {
					t.Fatal("DescribeKey must receive the original user input")
				}
				return testCase.output, testCase.lookupError
			})
			resolved, err := resolveHostedEtcdKMSKeyARN(t.Context(), describer, testCase.input)
			if (err != nil) != testCase.wantError {
				t.Fatalf("unexpected discovery error: %v", err)
			}
			if testCase.input == "" && called {
				t.Fatal("empty input must not cause an AWS call")
			}
			if err == nil && resolved != keyARN {
				t.Fatalf("expected resolved key ARN, got %q", resolved)
			}
			if testCase.lookupError != nil && !errors.Is(err, testCase.lookupError) {
				t.Fatal("discovery must preserve the AWS error")
			}
		})
	}
}

func TestConfiguredHostedEtcdKMSKeyARN(t *testing.T) {
	for _, testCase := range []struct {
		name, alias, input string
		cluster            *hyperv1.HostedCluster
		wantError          bool
	}{
		{name: "When a generic fixture supplies no expected key, it should not require AWS credentials"},
		{name: "When an alias is supplied without a HostedCluster, it should reject the missing fixture", alias: "alias/hypershift-etcd", wantError: true},
		{name: "When a key ARN is supplied for a non-AWS cluster, it should reject the missing AWS region", input: "arn:aws:kms:us-east-1:123456789012:key/fixture", cluster: &hyperv1.HostedCluster{}, wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("E2E_AWS_KMS_KEY_ALIAS", testCase.alias)
			t.Setenv("E2E_AWS_KMS_KEY_ARN", testCase.input)
			resolved, err := configuredHostedEtcdKMSKeyARN(t.Context(), testCase.cluster)
			if (err != nil) != testCase.wantError || resolved != "" {
				t.Fatalf("unexpected configuration result: %q, %v", resolved, err)
			}
		})
	}
}
