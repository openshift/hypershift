package azure

import (
	"context"
	"fmt"
	"maps"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/capabilities"
	"github.com/openshift/hypershift/support/upsert"
)

const placeholderClientID = "fakeClientID"

type clientIDs struct {
	ingress, azureDisk, azureFile, imageRegistry string
}

// SetupOperandCredentials configures Azure credentials for operand secrets in both managed and self-managed Azure environments
func SetupOperandCredentials(
	ctx context.Context,
	client client.Client,
	upsertProvider upsert.CreateOrUpdateProvider,
	hcp *hyperv1.HostedControlPlane,
	secretData map[string][]byte,
	isManagedAzure bool,
) []error {
	azureClientIDs := clientIDs{}
	errs := []error{}

	if isManagedAzure {
		// The ingress controller fails if this secret is not provided. The controller runs on the control plane side. In managed azure, we are
		// overriding the Azure credentials authentication method to always use client certificate authentication. This secret is just created
		// so that the ingress controller does not fail. The data in the secret is never used by the ingress controller due to the aforementioned
		// override to use client certificate authentication.
		azureClientIDs.ingress = placeholderClientID

		azureClientIDs.azureDisk = hcp.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities.DataPlane.DiskMSIClientID
		azureClientIDs.azureFile = hcp.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities.DataPlane.FileMSIClientID
		azureClientIDs.imageRegistry = hcp.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities.DataPlane.ImageRegistryMSIClientID
	} else {
		azureClientIDs.ingress = string(hcp.Spec.Platform.Azure.AzureAuthenticationConfig.WorkloadIdentities.Ingress.ClientID)
		azureClientIDs.azureDisk = string(hcp.Spec.Platform.Azure.AzureAuthenticationConfig.WorkloadIdentities.Disk.ClientID)
		azureClientIDs.azureFile = string(hcp.Spec.Platform.Azure.AzureAuthenticationConfig.WorkloadIdentities.File.ClientID)
		azureClientIDs.imageRegistry = string(hcp.Spec.Platform.Azure.AzureAuthenticationConfig.WorkloadIdentities.ImageRegistry.ClientID)
	}

	// Skip this step if the user explicitly disabled ingress.
	if capabilities.IsIngressCapabilityEnabled(hcp.Spec.Capabilities) {
		ingress := manifests.AzureIngressCloudCredsSecret()
		if _, err := upsertProvider.CreateOrUpdate(ctx, client, ingress, func() error {
			data, err := cloneAndSetClientID(secretData, "ingress", azureClientIDs.ingress)
			if err != nil {
				return err
			}

			ingress.Data = data
			return nil
		}); err != nil {
			errs = append(errs, fmt.Errorf("failed to reconcile guest cluster ingress operator secret: %w", err))
		}
	}

	// Skip this step if the user explicitly disabled image registry.
	if capabilities.IsImageRegistryCapabilityEnabled(hcp.Spec.Capabilities) {
		imageRegistrySecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "openshift-image-registry", Name: "installer-cloud-credentials"}}
		if _, err := upsertProvider.CreateOrUpdate(ctx, client, imageRegistrySecret, func() error {
			data, err := cloneAndSetClientID(secretData, "imageRegistry", azureClientIDs.imageRegistry)
			if err != nil {
				return err
			}

			imageRegistrySecret.Data = data
			return nil
		}); err != nil {
			errs = append(errs, fmt.Errorf("failed to reconcile guest cluster image-registry secret: %w", err))
		}
	}

	azureDiskCSISecret := manifests.AzureDiskCSICloudCredsSecret()
	if _, err := upsertProvider.CreateOrUpdate(ctx, client, azureDiskCSISecret, func() error {
		data, err := cloneAndSetClientID(secretData, "azureDisk", azureClientIDs.azureDisk)
		if err != nil {
			return err
		}

		azureDiskCSISecret.Data = data
		return nil
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile guest cluster CSI secret: %w", err))
	}

	azureFileCSISecret := manifests.AzureFileCSICloudCredsSecret()
	if _, err := upsertProvider.CreateOrUpdate(ctx, client, azureFileCSISecret, func() error {
		data, err := cloneAndSetClientID(secretData, "azureFile", azureClientIDs.azureFile)
		if err != nil {
			return err
		}

		azureFileCSISecret.Data = data
		return nil
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile csi driver secret: %w", err))
	}

	return errs
}

// cloneAndSetClientID clones the base map and sets the azure_client_id key to the provided clientID
// The function returns an error if the clientID is empty
func cloneAndSetClientID(base map[string][]byte, name, clientID string) (map[string][]byte, error) {
	if clientID == "" {
		return nil, fmt.Errorf("%s workload identity ClientID is unset", name)
	}
	out := maps.Clone(base) // shallow copy
	out["azure_client_id"] = []byte(clientID)
	return out, nil
}
