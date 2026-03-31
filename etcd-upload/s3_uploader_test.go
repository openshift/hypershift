package etcdupload

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	tmtypes "github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager/types"

	"go.uber.org/mock/gomock"
)

func createTempSnapshot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot.db")
	if err := os.WriteFile(path, []byte("fake-etcd-snapshot-data"), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func newTestS3Uploader(bucket, region, kmsKeyARN string, client S3TransferAPI) *S3Uploader {
	return &S3Uploader{
		bucket:    bucket,
		region:    region,
		kmsKeyARN: kmsKeyARN,
		client:    client,
	}
}

func TestS3Uploader(t *testing.T) {
	t.Run("When uploading successfully it should return the correct S3 URL", func(t *testing.T) {
		g := NewGomegaWithT(t)
		ctrl := gomock.NewController(t)
		mock := NewMockS3TransferAPI(ctrl)
		mock.EXPECT().PutObject(gomock.Any(), gomock.Any()).Return(&transfermanager.PutObjectOutput{}, nil)

		uploader := newTestS3Uploader("my-bucket", "us-east-1", "", mock)
		snapshotPath := createTempSnapshot(t)

		result, err := uploader.Upload(context.Background(), snapshotPath, "backups/12345.db")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result.URL).To(Equal("s3://my-bucket/backups/12345.db"))
	})

	t.Run("When KMS key ARN is provided it should set SSE-KMS encryption", func(t *testing.T) {
		g := NewGomegaWithT(t)
		ctrl := gomock.NewController(t)
		kmsARN := "arn:aws:kms:us-east-1:123456789012:key/test-key-id"
		mock := NewMockS3TransferAPI(ctrl)
		mock.EXPECT().PutObject(gomock.Any(), gomock.Any()).DoAndReturn(
			func(ctx context.Context, input *transfermanager.PutObjectInput, opts ...func(*transfermanager.Options)) (*transfermanager.PutObjectOutput, error) {
				g.Expect(string(input.ServerSideEncryption)).To(Equal(string(tmtypes.ServerSideEncryptionAwsKms)))
				g.Expect(input.SSEKMSKeyID).To(Equal(kmsARN))
				return &transfermanager.PutObjectOutput{}, nil
			},
		)

		uploader := newTestS3Uploader("my-bucket", "us-east-1", kmsARN, mock)
		snapshotPath := createTempSnapshot(t)

		_, err := uploader.Upload(context.Background(), snapshotPath, "backups/12345.db")
		g.Expect(err).ToNot(HaveOccurred())
	})

	t.Run("When no KMS key is provided it should not set SSE encryption", func(t *testing.T) {
		g := NewGomegaWithT(t)
		ctrl := gomock.NewController(t)
		mock := NewMockS3TransferAPI(ctrl)
		mock.EXPECT().PutObject(gomock.Any(), gomock.Any()).DoAndReturn(
			func(ctx context.Context, input *transfermanager.PutObjectInput, opts ...func(*transfermanager.Options)) (*transfermanager.PutObjectOutput, error) {
				g.Expect(string(input.ServerSideEncryption)).To(BeEmpty())
				g.Expect(input.SSEKMSKeyID).To(BeEmpty())
				return &transfermanager.PutObjectOutput{}, nil
			},
		)

		uploader := newTestS3Uploader("my-bucket", "us-east-1", "", mock)
		snapshotPath := createTempSnapshot(t)

		_, err := uploader.Upload(context.Background(), snapshotPath, "backups/12345.db")
		g.Expect(err).ToNot(HaveOccurred())
	})

	t.Run("When upload fails it should return the error", func(t *testing.T) {
		g := NewGomegaWithT(t)
		ctrl := gomock.NewController(t)
		mock := NewMockS3TransferAPI(ctrl)
		mock.EXPECT().PutObject(gomock.Any(), gomock.Any()).Return(nil,
			fmt.Errorf("upload failed: network error"),
		)

		uploader := newTestS3Uploader("my-bucket", "us-east-1", "", mock)
		snapshotPath := createTempSnapshot(t)

		_, err := uploader.Upload(context.Background(), snapshotPath, "backups/12345.db")
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("network error"))
	})

	t.Run("When snapshot file does not exist it should return an error", func(t *testing.T) {
		g := NewGomegaWithT(t)
		ctrl := gomock.NewController(t)
		mock := NewMockS3TransferAPI(ctrl)

		uploader := newTestS3Uploader("my-bucket", "us-east-1", "", mock)

		_, err := uploader.Upload(context.Background(), "/nonexistent/snapshot.db", "backups/12345.db")
		g.Expect(err).To(HaveOccurred())
	})
}
