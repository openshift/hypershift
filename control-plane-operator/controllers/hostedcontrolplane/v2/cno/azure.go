package cno

import (
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/secretproviderclass"
	component "github.com/openshift/hypershift/support/controlplane-component"

	secretsstorev1 "sigs.k8s.io/secrets-store-csi-driver/apis/v1"
)

func adaptAzureSecretProvider(cpContext component.WorkloadContext, secretProvider *secretsstorev1.SecretProviderClass) error {
	managedIdentity := cpContext.HCP.Spec.Platform.Azure.ManagedIdentities.ControlPlane.Network
	secretproviderclass.ReconcileManagedAzureSecretProviderClass(secretProvider, cpContext.HCP, managedIdentity.CertificateName)
	return nil
}
