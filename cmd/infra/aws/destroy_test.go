package aws

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/openshift/hypershift/support/awsapi"

	awsv2 "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

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
							{Key: awsv2.String("file1.txt")},
							{Key: awsv2.String("file2.txt")},
							{Key: awsv2.String("file3.txt")},
						},
					}, nil,
				)
				m.EXPECT().DeleteObjects(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&s3.DeleteObjectsOutput{
						Deleted: []s3types.DeletedObject{
							{Key: awsv2.String("file1.txt")},
							{Key: awsv2.String("file2.txt")},
							{Key: awsv2.String("file3.txt")},
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
							{Key: awsv2.String("file1.txt")},
							{Key: awsv2.String("file2.txt")},
							{Key: awsv2.String("file3.txt")},
						},
					}, nil,
				)
				m.EXPECT().DeleteObjects(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&s3.DeleteObjectsOutput{
						Deleted: []s3types.DeletedObject{
							{Key: awsv2.String("file1.txt")},
							{Key: awsv2.String("file2.txt")},
						},
						Errors: []s3types.Error{
							{
								Key:     awsv2.String("file3.txt"),
								Code:    awsv2.String("AccessDenied"),
								Message: awsv2.String("Access Denied"),
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
					nil, &s3types.NoSuchBucket{Message: awsv2.String("The specified bucket does not exist")},
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
			firstPageObjects[i] = s3types.Object{Key: awsv2.String(fmt.Sprintf("file-%d.txt", i))}
		}
		mockS3.EXPECT().ListObjectsV2(gomock.Any(), gomock.Any(), gomock.Any()).Return(
			&s3.ListObjectsV2Output{
				Contents:              firstPageObjects,
				IsTruncated:           awsv2.Bool(true),
				NextContinuationToken: awsv2.String("token1"),
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
			secondPageObjects[i-1000] = s3types.Object{Key: awsv2.String(fmt.Sprintf("file-%d.txt", i))}
		}
		mockS3.EXPECT().ListObjectsV2(gomock.Any(), gomock.Any(), gomock.Any()).Return(
			&s3.ListObjectsV2Output{
				Contents:              secondPageObjects,
				IsTruncated:           awsv2.Bool(true),
				NextContinuationToken: awsv2.String("token2"),
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
			thirdPageObjects[i-2000] = s3types.Object{Key: awsv2.String(fmt.Sprintf("file-%d.txt", i))}
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
