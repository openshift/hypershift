package aws

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/openshift/hypershift/support/awsapi"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancing"
	elbtypes "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancing/types"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbv2types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/go-logr/logr"
	"go.uber.org/mock/gomock"
)

func TestEmptyBucket(t *testing.T) {
	tests := []struct {
		name          string
		bucketName    string
		setupMock     func(*awsapi.MockS3API)
		expectError   bool
		errorContains string
	}{
		{
			name:       "When deleting objects succeeds it should return nil",
			bucketName: "test-bucket",
			setupMock: func(m *awsapi.MockS3API) {
				m.EXPECT().ListObjectsV2(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&s3.ListObjectsV2Output{
						Contents: []s3types.Object{
							{Key: aws.String("file1.txt")},
							{Key: aws.String("file2.txt")},
							{Key: aws.String("file3.txt")},
						},
					}, nil,
				)
				m.EXPECT().DeleteObjects(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&s3.DeleteObjectsOutput{
						Deleted: []s3types.DeletedObject{
							{Key: aws.String("file1.txt")},
							{Key: aws.String("file2.txt")},
							{Key: aws.String("file3.txt")},
						},
					}, nil,
				)
			},
			expectError: false,
		},
		{
			name:       "When partial deletion fails it should return error",
			bucketName: "test-bucket",
			setupMock: func(m *awsapi.MockS3API) {
				m.EXPECT().ListObjectsV2(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&s3.ListObjectsV2Output{
						Contents: []s3types.Object{
							{Key: aws.String("file1.txt")},
							{Key: aws.String("file2.txt")},
							{Key: aws.String("file3.txt")},
						},
					}, nil,
				)
				m.EXPECT().DeleteObjects(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&s3.DeleteObjectsOutput{
						Deleted: []s3types.DeletedObject{
							{Key: aws.String("file1.txt")},
							{Key: aws.String("file2.txt")},
						},
						Errors: []s3types.Error{
							{
								Key:     aws.String("file3.txt"),
								Code:    aws.String("AccessDenied"),
								Message: aws.String("Access Denied"),
							},
						},
					}, nil,
				)
			},
			expectError:   true,
			errorContains: "failed to delete 1 objects from bucket test-bucket",
		},
		{
			name:       "When bucket does not exist it should succeed",
			bucketName: "non-existent-bucket",
			setupMock: func(m *awsapi.MockS3API) {
				m.EXPECT().ListObjectsV2(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					nil, &s3types.NoSuchBucket{Message: aws.String("The specified bucket does not exist")},
				)
			},
			expectError: false,
		},
		{
			name:       "When API error occurs it should return error",
			bucketName: "test-bucket",
			setupMock: func(m *awsapi.MockS3API) {
				m.EXPECT().ListObjectsV2(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					nil, errors.New("network timeout"),
				)
			},
			expectError:   true,
			errorContains: "network timeout",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockS3 := awsapi.NewMockS3API(ctrl)
			tt.setupMock(mockS3)

			err := emptyBucket(ctx, mockS3, tt.bucketName)

			if tt.expectError {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tt.errorContains != "" && !strings.Contains(err.Error(), tt.errorContains) {
					t.Errorf("expected error containing %q, got: %v", tt.errorContains, err)
				}
			} else {
				if err != nil {
					t.Errorf("expected no error, got: %v", err)
				}
			}
		})
	}
}

func TestEmptyBucket_Pagination(t *testing.T) {
	t.Run("When processing large bucket it should paginate correctly", func(t *testing.T) {
		ctx := context.Background()
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		bucketName := "test-large-bucket"
		mockS3 := awsapi.NewMockS3API(ctrl)

		// First page: 1000 objects
		firstPageObjects := make([]s3types.Object, 1000)
		for i := 0; i < 1000; i++ {
			firstPageObjects[i] = s3types.Object{Key: aws.String(fmt.Sprintf("file-%d.txt", i))}
		}
		mockS3.EXPECT().ListObjectsV2(gomock.Any(), gomock.Any(), gomock.Any()).Return(
			&s3.ListObjectsV2Output{
				Contents:              firstPageObjects,
				IsTruncated:           aws.Bool(true),
				NextContinuationToken: aws.String("token1"),
			}, nil,
		)
		mockS3.EXPECT().DeleteObjects(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
			func(ctx context.Context, input *s3.DeleteObjectsInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error) {
				deleted := make([]s3types.DeletedObject, len(input.Delete.Objects))
				for i, obj := range input.Delete.Objects {
					deleted[i] = s3types.DeletedObject{Key: obj.Key}
				}
				return &s3.DeleteObjectsOutput{Deleted: deleted}, nil
			},
		)

		// Second page: 1000 objects
		secondPageObjects := make([]s3types.Object, 1000)
		for i := 1000; i < 2000; i++ {
			secondPageObjects[i-1000] = s3types.Object{Key: aws.String(fmt.Sprintf("file-%d.txt", i))}
		}
		mockS3.EXPECT().ListObjectsV2(gomock.Any(), gomock.Any(), gomock.Any()).Return(
			&s3.ListObjectsV2Output{
				Contents:              secondPageObjects,
				IsTruncated:           aws.Bool(true),
				NextContinuationToken: aws.String("token2"),
			}, nil,
		)
		mockS3.EXPECT().DeleteObjects(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
			func(ctx context.Context, input *s3.DeleteObjectsInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error) {
				deleted := make([]s3types.DeletedObject, len(input.Delete.Objects))
				for i, obj := range input.Delete.Objects {
					deleted[i] = s3types.DeletedObject{Key: obj.Key}
				}
				return &s3.DeleteObjectsOutput{Deleted: deleted}, nil
			},
		)

		// Third page: 500 objects (final page)
		thirdPageObjects := make([]s3types.Object, 500)
		for i := 2000; i < 2500; i++ {
			thirdPageObjects[i-2000] = s3types.Object{Key: aws.String(fmt.Sprintf("file-%d.txt", i))}
		}
		mockS3.EXPECT().ListObjectsV2(gomock.Any(), gomock.Any(), gomock.Any()).Return(
			&s3.ListObjectsV2Output{
				Contents: thirdPageObjects,
			}, nil,
		)
		mockS3.EXPECT().DeleteObjects(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
			func(ctx context.Context, input *s3.DeleteObjectsInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error) {
				deleted := make([]s3types.DeletedObject, len(input.Delete.Objects))
				for i, obj := range input.Delete.Objects {
					deleted[i] = s3types.DeletedObject{Key: obj.Key}
				}
				return &s3.DeleteObjectsOutput{
					Deleted: deleted,
				}, nil
			},
		)

		err := emptyBucket(ctx, mockS3, bucketName)
		if err != nil {
			t.Errorf("expected no error, got: %v", err)
		}
	})
}

func TestDestroyV1ELBs(t *testing.T) {
	const targetVPC = "vpc-111"

	tests := []struct {
		name           string
		setupMock      func(*awsapi.MockELBAPI)
		expectErrCount int
	}{
		{
			name: "When load balancers exist in mixed VPCs it should delete only target VPC ones",
			setupMock: func(m *awsapi.MockELBAPI) {
				m.EXPECT().DescribeLoadBalancers(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&elasticloadbalancing.DescribeLoadBalancersOutput{
						LoadBalancerDescriptions: []elbtypes.LoadBalancerDescription{
							{LoadBalancerName: aws.String("lb-target"), VPCId: aws.String(targetVPC)},
							{LoadBalancerName: aws.String("lb-other"), VPCId: aws.String("vpc-other")},
						},
					}, nil,
				)
				m.EXPECT().DeleteLoadBalancer(gomock.Any(), &elasticloadbalancing.DeleteLoadBalancerInput{
					LoadBalancerName: aws.String("lb-target"),
				}, gomock.Any()).Return(&elasticloadbalancing.DeleteLoadBalancerOutput{}, nil)
			},
			expectErrCount: 0,
		},
		{
			name: "When paginator returns multiple pages it should process all pages",
			setupMock: func(m *awsapi.MockELBAPI) {
				gomock.InOrder(
					m.EXPECT().DescribeLoadBalancers(gomock.Any(), gomock.Any(), gomock.Any()).Return(
						&elasticloadbalancing.DescribeLoadBalancersOutput{
							LoadBalancerDescriptions: []elbtypes.LoadBalancerDescription{
								{LoadBalancerName: aws.String("lb-page1"), VPCId: aws.String(targetVPC)},
							},
							NextMarker: aws.String("token1"),
						}, nil,
					),
					m.EXPECT().DescribeLoadBalancers(gomock.Any(), gomock.Any(), gomock.Any()).Return(
						&elasticloadbalancing.DescribeLoadBalancersOutput{
							LoadBalancerDescriptions: []elbtypes.LoadBalancerDescription{
								{LoadBalancerName: aws.String("lb-page2"), VPCId: aws.String(targetVPC)},
							},
						}, nil,
					),
				)
				m.EXPECT().DeleteLoadBalancer(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&elasticloadbalancing.DeleteLoadBalancerOutput{}, nil,
				).Times(2)
			},
			expectErrCount: 0,
		},
		{
			name: "When DescribeLoadBalancers fails it should return the error",
			setupMock: func(m *awsapi.MockELBAPI) {
				m.EXPECT().DescribeLoadBalancers(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					nil, errors.New("api error"),
				)
			},
			expectErrCount: 1,
		},
		{
			name: "When DeleteLoadBalancer fails it should collect the error and continue",
			setupMock: func(m *awsapi.MockELBAPI) {
				m.EXPECT().DescribeLoadBalancers(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&elasticloadbalancing.DescribeLoadBalancersOutput{
						LoadBalancerDescriptions: []elbtypes.LoadBalancerDescription{
							{LoadBalancerName: aws.String("lb-fail"), VPCId: aws.String(targetVPC)},
							{LoadBalancerName: aws.String("lb-ok"), VPCId: aws.String(targetVPC)},
						},
					}, nil,
				)
				m.EXPECT().DeleteLoadBalancer(gomock.Any(), &elasticloadbalancing.DeleteLoadBalancerInput{
					LoadBalancerName: aws.String("lb-fail"),
				}, gomock.Any()).Return(nil, errors.New("delete failed"))
				m.EXPECT().DeleteLoadBalancer(gomock.Any(), &elasticloadbalancing.DeleteLoadBalancerInput{
					LoadBalancerName: aws.String("lb-ok"),
				}, gomock.Any()).Return(&elasticloadbalancing.DeleteLoadBalancerOutput{}, nil)
			},
			expectErrCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)

			mockELB := awsapi.NewMockELBAPI(ctrl)
			tt.setupMock(mockELB)

			o := &DestroyInfraOptions{Log: logr.Discard()}
			errs := o.DestroyV1ELBs(context.Background(), mockELB, targetVPC)

			if len(errs) != tt.expectErrCount {
				t.Errorf("expected %d errors, got %d: %v", tt.expectErrCount, len(errs), errs)
			}
		})
	}
}

func TestDestroyV2ELBs(t *testing.T) {
	const targetVPC = "vpc-222"

	tests := []struct {
		name           string
		setupMock      func(*awsapi.MockELBV2API)
		expectErrCount int
	}{
		{
			name: "When load balancers and target groups exist in mixed VPCs it should delete only target VPC ones",
			setupMock: func(m *awsapi.MockELBV2API) {
				m.EXPECT().DescribeLoadBalancers(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&elasticloadbalancingv2.DescribeLoadBalancersOutput{
						LoadBalancers: []elbv2types.LoadBalancer{
							{LoadBalancerArn: aws.String("arn:lb:1"), LoadBalancerName: aws.String("lb-1"), VpcId: aws.String(targetVPC)},
							{LoadBalancerArn: aws.String("arn:lb:other"), LoadBalancerName: aws.String("lb-other"), VpcId: aws.String("vpc-other")},
						},
					}, nil,
				)
				m.EXPECT().DeleteLoadBalancer(gomock.Any(), &elasticloadbalancingv2.DeleteLoadBalancerInput{
					LoadBalancerArn: aws.String("arn:lb:1"),
				}, gomock.Any()).Return(&elasticloadbalancingv2.DeleteLoadBalancerOutput{}, nil)
				m.EXPECT().DescribeTargetGroups(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&elasticloadbalancingv2.DescribeTargetGroupsOutput{
						TargetGroups: []elbv2types.TargetGroup{
							{TargetGroupArn: aws.String("arn:tg:1"), TargetGroupName: aws.String("tg-1"), VpcId: aws.String(targetVPC)},
							{TargetGroupArn: aws.String("arn:tg:other"), TargetGroupName: aws.String("tg-other"), VpcId: aws.String("vpc-other")},
						},
					}, nil,
				)
				m.EXPECT().DeleteTargetGroup(gomock.Any(), &elasticloadbalancingv2.DeleteTargetGroupInput{
					TargetGroupArn: aws.String("arn:tg:1"),
				}, gomock.Any()).Return(&elasticloadbalancingv2.DeleteTargetGroupOutput{}, nil)
			},
			expectErrCount: 0,
		},
		{
			name: "When load balancer paginator returns multiple pages it should process all pages",
			setupMock: func(m *awsapi.MockELBV2API) {
				gomock.InOrder(
					m.EXPECT().DescribeLoadBalancers(gomock.Any(), gomock.Any(), gomock.Any()).Return(
						&elasticloadbalancingv2.DescribeLoadBalancersOutput{
							LoadBalancers: []elbv2types.LoadBalancer{
								{LoadBalancerArn: aws.String("arn:lb:p1"), LoadBalancerName: aws.String("lb-p1"), VpcId: aws.String(targetVPC)},
							},
							NextMarker: aws.String("token1"),
						}, nil,
					),
					m.EXPECT().DescribeLoadBalancers(gomock.Any(), gomock.Any(), gomock.Any()).Return(
						&elasticloadbalancingv2.DescribeLoadBalancersOutput{
							LoadBalancers: []elbv2types.LoadBalancer{
								{LoadBalancerArn: aws.String("arn:lb:p2"), LoadBalancerName: aws.String("lb-p2"), VpcId: aws.String(targetVPC)},
							},
						}, nil,
					),
				)
				m.EXPECT().DeleteLoadBalancer(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&elasticloadbalancingv2.DeleteLoadBalancerOutput{}, nil,
				).Times(2)
				m.EXPECT().DescribeTargetGroups(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&elasticloadbalancingv2.DescribeTargetGroupsOutput{}, nil,
				)
			},
			expectErrCount: 0,
		},
		{
			name: "When DescribeLoadBalancers fails it should return the error",
			setupMock: func(m *awsapi.MockELBV2API) {
				m.EXPECT().DescribeLoadBalancers(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					nil, errors.New("api error"),
				)
				m.EXPECT().DescribeTargetGroups(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&elasticloadbalancingv2.DescribeTargetGroupsOutput{}, nil,
				)
			},
			expectErrCount: 1,
		},
		{
			name: "When DescribeTargetGroups fails it should return the error",
			setupMock: func(m *awsapi.MockELBV2API) {
				m.EXPECT().DescribeLoadBalancers(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&elasticloadbalancingv2.DescribeLoadBalancersOutput{}, nil,
				)
				m.EXPECT().DescribeTargetGroups(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					nil, errors.New("api error"),
				)
			},
			expectErrCount: 1,
		},
		{
			name: "When DeleteLoadBalancer fails it should collect the error and continue to target groups",
			setupMock: func(m *awsapi.MockELBV2API) {
				m.EXPECT().DescribeLoadBalancers(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&elasticloadbalancingv2.DescribeLoadBalancersOutput{
						LoadBalancers: []elbv2types.LoadBalancer{
							{LoadBalancerArn: aws.String("arn:lb:fail"), LoadBalancerName: aws.String("lb-fail"), VpcId: aws.String(targetVPC)},
						},
					}, nil,
				)
				m.EXPECT().DeleteLoadBalancer(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					nil, errors.New("delete lb failed"),
				)
				m.EXPECT().DescribeTargetGroups(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&elasticloadbalancingv2.DescribeTargetGroupsOutput{}, nil,
				)
			},
			expectErrCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)

			mockELBV2 := awsapi.NewMockELBV2API(ctrl)
			tt.setupMock(mockELBV2)

			o := &DestroyInfraOptions{Log: logr.Discard()}
			errs := o.DestroyV2ELBs(context.Background(), mockELBV2, targetVPC)

			if len(errs) != tt.expectErrCount {
				t.Errorf("expected %d errors, got %d: %v", tt.expectErrCount, len(errs), errs)
			}
		})
	}
}
