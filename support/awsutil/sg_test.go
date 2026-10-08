package awsutil

import (
	"context"
	"strings"
	"testing"

	"github.com/openshift/hypershift/support/awsapi"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

type fakeEC2TagClient struct {
	awsapi.EC2API
	createdTags []ec2types.Tag
	deletedTags []ec2types.Tag
	deleteCalls int
}

func (f *fakeEC2TagClient) CreateTags(_ context.Context, input *ec2.CreateTagsInput, _ ...func(*ec2.Options)) (*ec2.CreateTagsOutput, error) {
	f.createdTags = append(f.createdTags, input.Tags...)
	return &ec2.CreateTagsOutput{}, nil
}

func (f *fakeEC2TagClient) DeleteTags(_ context.Context, input *ec2.DeleteTagsInput, _ ...func(*ec2.Options)) (*ec2.DeleteTagsOutput, error) {
	f.deleteCalls++
	f.deletedTags = append(f.deletedTags, input.Tags...)
	return &ec2.DeleteTagsOutput{}, nil
}

func TestUpdateResourceTags_FiltersAWSReservedKeys(t *testing.T) {
	tests := []struct {
		name               string
		remove             map[string]string
		expectDeleteCalled bool
		expectDeletedKeys  []string
	}{
		{
			name: "only aws: prefixed keys are filtered, no DeleteTags call",
			remove: map[string]string{
				"aws:cloudformation:stack-name": "my-stack",
				"aws:cloudformation:stack-id":   "arn:aws:cloudformation:us-east-1:123456:stack/my-stack/guid",
				"aws:cloudformation:logical-id": "DefaultSg",
				"aws:":                          "reserved",
			},
			expectDeleteCalled: false,
		},
		{
			name: "mixed keys, only non-aws keys passed to DeleteTags",
			remove: map[string]string{
				"aws:cloudformation:stack-name": "my-stack",
				"custom-tag":                    "value1",
				"another-tag":                   "value2",
			},
			expectDeleteCalled: true,
			expectDeletedKeys:  []string{"custom-tag", "another-tag"},
		},
		{
			name: "no aws: keys, all passed to DeleteTags",
			remove: map[string]string{
				"red-hat-managed": "true",
				"Name":            "my-sg",
			},
			expectDeleteCalled: true,
			expectDeletedKeys:  []string{"red-hat-managed", "Name"},
		},
		{
			name:               "empty remove map",
			remove:             map[string]string{},
			expectDeleteCalled: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeEC2TagClient{}
			err := UpdateResourceTags(context.Background(), fake, "sg-test123", nil, tt.remove)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if !tt.expectDeleteCalled {
				if fake.deleteCalls != 0 {
					t.Errorf("expected no DeleteTags call, but got %d call(s)", fake.deleteCalls)
				}
				return
			}

			deletedKeys := make(map[string]bool)
			for _, tag := range fake.deletedTags {
				deletedKeys[aws.ToString(tag.Key)] = true
			}

			for _, key := range tt.expectDeletedKeys {
				if !deletedKeys[key] {
					t.Errorf("expected key %q to be deleted, but it wasn't", key)
				}
			}

			if len(deletedKeys) != len(tt.expectDeletedKeys) {
				t.Errorf("expected %d deleted keys, got %d: %v", len(tt.expectDeletedKeys), len(deletedKeys), deletedKeys)
			}

			// Verify no aws: prefixed keys leaked through
			for key := range deletedKeys {
				if strings.HasPrefix(key, "aws:") {
					t.Errorf("aws: prefixed key %q should have been filtered", key)
				}
			}
		})
	}
}
