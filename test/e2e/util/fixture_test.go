package util

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"
	resourcegroupstaggingapitypes "github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi/types"
)

type paginatedAWSResourceTaggingClient struct {
	outputs []*resourcegroupstaggingapi.GetResourcesOutput
	inputs  []*resourcegroupstaggingapi.GetResourcesInput
}

func (c *paginatedAWSResourceTaggingClient) GetResources(_ context.Context, input *resourcegroupstaggingapi.GetResourcesInput, _ ...func(*resourcegroupstaggingapi.Options)) (*resourcegroupstaggingapi.GetResourcesOutput, error) {
	c.inputs = append(c.inputs, input)
	output := c.outputs[0]
	c.outputs = c.outputs[1:]
	return output, nil
}

func TestGetTaggedAWSResources(t *testing.T) {
	t.Parallel()
	client := &paginatedAWSResourceTaggingClient{
		outputs: []*resourcegroupstaggingapi.GetResourcesOutput{
			{
				ResourceTagMappingList: []resourcegroupstaggingapitypes.ResourceTagMapping{{ResourceARN: awssdk.String("arn:aws:s3:::first")}},
				PaginationToken:        awssdk.String("next-page"),
			},
			{
				ResourceTagMappingList: []resourcegroupstaggingapitypes.ResourceTagMapping{{ResourceARN: awssdk.String("arn:aws:s3:::second")}},
			},
		},
	}

	mappings, err := getTaggedAWSResources(context.Background(), client, "cluster-id")
	g := NewWithT(t)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(mappings).To(HaveLen(2))
	g.Expect(client.inputs).To(HaveLen(2))
	g.Expect(client.inputs[0].PaginationToken).To(BeNil())
	g.Expect(awssdk.ToString(client.inputs[1].PaginationToken)).To(Equal("next-page"))
}

func TestHasGuestResources(t *testing.T) {
	t.Parallel()
	nlbARN := "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/net/my-nlb/abc123"
	s3ARN := "arn:aws:s3:::my-bucket"
	volumeARN := "arn:aws:ec2:us-east-1:123456789012:volume/vol-abc123"

	tests := []struct {
		name     string
		mappings []resourcegroupstaggingapitypes.ResourceTagMapping
		want     bool
	}{
		{
			name:     "When mappings are empty it should return false",
			mappings: nil,
			want:     false,
		},
		{
			name: "When mappings contain a load balancer it should return true",
			mappings: []resourcegroupstaggingapitypes.ResourceTagMapping{
				{ResourceARN: awssdk.String(nlbARN)},
			},
			want: true,
		},
		{
			name: "When mappings contain an S3 bucket it should return true",
			mappings: []resourcegroupstaggingapitypes.ResourceTagMapping{
				{ResourceARN: awssdk.String(s3ARN)},
			},
			want: true,
		},
		{
			name: "When mappings contain only a non-PV EC2 volume it should return false",
			mappings: []resourcegroupstaggingapitypes.ResourceTagMapping{
				{ResourceARN: awssdk.String(volumeARN)},
			},
			want: false,
		},
		{
			name: "When mappings contain a PV-tagged EC2 volume it should return true",
			mappings: []resourcegroupstaggingapitypes.ResourceTagMapping{
				{
					ResourceARN: awssdk.String(volumeARN),
					Tags: []resourcegroupstaggingapitypes.Tag{
						{Key: awssdk.String("kubernetes.io/created-for/pv/name"), Value: awssdk.String("pvc-1")},
					},
				},
			},
			want: true,
		},
		{
			name: "When mappings contain a malformed ARN it should skip it and continue",
			mappings: []resourcegroupstaggingapitypes.ResourceTagMapping{
				{ResourceARN: awssdk.String("not-an-arn")},
				{ResourceARN: awssdk.String(nlbARN)},
			},
			want: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(hasGuestResources(t, tc.mappings)).To(Equal(tc.want))
		})
	}
}
