package azure

import (
	"context"
	"fmt"
	"maps"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	"github.com/openshift/hypershift/support/capabilities"
	"github.com/openshift/hypershift/support/upsert"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const placeholderClientID = "fakeClientID"

type clientIDs struct {
	ingress, azureDisk, azureFile, imageRegistry string
}

// secretConfig defines the configuration for an Azure operand secret, including its name, how to generate its manifest,
// which key and value to use for the client ID, an optional capability check, and an error context for reporting.
type secretConfig struct {
	name           string
	manifestFunc   func() *corev1.Secret
	clientIDKey    string
	clientID       string
	capabilityFunc func(*hyperv1.Capabilities) bool
	errorContext   string
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

	configs := []secretConfig{
		{
			name:           "ingress",
			manifestFunc:   manifests.AzureIngressCloudCredsSecret,
			clientIDKey:    "ingress",
			clientID:       azureClientIDs.ingress,
			capabilityFunc: capabilities.IsIngressCapabilityEnabled,
			errorContext:   "guest cluster ingress operator secret",
		},
		{
			name:           "imageRegistry",
			manifestFunc:   manifests.AzureImageRegistryCloudCredsSecret,
			clientIDKey:    "imageRegistry",
			clientID:       azureClientIDs.imageRegistry,
			capabilityFunc: capabilities.IsImageRegistryCapabilityEnabled,
			errorContext:   "guest cluster image-registry secret",
		},
		{
			name:           "azureDisk",
			manifestFunc:   manifests.AzureDiskCSICloudCredsSecret,
			clientIDKey:    "azureDisk",
			clientID:       azureClientIDs.azureDisk,
			capabilityFunc: nil, // Always enabled
			errorContext:   "guest cluster CSI secret",
		},
		{
			name:           "azureFile",
			manifestFunc:   manifests.AzureFileCSICloudCredsSecret,
			clientIDKey:    "azureFile",
			clientID:       azureClientIDs.azureFile,
			capabilityFunc: nil, // Always enabled
			errorContext:   "CSI driver secret",
		},
	}

	var errs []error
	for _, config := range configs {
		if err := reconcileAzureSecret(ctx, client, upsertProvider, secretData, config, hcp.Spec.Capabilities); err != nil {
			errs = append(errs, err)
		}
	}

	return errs
}

// reconcileAzureSecret ensures that a specific Azure operand secret is created or updated in the guest cluster.
// It checks if the relevant capability is enabled (if a capability check is provided), clones the base secret data
// and sets the appropriate Azure client ID, and then uses the upsert provider to create or update the secret resource.
//
// Parameters:
//   - ctx: Context for controlling cancellation and deadlines.
//   - client: Kubernetes client for interacting with the cluster.
//   - upsertProvider: Provider for create-or-update operations on resources.
//   - secretData: The base secret data to clone and modify for the secret.
//   - config: The configuration for the specific Azure operand secret (name, manifest, client ID, etc.).
//   - capabilities: The set of enabled capabilities for the HostedControlPlane.
//
// Returns:
//   - error: An error if reconciliation fails, or nil if successful or if the capability is not enabled.
func reconcileAzureSecret(ctx context.Context, client client.Client, upsertProvider upsert.CreateOrUpdateProvider, 
	secretData map[string][]byte, config secretConfig, capabilities *hyperv1.Capabilities) error {

	if config.capabilityFunc != nil && !config.capabilityFunc(capabilities) {
		return nil
	}

	secret := config.manifestFunc()
	_, err := upsertProvider.CreateOrUpdate(ctx, client, secret, func() error {
		data, err := cloneAndSetClientID(secretData, config.clientIDKey, config.clientID)
		if err != nil {
			return fmt.Errorf("failed to clone and set client ID for %s: %w", config.name, err)
		}
		secret.Data = data
		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to reconcile %s: %w", config.errorContext, err)
	}

	return nil
}
