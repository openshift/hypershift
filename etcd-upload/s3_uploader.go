//go:generate ../hack/tools/bin/mockgen -source=s3_uploader.go -package=etcdupload -destination=s3_uploader_mock.go

package etcdupload

import (
	"context"
	"fmt"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	tmtypes "github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// S3Uploader uploads etcd snapshots to AWS S3 using the transfer manager.
type S3Uploader struct {
	bucket    string
	region    string
	kmsKeyARN string
	client    S3TransferAPI
}

// S3TransferAPI defines the transfer manager interface used by the uploader.
type S3TransferAPI interface {
	UploadObject(ctx context.Context, input *transfermanager.UploadObjectInput, opts ...func(*transfermanager.Options)) (*transfermanager.UploadObjectOutput, error)
}

// NewS3Uploader creates a new S3Uploader.
// If credentialsFile is empty, it falls back to the default AWS credential chain
// (environment variables, shared config, EC2 instance profile, IRSA, etc.).
func NewS3Uploader(ctx context.Context, bucket, region, credentialsFile, kmsKeyARN string) (*S3Uploader, error) {
	if bucket == "" {
		return nil, fmt.Errorf("--bucket is required for S3 storage type")
	}
	if region == "" {
		return nil, fmt.Errorf("--region is required for S3 storage type")
	}

	var configOpts []func(*config.LoadOptions) error
	configOpts = append(configOpts, config.WithRegion(region))
	if credentialsFile != "" {
		configOpts = append(configOpts, config.WithSharedCredentialsFiles([]string{credentialsFile}))
	}

	cfg, err := config.LoadDefaultConfig(ctx, configOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	s3Client := s3.NewFromConfig(cfg)
	return &S3Uploader{
		bucket:    bucket,
		region:    region,
		kmsKeyARN: kmsKeyARN,
		client:    transfermanager.New(s3Client),
	}, nil
}

// Upload uploads a snapshot file to S3 with optional SSE-KMS encryption.
// Uses the transfer manager for automatic multipart upload of large files.
func (u *S3Uploader) Upload(ctx context.Context, snapshotPath string, key string) (*UploadResult, error) {
	f, err := os.Open(snapshotPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open snapshot file %q: %w", snapshotPath, err)
	}
	defer f.Close()

	input := &transfermanager.UploadObjectInput{
		Bucket: aws.String(u.bucket),
		Key:    aws.String(key),
		Body:   f,
	}

	if u.kmsKeyARN != "" {
		input.ServerSideEncryption = tmtypes.ServerSideEncryptionAwsKms
		input.SSEKMSKeyID = aws.String(u.kmsKeyARN)
	}

	if _, err := u.client.UploadObject(ctx, input); err != nil {
		return nil, fmt.Errorf("failed to upload to s3://%s/%s: %w", u.bucket, key, err)
	}

	return &UploadResult{
		URL: fmt.Sprintf("s3://%s/%s", u.bucket, key),
	}, nil
}
