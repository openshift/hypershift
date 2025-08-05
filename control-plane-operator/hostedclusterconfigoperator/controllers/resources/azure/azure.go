package azure

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/capabilities"
	"github.com/openshift/hypershift/support/upsert"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// SetupManagedCredentials configures Azure credentials for managed Azure environments (ARO-HCP)
func SetupManagedCredentials(
	ctx context.Context,
	client client.Client,
	upsertProvider upsert.CreateOrUpdateProvider,
	hcp *hyperv1.HostedControlPlane,
	secretData map[string][]byte,
) []error {
	errs := []error{}

	// The ingress controller fails if this secret is not provided. The controller runs on the control plane side. In managed azure, we are
	// overriding the Azure credentials authentication method to always use client certificate authentication. This secret is just created
	// so that the ingress controller does not fail. The data in the secret is never used by the ingress controller due to the aforementioned
	// override to use client certificate authentication.
	//
	// Skip this step if the user explicitly disabled ingress.
	if capabilities.IsIngressCapabilityEnabled(hcp.Spec.Capabilities) {
		ingressCredentialSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "openshift-ingress-operator", Name: "cloud-credentials"}}
		if _, err := upsertProvider.CreateOrUpdate(ctx, client, ingressCredentialSecret, func() error {
			secretData["azure_client_id"] = []byte("fakeClientID")
			ingressCredentialSecret.Data = secretData
			return nil
		}); err != nil {
			errs = append(errs, fmt.Errorf("failed to reconcile guest cluster ingress operator secret: %w", err))
		}
	}

	azureDiskCSISecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "openshift-cluster-csi-drivers", Name: "azure-disk-credentials"}}
	if _, err := upsertProvider.CreateOrUpdate(ctx, client, azureDiskCSISecret, func() error {
		secretData["azure_client_id"] = []byte(hcp.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities.DataPlane.DiskMSIClientID)
		azureDiskCSISecret.Data = secretData
		return nil
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile guest cluster CSI secret: %w", err))
	}

	if capabilities.IsImageRegistryCapabilityEnabled(hcp.Spec.Capabilities) {
		imageRegistrySecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "openshift-image-registry", Name: "installer-cloud-credentials"}}
		if _, err := upsertProvider.CreateOrUpdate(ctx, client, imageRegistrySecret, func() error {
			secretData["azure_client_id"] = []byte(hcp.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities.DataPlane.ImageRegistryMSIClientID)
			imageRegistrySecret.Data = secretData
			return nil
		}); err != nil {
			errs = append(errs, fmt.Errorf("failed to reconcile guest cluster image-registry secret: %w", err))
		}
	}

	azureFileCSISecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "openshift-cluster-csi-drivers", Name: "azure-file-credentials"}}
	if _, err := upsertProvider.CreateOrUpdate(ctx, client, azureFileCSISecret, func() error {
		secretData["azure_client_id"] = []byte(hcp.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities.DataPlane.FileMSIClientID)
		azureFileCSISecret.Data = secretData
		return nil
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile csi driver secret: %w", err))
	}

	return errs
}

// SetupSelfManagedCredentials configures Azure credentials for self-managed Azure environments
func SetupSelfManagedCredentials(
	ctx context.Context,
	client client.Client,
	upsertProvider upsert.CreateOrUpdateProvider,
	hcp *hyperv1.HostedControlPlane,
	secretData map[string][]byte,
) []error {
	errs := []error{}

	if capabilities.IsIngressCapabilityEnabled(hcp.Spec.Capabilities) {
		ingressCredentialSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "openshift-ingress-operator", Name: "cloud-credentials"}}
		if _, err := upsertProvider.CreateOrUpdate(ctx, client, ingressCredentialSecret, func() error {
			secretData["azure_client_id"] = []byte(hcp.Spec.Platform.Azure.AzureAuthenticationConfig.WorkloadIdentities.Ingress.ClientID)
			ingressCredentialSecret.Data = secretData
			return nil
		}); err != nil {
			errs = append(errs, fmt.Errorf("failed to reconcile guest cluster ingress operator secret: %w", err))
		}
	}

	azureDiskCSISecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "openshift-cluster-csi-drivers", Name: "azure-disk-credentials"}}
	if _, err := upsertProvider.CreateOrUpdate(ctx, client, azureDiskCSISecret, func() error {
		secretData["azure_client_id"] = []byte(hcp.Spec.Platform.Azure.AzureAuthenticationConfig.WorkloadIdentities.Disk.ClientID)
		azureDiskCSISecret.Data = secretData
		return nil
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile guest cluster CSI secret: %w", err))
	}

	if capabilities.IsImageRegistryCapabilityEnabled(hcp.Spec.Capabilities) {
		imageRegistrySecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "openshift-image-registry", Name: "installer-cloud-credentials"}}
		if _, err := upsertProvider.CreateOrUpdate(ctx, client, imageRegistrySecret, func() error {
			secretData["azure_client_id"] = []byte(hcp.Spec.Platform.Azure.AzureAuthenticationConfig.WorkloadIdentities.ImageRegistry.ClientID)
			imageRegistrySecret.Data = secretData
			return nil
		}); err != nil {
			errs = append(errs, fmt.Errorf("failed to reconcile guest cluster image-registry secret: %w", err))
		}
	}

	azureFileCSISecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "openshift-cluster-csi-drivers", Name: "azure-file-credentials"}}
	if _, err := upsertProvider.CreateOrUpdate(ctx, client, azureFileCSISecret, func() error {
		secretData["azure_client_id"] = []byte(hcp.Spec.Platform.Azure.AzureAuthenticationConfig.WorkloadIdentities.File.ClientID)
		azureFileCSISecret.Data = secretData
		return nil
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile csi driver secret: %w", err))
	}

	return errs
}
