package etcdupload

import (
	"context"
	"fmt"
	"os"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
)

type mockAzureBlobClient struct {
	uploadFileFn func(ctx context.Context, containerName string, blobName string, file *os.File, o *azblob.UploadFileOptions) (azblob.UploadFileResponse, error)
	lastOpts     *azblob.UploadFileOptions
}

func (m *mockAzureBlobClient) UploadFile(ctx context.Context, containerName string, blobName string, file *os.File, o *azblob.UploadFileOptions) (azblob.UploadFileResponse, error) {
	m.lastOpts = o
	if m.uploadFileFn != nil {
		return m.uploadFileFn(ctx, containerName, blobName, file, o)
	}
	return azblob.UploadFileResponse{}, nil
}

func TestAzureBlobUploader(t *testing.T) {
	t.Run("When uploading successfully it should return the correct Azure Blob URL", func(t *testing.T) {
		g := NewGomegaWithT(t)
		mock := &mockAzureBlobClient{}
		uploader := newAzureBlobUploaderWithClient("my-container", "mystorageaccount", "", mock)
		snapshotPath := createTempSnapshot(t)

		result, err := uploader.Upload(context.Background(), snapshotPath, "backups/12345.db")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result.URL).To(Equal("https://mystorageaccount.blob.core.windows.net/my-container/backups/12345.db"))
	})

	t.Run("When uploading it should set IfNoneMatch for conditional write", func(t *testing.T) {
		g := NewGomegaWithT(t)
		mock := &mockAzureBlobClient{}
		uploader := newAzureBlobUploaderWithClient("my-container", "mystorageaccount", "", mock)
		snapshotPath := createTempSnapshot(t)

		_, err := uploader.Upload(context.Background(), snapshotPath, "backups/12345.db")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(mock.lastOpts.AccessConditions).ToNot(BeNil())
		g.Expect(mock.lastOpts.AccessConditions.ModifiedAccessConditions).ToNot(BeNil())
		g.Expect(mock.lastOpts.AccessConditions.ModifiedAccessConditions.IfNoneMatch).ToNot(BeNil())
	})

	t.Run("When encryption scope is provided it should set CPKScopeInfo", func(t *testing.T) {
		g := NewGomegaWithT(t)
		mock := &mockAzureBlobClient{}
		encryptionScope := "my-encryption-scope"
		uploader := newAzureBlobUploaderWithClient("my-container", "mystorageaccount", encryptionScope, mock)
		snapshotPath := createTempSnapshot(t)

		_, err := uploader.Upload(context.Background(), snapshotPath, "backups/12345.db")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(mock.lastOpts.CPKScopeInfo).ToNot(BeNil())
		g.Expect(mock.lastOpts.CPKScopeInfo.EncryptionScope).ToNot(BeNil())
		g.Expect(*mock.lastOpts.CPKScopeInfo.EncryptionScope).To(Equal(encryptionScope))
	})

	t.Run("When no encryption scope is provided it should not set CPKScopeInfo", func(t *testing.T) {
		g := NewGomegaWithT(t)
		mock := &mockAzureBlobClient{}
		uploader := newAzureBlobUploaderWithClient("my-container", "mystorageaccount", "", mock)
		snapshotPath := createTempSnapshot(t)

		_, err := uploader.Upload(context.Background(), snapshotPath, "backups/12345.db")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(mock.lastOpts.CPKScopeInfo).To(BeNil())
	})

	t.Run("When blob already exists it should return condition not met error", func(t *testing.T) {
		g := NewGomegaWithT(t)
		mock := &mockAzureBlobClient{
			uploadFileFn: func(ctx context.Context, containerName string, blobName string, file *os.File, o *azblob.UploadFileOptions) (azblob.UploadFileResponse, error) {
				return azblob.UploadFileResponse{}, fmt.Errorf("ConditionNotMet: The condition specified using HTTP conditional header(s) is not met")
			},
		}
		uploader := newAzureBlobUploaderWithClient("my-container", "mystorageaccount", "", mock)
		snapshotPath := createTempSnapshot(t)

		_, err := uploader.Upload(context.Background(), snapshotPath, "backups/12345.db")
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("ConditionNotMet"))
	})

	t.Run("When snapshot file does not exist it should return an error", func(t *testing.T) {
		g := NewGomegaWithT(t)
		mock := &mockAzureBlobClient{}
		uploader := newAzureBlobUploaderWithClient("my-container", "mystorageaccount", "", mock)

		_, err := uploader.Upload(context.Background(), "/nonexistent/snapshot.db", "backups/12345.db")
		g.Expect(err).To(HaveOccurred())
	})
}
