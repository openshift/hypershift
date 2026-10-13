package etcdupload

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

func TestAzureCredential(t *testing.T) {
	t.Run("When auth type is managed-identity it should attempt to load msi-dataplane credential", func(t *testing.T) {
		g := NewGomegaWithT(t)

		// Create a file with valid JSON structure but invalid certificate data,
		// simulating what a CSI SecretProviderClass would mount from Azure Key Vault.
		fakeCredentials := map[string]string{
			"authenticationEndpoint": "https://login.microsoftonline.com",
			"clientId":               "fake-client-id",
			"tenantId":               "fake-tenant-id",
			"clientSecret":           "bm90LWEtdmFsaWQtY2VydA==", // base64("not-a-valid-cert")
			"notBefore":              "2026-01-01T00:00:00Z",
			"notAfter":               "2027-01-01T00:00:00Z",
		}
		data, err := json.Marshal(fakeCredentials)
		g.Expect(err).ToNot(HaveOccurred())

		credFile := filepath.Join(t.TempDir(), "managed-identity-creds.json")
		g.Expect(os.WriteFile(credFile, data, 0644)).To(Succeed())

		// msi-dataplane will parse the JSON but fail on the invalid certificate.
		// This proves the managed-identity path is reached and the file is consumed.
		_, err = newAzureCredential(context.Background(), credFile, AuthTypeManagedIdentity)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("managed identity credential"))
	})

	t.Run("When auth type is managed-identity and credentials file is missing it should return an error", func(t *testing.T) {
		g := NewGomegaWithT(t)

		_, err := newAzureCredential(context.Background(), "/nonexistent/creds.json", AuthTypeManagedIdentity)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("managed identity credential"))
	})

	t.Run("When auth type is managed-identity but credentials file is empty it should fall back to DefaultAzureCredential", func(t *testing.T) {
		// When no credentials file is provided, authType is ignored and
		// DefaultAzureCredential is used. This matches the behavior where
		// the controller doesn't pass --credentials-file.
		_, err := newAzureCredential(context.Background(), "", AuthTypeManagedIdentity)
		// DefaultAzureCredential will fail in a test environment (no Azure identity),
		// but the error should NOT mention "managed identity credential".
		if err != nil {
			g := NewGomegaWithT(t)
			g.Expect(err.Error()).To(ContainSubstring("default Azure credential"))
		}
	})

	t.Run("When auth type is client-secret it should parse client secret JSON", func(t *testing.T) {
		g := NewGomegaWithT(t)

		creds := azureCredentialsFile{
			SubscriptionID: "sub-id",
			TenantID:       "tenant-id",
			ClientID:       "client-id",
			ClientSecret:   "client-secret",
		}
		data, err := json.Marshal(creds)
		g.Expect(err).ToNot(HaveOccurred())

		credFile := filepath.Join(t.TempDir(), "client-secret-creds.json")
		g.Expect(os.WriteFile(credFile, data, 0644)).To(Succeed())

		credential, err := newAzureCredential(context.Background(), credFile, AuthTypeClientSecret)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(credential).ToNot(BeNil())
	})
}
