//go:build e2ev2

package lifecycle

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/kms/types"
)

type fakeKMSKeyDescriber struct {
	output *kms.DescribeKeyOutput
	err    error
	input  string
}

func (client *fakeKMSKeyDescriber) DescribeKey(ctx context.Context, input *kms.DescribeKeyInput, options ...func(*kms.Options)) (*kms.DescribeKeyOutput, error) {
	client.input = aws.ToString(input.KeyId)
	return client.output, client.err
}

func TestDescribeEtcdKMSKey(t *testing.T) {
	sentinel := errors.New("injected KMS error")
	for _, test := range []struct {
		name      string
		output    *kms.DescribeKeyOutput
		err       error
		wantError bool
	}{
		{name: "When metadata contains an ARN, it should resolve the exact alias", output: &kms.DescribeKeyOutput{KeyMetadata: &types.KeyMetadata{Arn: aws.String("arn:aws:kms:us-east-1:123456789012:key/example")}}},
		{name: "When KMS fails, it should preserve the error", err: sentinel, wantError: true},
		{name: "When output is nil, it should fail", wantError: true},
		{name: "When key metadata is absent, it should fail", output: &kms.DescribeKeyOutput{}, wantError: true},
		{name: "When the ARN is absent, it should fail", output: &kms.DescribeKeyOutput{KeyMetadata: &types.KeyMetadata{}}, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &fakeKMSKeyDescriber{output: test.output, err: test.err}
			arn, err := describeEtcdKMSKey(t.Context(), client, "alias/e2e-etcd")
			if client.input != "alias/e2e-etcd" || (err != nil) != test.wantError || test.err != nil && !errors.Is(err, sentinel) {
				t.Fatalf("unexpected alias lookup: %s, %v", client.input, err)
			}
			if !test.wantError && arn != aws.ToString(test.output.KeyMetadata.Arn) {
				t.Fatal("resolved ARN differs from DescribeKey metadata")
			}
		})
	}
	if _, err := describeEtcdKMSKey(t.Context(), &fakeKMSKeyDescriber{}, ""); err == nil {
		t.Fatal("empty alias must fail before a KMS request")
	}
}
