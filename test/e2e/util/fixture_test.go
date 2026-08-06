package util

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	elbv2 "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbv2types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
	resourcegroupstaggingapitypes "github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi/types"
)

type fakeLoadBalancerDeleter struct {
	attempted []string
	deleted   []string
	errs      []error
}

func (f *fakeLoadBalancerDeleter) DeleteLoadBalancer(_ context.Context, input *elbv2.DeleteLoadBalancerInput, _ ...func(*elbv2.Options)) (*elbv2.DeleteLoadBalancerOutput, error) {
	lbARN := awssdk.ToString(input.LoadBalancerArn)
	f.attempted = append(f.attempted, lbARN)
	if len(f.errs) > 0 {
		err := f.errs[0]
		f.errs = f.errs[1:]
		if err != nil {
			return nil, err
		}
	}
	f.deleted = append(f.deleted, lbARN)
	return &elbv2.DeleteLoadBalancerOutput{}, nil
}

func TestDeleteTaggedLoadBalancers(t *testing.T) {
	t.Parallel()
	validNLBArn := "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/net/my-nlb/abc123"
	validALBArn := "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/my-alb/def456"
	ec2Arn := "arn:aws:ec2:us-east-1:123456789012:volume/vol-abc123"

	tests := []struct {
		name           string
		mappings       []resourcegroupstaggingapitypes.ResourceTagMapping
		errs           []error
		wantErr        bool
		wantErrContain string
		wantAttempted  []string
		wantDeleted    []string
	}{
		{
			name: "When mappings contain NLB and ALB ARNs it should delete both",
			mappings: []resourcegroupstaggingapitypes.ResourceTagMapping{
				{ResourceARN: awssdk.String(validNLBArn)},
				{ResourceARN: awssdk.String(validALBArn)},
			},
			wantAttempted: []string{validNLBArn, validALBArn},
			wantDeleted:   []string{validNLBArn, validALBArn},
		},
		{
			name: "When mappings contain non-LB ARNs it should skip them",
			mappings: []resourcegroupstaggingapitypes.ResourceTagMapping{
				{ResourceARN: awssdk.String(ec2Arn)},
				{ResourceARN: awssdk.String(validNLBArn)},
			},
			wantAttempted: []string{validNLBArn},
			wantDeleted:   []string{validNLBArn},
		},
		{
			name: "When mappings contain malformed ARNs it should skip them",
			mappings: []resourcegroupstaggingapitypes.ResourceTagMapping{
				{ResourceARN: awssdk.String("not-an-arn")},
				{ResourceARN: awssdk.String(validNLBArn)},
			},
			wantAttempted: []string{validNLBArn},
			wantDeleted:   []string{validNLBArn},
		},
		{
			name:     "When mappings are empty it should do nothing",
			mappings: []resourcegroupstaggingapitypes.ResourceTagMapping{},
		},
		{
			name: "When transient error on first LB it should continue to second LB",
			mappings: []resourcegroupstaggingapitypes.ResourceTagMapping{
				{ResourceARN: awssdk.String(validNLBArn)},
				{ResourceARN: awssdk.String(validALBArn)},
			},
			errs:          []error{fmt.Errorf("RequestLimitExceeded")},
			wantAttempted: []string{validNLBArn, validALBArn},
			wantDeleted:   []string{validALBArn},
		},
		{
			name: "When OperationNotPermitted on first LB it should stop immediately",
			mappings: []resourcegroupstaggingapitypes.ResourceTagMapping{
				{ResourceARN: awssdk.String(validNLBArn)},
				{ResourceARN: awssdk.String(validALBArn)},
			},
			errs:           []error{&elbv2types.OperationNotPermittedException{Message: awssdk.String("deletion protection enabled")}},
			wantErr:        true,
			wantErrContain: "terminal error",
			wantAttempted:  []string{validNLBArn},
		},
		{
			name: "When ResourceInUse on first LB it should stop immediately",
			mappings: []resourcegroupstaggingapitypes.ResourceTagMapping{
				{ResourceARN: awssdk.String(validNLBArn)},
				{ResourceARN: awssdk.String(validALBArn)},
			},
			errs:           []error{&elbv2types.ResourceInUseException{Message: awssdk.String("associated with VPC endpoint")}},
			wantErr:        true,
			wantErrContain: "terminal error",
			wantAttempted:  []string{validNLBArn},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			fake := &fakeLoadBalancerDeleter{errs: tc.errs}
			err := deleteTaggedLoadBalancers(context.Background(), t, fake, tc.mappings)

			if tc.wantErr {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tc.wantErrContain))
				g.Expect(fake.attempted).To(Equal(tc.wantAttempted))
				return
			}
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(fake.attempted).To(Equal(tc.wantAttempted))
			g.Expect(fake.deleted).To(Equal(tc.wantDeleted))
		})
	}
}
