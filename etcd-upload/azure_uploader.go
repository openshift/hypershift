package etcdupload

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	"github.com/Azure/msi-dataplane/pkg/dataplane"
	"github.com/openshift/hypershift/support/azureutil"
)

const (
	// AuthTypeClientSecret uses a JSON file with client ID, secret, and tenant ID.
	AuthTypeClientSecret = "client-secret"
	// AuthTypeManagedIdentity uses msi-dataplane with a certificate file mounted
	// via CSI SecretProviderClass (ARO HCP).
	AuthTypeManagedIdentity = "managed-identity"
)

// AzureBlobUploader uploads etcd snapshots to Azure Blob Storage.
type AzureBlobUploader struct {
	container       string
	serviceURL      string
	encryptionScope string
	client          AzureBlobUploadAPI
}

// AzureBlobUploadAPI defines the Azure Blob client interface used by the uploader.
type AzureBlobUploadAPI interface {
	UploadFile(ctx context.Context, containerName string, blobName string, file *os.File, o *azblob.UploadFileOptions) (azblob.UploadFileResponse, error)
}

// azureCredentialsFile represents the Azure credentials JSON file format.
type azureCredentialsFile struct {
	SubscriptionID string `json:"subscriptionId"`
	TenantID       string `json:"tenantId"`
	ClientID       string `json:"clientId"`
	ClientSecret   string `json:"clientSecret"`
}

// NewAzureBlobUploader creates a new AzureBlobUploader.
// authType controls how credentials are loaded:
//   - "client-secret": reads a JSON file with clientId/clientSecret/tenantId (default)
//   - "managed-identity": uses msi-dataplane with a certificate file from CSI mount (ARO HCP)
//
// If credentialsFile is empty, falls back to DefaultAzureCredential regardless of authType.
func NewAzureBlobUploader(ctx context.Context, container, storageAccount, credentialsFile, encryptionScope, authType, cloudName string) (*AzureBlobUploader, error) {
	if container == "" {
		return nil, fmt.Errorf("--container is required for AzureBlob storage type")
	}
	if storageAccount == "" {
		return nil, fmt.Errorf("--storage-account is required for AzureBlob storage type")
	}

	cloudConfig, err := azureutil.GetAzureCloudConfiguration(cloudName)
	if err != nil {
		return nil, fmt.Errorf("failed to get Azure cloud configuration: %w", err)
	}
	cred, err := newAzureCredential(ctx, credentialsFile, authType, cloudConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to load Azure credentials: %w", err)
	}

	serviceURL, err := azureBlobServiceURL(storageAccount, cloudName)
	if err != nil {
		return nil, err
	}
	client, err := azblob.NewClient(serviceURL, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure Blob client: %w", err)
	}

	return &AzureBlobUploader{
		container:       container,
		serviceURL:      serviceURL,
		encryptionScope: encryptionScope,
		client:          client,
	}, nil
}

func azureBlobServiceURL(storageAccount, cloudName string) (string, error) {
	blobDNSSuffix, err := azureutil.GetAzureBlobDNSSuffix(cloudName)
	if err != nil {
		return "", fmt.Errorf("failed to get Azure Blob DNS suffix: %w", err)
	}
	return fmt.Sprintf("https://%s.%s", storageAccount, blobDNSSuffix), nil
}

// Upload uploads a snapshot file to Azure Blob Storage with conditional write and optional CMK encryption.
func (u *AzureBlobUploader) Upload(ctx context.Context, snapshotPath string, key string) (*UploadResult, error) {
	f, err := os.Open(snapshotPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open snapshot file %q: %w", snapshotPath, err)
	}
	defer f.Close()

	opts := &azblob.UploadFileOptions{
		BlockSize: 4 * 1024 * 1024, // 4 MiB blocks
		AccessConditions: &blob.AccessConditions{
			ModifiedAccessConditions: &blob.ModifiedAccessConditions{
				IfNoneMatch: etagPtr(azcore.ETagAny),
			},
		},
	}

	if u.encryptionScope != "" {
		opts.CPKScopeInfo = &blob.CPKScopeInfo{
			EncryptionScope: &u.encryptionScope,
		}
	}

	if _, err := u.client.UploadFile(ctx, u.container, key, f, opts); err != nil {
		return nil, fmt.Errorf("failed to upload to Azure Blob %s/%s: %w", u.container, key, err)
	}

	url := fmt.Sprintf("%s/%s/%s", u.serviceURL, u.container, key)
	return &UploadResult{URL: url}, nil
}

// newAzureCredential returns a TokenCredential based on the provided credentials file and auth type.
// If credentialsFile is empty, it uses DefaultAzureCredential regardless of authType.
// If credentialsFile is provided:
//   - authType "client-secret": reads a JSON file with clientId/clientSecret/tenantId
//   - authType "managed-identity": uses msi-dataplane to load a certificate credential (ARO HCP)
func newAzureCredential(ctx context.Context, credentialsFile, authType string, cloudConfig cloud.Configuration) (azcore.TokenCredential, error) {
	if credentialsFile == "" {
		cred, err := azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{
			ClientOptions: azcore.ClientOptions{Cloud: cloudConfig},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create default Azure credential: %w", err)
		}
		return cred, nil
	}

	switch authType {
	case AuthTypeManagedIdentity:
		return newManagedIdentityCredential(ctx, credentialsFile)
	case AuthTypeClientSecret, "":
		return newClientSecretCredential(credentialsFile, cloudConfig)
	default:
		return nil, fmt.Errorf("unsupported auth type: %q (must be %q or %q)", authType, AuthTypeClientSecret, AuthTypeManagedIdentity)
	}
}

// newClientSecretCredential reads a JSON file with clientId/clientSecret/tenantId
// and returns a ClientSecretCredential.
func newClientSecretCredential(credentialsFile string, cloudConfig cloud.Configuration) (azcore.TokenCredential, error) {
	data, err := os.ReadFile(credentialsFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read credentials file %q: %w", credentialsFile, err)
	}

	var creds azureCredentialsFile
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, fmt.Errorf("failed to parse credentials file: %w", err)
	}

	credential, err := azidentity.NewClientSecretCredential(creds.TenantID, creds.ClientID, creds.ClientSecret, &azidentity.ClientSecretCredentialOptions{
		ClientOptions: azcore.ClientOptions{Cloud: cloudConfig},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure credential: %w", err)
	}

	return credential, nil
}

// newManagedIdentityCredential loads a UserAssignedIdentityCredentials file
// (certificate-based, mounted via CSI SecretProviderClass) and returns
// a TokenCredential using the msi-dataplane library. This is the auth path
// used by ARO HCP.
func newManagedIdentityCredential(ctx context.Context, credentialsFile string) (azcore.TokenCredential, error) {
	cred, err := dataplane.NewUserAssignedIdentityCredential(ctx, credentialsFile)
	if err != nil {
		return nil, fmt.Errorf("failed to create managed identity credential from %q: %w", credentialsFile, err)
	}
	return cred, nil
}

func etagPtr(e azcore.ETag) *azcore.ETag {
	return &e
}

// newAzureBlobUploaderWithClient creates an AzureBlobUploader with a provided client (for testing).
func newAzureBlobUploaderWithClient(container, serviceURL, encryptionScope string, client AzureBlobUploadAPI) *AzureBlobUploader {
	return &AzureBlobUploader{
		container:       container,
		serviceURL:      serviceURL,
		encryptionScope: encryptionScope,
		client:          client,
	}
}
