package util

import (
	"testing"

	. "github.com/onsi/gomega"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	resourcegroupstaggingapitypes "github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi/types"
)

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
