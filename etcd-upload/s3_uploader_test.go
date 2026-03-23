package etcdupload

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type mockS3Client struct {
	putObjectFn func(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	lastInput   *s3.PutObjectInput
}

func (m *mockS3Client) PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	m.lastInput = params
	if m.putObjectFn != nil {
		return m.putObjectFn(ctx, params, optFns...)
	}
	return &s3.PutObjectOutput{}, nil
}

func newTestS3Uploader(bucket, region, kmsKeyARN string, client S3PutObjectAPI) *S3Uploader {
	return &S3Uploader{
		bucket:    bucket,
		region:    region,
		kmsKeyARN: kmsKeyARN,
		client:    client,
	}
}

func createTempSnapshot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot.db")
	if err := os.WriteFile(path, []byte("fake-etcd-snapshot-data"), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestS3Uploader(t *testing.T) {
	t.Run("When uploading successfully it should return the correct S3 URL", func(t *testing.T) {
		g := NewGomegaWithT(t)
		mock := &mockS3Client{}
		uploader := newTestS3Uploader("my-bucket", "us-east-1", "", mock)
		snapshotPath := createTempSnapshot(t)

		result, err := uploader.Upload(context.Background(), snapshotPath, "backups/12345.db")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result.URL).To(Equal("s3://my-bucket/backups/12345.db"))
	})

	t.Run("When uploading it should always set IfNoneMatch header", func(t *testing.T) {
		g := NewGomegaWithT(t)
		mock := &mockS3Client{}
		uploader := newTestS3Uploader("my-bucket", "us-east-1", "", mock)
		snapshotPath := createTempSnapshot(t)

		_, err := uploader.Upload(context.Background(), snapshotPath, "backups/12345.db")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(mock.lastInput.IfNoneMatch).ToNot(BeNil())
		g.Expect(*mock.lastInput.IfNoneMatch).To(Equal("*"))
	})

	t.Run("When KMS key ARN is provided it should set SSE-KMS encryption", func(t *testing.T) {
		g := NewGomegaWithT(t)
		mock := &mockS3Client{}
		kmsARN := "arn:aws:kms:us-east-1:123456789012:key/test-key-id"
		uploader := newTestS3Uploader("my-bucket", "us-east-1", kmsARN, mock)
		snapshotPath := createTempSnapshot(t)

		_, err := uploader.Upload(context.Background(), snapshotPath, "backups/12345.db")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(mock.lastInput.ServerSideEncryption).To(Equal(types.ServerSideEncryptionAwsKms))
		g.Expect(mock.lastInput.SSEKMSKeyId).ToNot(BeNil())
		g.Expect(*mock.lastInput.SSEKMSKeyId).To(Equal(kmsARN))
	})

	t.Run("When no KMS key is provided it should not set SSE encryption", func(t *testing.T) {
		g := NewGomegaWithT(t)
		mock := &mockS3Client{}
		uploader := newTestS3Uploader("my-bucket", "us-east-1", "", mock)
		snapshotPath := createTempSnapshot(t)

		_, err := uploader.Upload(context.Background(), snapshotPath, "backups/12345.db")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(string(mock.lastInput.ServerSideEncryption)).To(BeEmpty())
	})

	t.Run("When object already exists it should return precondition failed error", func(t *testing.T) {
		g := NewGomegaWithT(t)
		mock := &mockS3Client{
			putObjectFn: func(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
				return nil, fmt.Errorf("PreconditionFailed: At least one of the pre-conditions you specified did not hold")
			},
		}
		uploader := newTestS3Uploader("my-bucket", "us-east-1", "", mock)
		snapshotPath := createTempSnapshot(t)

		_, err := uploader.Upload(context.Background(), snapshotPath, "backups/12345.db")
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("PreconditionFailed"))
	})

	t.Run("When snapshot file does not exist it should return an error", func(t *testing.T) {
		g := NewGomegaWithT(t)
		mock := &mockS3Client{}
		uploader := newTestS3Uploader("my-bucket", "us-east-1", "", mock)

		_, err := uploader.Upload(context.Background(), "/nonexistent/snapshot.db", "backups/12345.db")
		g.Expect(err).To(HaveOccurred())
	})
}
