package azure

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/pageblob"
	"github.com/go-logr/logr"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/ptr"
)

const (
	// ImageCopyPollInterval is how often to check image copy progress
	ImageCopyPollInterval = 5 * time.Second
	// ImageCopyTimeout is the maximum time to wait for image copy to complete
	ImageCopyTimeout = 10 * time.Minute
)

// ImageManager handles Azure image operations
type ImageManager struct {
	subscriptionID string
	creds          azcore.TokenCredential
}

// NewImageManager creates a new ImageManager
func NewImageManager(subscriptionID string, creds azcore.TokenCredential) *ImageManager {
	return &ImageManager{
		subscriptionID: subscriptionID,
		creds:          creds,
	}
}

// CreateRHCOSImages uploads the RHCOS image and creates a bootable image
func (i *ImageManager) CreateRHCOSImages(ctx context.Context, l logr.Logger, opts *CreateInfraOptions, resourceGroupName string) (string, error) {
	storageAccountClient, err := armstorage.NewAccountsClient(i.subscriptionID, i.creds, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create new accounts client for storage: %w", err)
	}

	storageAccountName := "cluster" + utilrand.String(5)
	storageAccountFuture, err := storageAccountClient.BeginCreate(ctx, resourceGroupName, storageAccountName,
		armstorage.AccountCreateParameters{
			SKU: &armstorage.SKU{
				Name: ptr.To(armstorage.SKUNamePremiumLRS),
				Tier: ptr.To(armstorage.SKUTierStandard),
			},
			Location: ptr.To(opts.Location),
		}, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create storage account: %w", err)
	}
	storageAccount, err := storageAccountFuture.PollUntilDone(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("failed waiting for storage account creation to complete: %w", err)
	}
	l.Info("Successfully created storage account", "name", *storageAccount.Name)

	blobContainersClient, err := armstorage.NewBlobContainersClient(i.subscriptionID, i.creds, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create blob containers client: %w", err)
	}

	imageContainer, err := blobContainersClient.Create(ctx, resourceGroupName, storageAccountName, "vhd", armstorage.BlobContainer{}, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create blob container: %w", err)
	}
	l.Info("Successfully created blob container", "name", *imageContainer.Name)

	sourceURL := opts.RHCOSImage
	blobName := "rhcos.x86_64.vhd"

	// Explicitly check this, Azure API makes inferring the problem from the error message extremely hard
	if !strings.HasPrefix(sourceURL, "https://rhcos.blob.core.windows.net") {
		return "", fmt.Errorf("the image source url must be from an azure blob storage, otherwise upload will fail with an `One of the request inputs is out of range` error")
	}

	// storage object access has its own authentication system: https://github.com/hashicorp/terraform-provider-azurerm/blob/b0c897055329438be6a3a159f6ffac4e1ce958f2/internal/services/storage/client/client.go#L133
	accountsClient, err := armstorage.NewAccountsClient(i.subscriptionID, i.creds, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create new accounts client: %w", err)
	}
	storageAccountKeyResult, err := accountsClient.ListKeys(ctx, resourceGroupName, storageAccountName, &armstorage.AccountsClientListKeysOptions{Expand: ptr.To("kerb")})
	if err != nil {
		return "", fmt.Errorf("failed to list storage account keys: %w", err)
	}
	if len(storageAccountKeyResult.Keys) == 0 || storageAccountKeyResult.Keys[0].Value == nil {
		return "", errors.New("no storage account keys exist")
	}

	credential, err := container.NewSharedKeyCredential(storageAccountName, *storageAccountKeyResult.Keys[0].Value)
	if err != nil {
		return "", fmt.Errorf("failed to create shared key credentials: %w", err)
	}

	imageBlobURLPrefix := fmt.Sprintf("https://%s.blob.core.windows.net/vhd/", storageAccountName)

	containerClient, err := container.NewClientWithSharedKeyCredential(imageBlobURLPrefix, credential, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create new container client: %w", err)
	}

	// VHDs should be uploaded to page blobs instead of block blobs per
	// https://learn.microsoft.com/en-us/answers/questions/792044/how-to-create-a-vm-from-vhd-file-in-azure
	pageBlobClient := containerClient.NewPageBlobClient(blobName)
	_, err = pageBlobClient.Create(ctx, 0, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create page blob for vhd: %w", err)
	}

	l.Info("Copying RHCOS image to vhd blob, this can take a few minutes...")
	err = i.copyImageAndWait(ctx, sourceURL, pageBlobClient)
	if err != nil {
		return "", err
	}

	l.Info("Successfully uploaded RHCOS image to vhd blob")
	imagesClient, err := armcompute.NewImagesClient(i.subscriptionID, i.creds, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create images client: %w", err)
	}

	imageInput := armcompute.Image{
		Properties: &armcompute.ImageProperties{
			StorageProfile: &armcompute.ImageStorageProfile{
				OSDisk: &armcompute.ImageOSDisk{
					OSType:  ptr.To(armcompute.OperatingSystemTypesLinux),
					OSState: ptr.To(armcompute.OperatingSystemStateTypesGeneralized),
					BlobURI: ptr.To(imageBlobURLPrefix + blobName),
				},
			},
			HyperVGeneration: ptr.To(armcompute.HyperVGenerationTypesV1),
		},
		Location: ptr.To(opts.Location),
	}
	imageCreationFuture, err := imagesClient.BeginCreateOrUpdate(ctx, resourceGroupName, blobName, imageInput, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create image: %w", err)
	}
	imageCreationResult, err := imageCreationFuture.PollUntilDone(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("failed to wait for image creation to finish: %w", err)
	}
	bootImageID := *imageCreationResult.ID
	l.Info("Successfully created image", "resourceID", *imageCreationResult.ID, "result", imageCreationResult)

	return bootImageID, nil
}

// copyImageAndWait copies an RHCOS image from its Azure blob URL to a page blob within the managed resource group to be
// used as the basis for creating Azure virtual machines for a NodePool.
//
// This function is hardcoded to wait 10 minutes for the copy to complete or else it will error out.
func (i *ImageManager) copyImageAndWait(ctx context.Context, rhcosURL string, pageBlobClient *pageblob.Client) error {
	_, err := pageBlobClient.CopyFromURL(ctx, rhcosURL, nil)
	if err != nil {
		return fmt.Errorf("failed to start the process to copy rhcos image to vhd blob: %w", err)
	}

	if err = wait.PollUntilContextTimeout(ctx, ImageCopyPollInterval, ImageCopyTimeout, true, func(ctx context.Context) (done bool, err error) {
		// Grab the latest status on the copy effort
		properties, err := pageBlobClient.GetProperties(ctx, nil)
		if err != nil {
			return true, fmt.Errorf("failed to check rhcos copy status: %w", err)
		}

		// This should never happen but just in case
		if properties.CopyStatus == nil {
			return true, fmt.Errorf("rhcos copy status is nil")
		}

		// Copy is complete, bail out
		if *properties.CopyStatus == blob.CopyStatusTypeSuccess {
			return true, nil
		}

		// Something went wrong with the copy process, bail out
		if *properties.CopyStatus == blob.CopyStatusTypeAborted || *properties.CopyStatus == blob.CopyStatusTypeFailed {
			return true, fmt.Errorf("failed to copy rhcos image: %w", err)
		}

		return false, nil
	}); err != nil {
		return fmt.Errorf("failed to copy and wait for rhcos image: %w", err)
	}

	return nil
}