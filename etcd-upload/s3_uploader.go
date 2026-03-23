package etcdupload

import (
	"context"
	"fmt"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// S3Uploader uploads etcd snapshots to AWS S3.
type S3Uploader struct {
	bucket    string
	region    string
	kmsKeyARN string
	client    S3PutObjectAPI
}

// S3PutObjectAPI defines the S3 client interface used by the uploader.
type S3PutObjectAPI interface {
	PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
}

// NewS3Uploader creates a new S3Uploader.
func NewS3Uploader(bucket, region, credentialsFile, kmsKeyARN string) (*S3Uploader, error) {
	if bucket == "" {
		return nil, fmt.Errorf("--bucket is required for S3 storage type")
	}
	if region == "" {
		return nil, fmt.Errorf("--region is required for S3 storage type")
	}
	if credentialsFile == "" {
		return nil, fmt.Errorf("--credentials-file is required for S3 storage type")
	}

	cfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion(region),
		config.WithSharedCredentialsFiles([]string{credentialsFile}),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	return &S3Uploader{
		bucket:    bucket,
		region:    region,
		kmsKeyARN: kmsKeyARN,
		client:    s3.NewFromConfig(cfg),
	}, nil
}

// Upload uploads a snapshot file to S3 with conditional write and optional SSE-KMS encryption.
func (u *S3Uploader) Upload(ctx context.Context, snapshotPath string, key string) (*UploadResult, error) {
	f, err := os.Open(snapshotPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open snapshot file %q: %w", snapshotPath, err)
	}
	defer f.Close()

	input := &s3.PutObjectInput{
		Bucket:      aws.String(u.bucket),
		Key:         aws.String(key),
		Body:        f,
		IfNoneMatch: aws.String("*"),
	}

	if u.kmsKeyARN != "" {
		input.ServerSideEncryption = types.ServerSideEncryptionAwsKms
		input.SSEKMSKeyId = aws.String(u.kmsKeyARN)
	}

	if _, err := u.client.PutObject(ctx, input); err != nil {
		return nil, fmt.Errorf("failed to upload to s3://%s/%s: %w", u.bucket, key, err)
	}

	return &UploadResult{
		URL: fmt.Sprintf("s3://%s/%s", u.bucket, key),
	}, nil
}
