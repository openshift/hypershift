package azure

import (
	"context"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	"github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/capabilities"
	"github.com/openshift/hypershift/support/upsert"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const placeholderClientID = "fakeClientID"

type clientIDs struct {
	ingress, azureDisk, azureFile, imageRegistry string
}

// SetupOperandCredentials ensures that the required Azure operand credential secrets are created or updated
// for the guest cluster's components (ingress, image registry, disk CSI, file CSI) based on the HostedControlPlane
// configuration. It determines the correct client IDs for each component, depending on whether the cluster is
// managed or self-managed, and then reconciles the secrets using the provided upsert provider. The function
// returns a slice of errors for any failures encountered during reconciliation.
//
// Parameters:
//   - ctx: The context for the operation.
//   - client: The Kubernetes client used to interact with the API server.
//   - upsertProvider: The provider used to create or update secrets.
//   - hcp: The HostedControlPlane resource containing Azure authentication configuration.
//   - secretData: The base data to include in each secret.
//   - isManagedAzure: Indicates whether the cluster is a managed Azure cluster.
//
// Returns:
//   - []error: A slice of errors encountered during secret reconciliation, or an empty slice if successful.
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

	reconcileErrs := reconcileAzureCloudCredentials(ctx, client, upsertProvider, hcp, secretData, azureClientIDs)
	errs = append(errs, reconcileErrs...)

	return errs
}

// reconcileAzureCloudCredentials ensures that the required Azure cloud credential secrets
// are created or updated for the guest cluster's components (ingress, image registry, disk CSI, file CSI).
// It determines the correct client IDs for each component, checks if the corresponding capability is enabled
// (if applicable), and then reconciles the secrets using the provided upsert provider.
// Returns a slice of errors for any failures encountered during reconciliation.
//
// Parameters:
//   - ctx: Context for controlling cancellation and deadlines.
//   - client: Kubernetes client for interacting with the cluster.
//   - upsertProvider: Provider for create-or-update operations on resources.
//   - hcp: The HostedControlPlane resource being reconciled.
//   - secretData: The base secret data to clone and modify for each secret.
//   - azureClientIDs: Struct containing the client IDs for each Azure component.
//
// Returns:
//   - []error: A slice of errors encountered during reconciliation, or empty if successful.
func reconcileAzureCloudCredentials(ctx context.Context, client client.Client, upsertProvider upsert.CreateOrUpdateProvider,
	hcp *hyperv1.HostedControlPlane, secretData map[string][]byte, azureClientIDs clientIDs) []error {

	configs := []azureutil.AzureCredentialConfig{
		{
			Name:              "ingress",
			ManifestFunc:      manifests.AzureIngressCloudCredsSecret,
			ClientID:          azureClientIDs.ingress,
			CapabilityChecker: capabilities.IsIngressCapabilityEnabled,
			ErrorContext:      "guest cluster ingress operator secret",
		},
		{
			Name:              "imageRegistry",
			ManifestFunc:      manifests.AzureImageRegistryCloudCredsSecret,
			ClientID:          azureClientIDs.imageRegistry,
			CapabilityChecker: capabilities.IsImageRegistryCapabilityEnabled,
			ErrorContext:      "guest cluster image-registry secret",
		},
		{
			Name:              "azureDisk",
			ManifestFunc:      manifests.AzureDiskCSICloudCredsSecret,
			ClientID:          azureClientIDs.azureDisk,
			CapabilityChecker: nil, // Always enabled
			ErrorContext:      "guest cluster CSI secret",
		},
		{
			Name:              "azureFile",
			ManifestFunc:      manifests.AzureFileCSICloudCredsSecret,
			ClientID:          azureClientIDs.azureFile,
			CapabilityChecker: nil, // Always enabled
			ErrorContext:      "CSI driver secret",
		},
	}

	return azureutil.ReconcileAzureCredentials(
		ctx,
		client,
		upsertProvider.CreateOrUpdate,
		secretData,
		configs,
		hcp.Spec.Capabilities,
	)
}
