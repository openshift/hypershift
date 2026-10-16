package registryoperator

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/secretproviderclass"

	secretsstorev1 "sigs.k8s.io/secrets-store-csi-driver/apis/v1"
)

func adaptAzureSecretProvider(cpContext component.WorkloadContext, secretProvider *secretsstorev1.SecretProviderClass) error {
	managedIdentity, err := managedAzureImageRegistryIdentity(cpContext.HCP)
	if err != nil {
		return err
	}
	secretproviderclass.ReconcileManagedAzureSecretProviderClass(secretProvider, cpContext.HCP, *managedIdentity)
	return nil
}

func managedAzureImageRegistryIdentity(hcp *hyperv1.HostedControlPlane) (*hyperv1.ManagedIdentity, error) {
	if hcp.Spec.Platform.Azure == nil || hcp.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities == nil {
		return nil, fmt.Errorf("azure managed identities are required for the image registry operator")
	}
	identity := &hcp.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities.ControlPlane.ImageRegistry
	if identity.CredentialsSecretName == "" {
		return nil, fmt.Errorf("azure image registry managed identity is required when the ImageRegistry capability is enabled")
	}
	return identity, nil
}
