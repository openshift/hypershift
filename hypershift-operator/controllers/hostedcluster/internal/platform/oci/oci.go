package oci

import (
	"context"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/upsert"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// OCI credentials secret keys
	credentialsConfigKey = "config"
	credentialsKeyKey    = "key"
)

type OCI struct {
	// capiProviderImage is the image for the CAPI OCI provider (future use)
	capiProviderImage string
}

func New(capiProviderImage string) *OCI {
	return &OCI{
		capiProviderImage: capiProviderImage,
	}
}

// ReconcileCAPIInfraCR reconciles the CAPI infrastructure cluster resource for OCI.
// For the MVP, this returns nil because we don't yet have CAPOCI provider types imported.
// This will be implemented in a future phase when we integrate the actual CAPI OCI provider.
func (o OCI) ReconcileCAPIInfraCR(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN,
	hcluster *hyperv1.HostedCluster, controlPlaneNamespace string, apiEndpoint hyperv1.APIEndpoint) (client.Object, error) {

	// TODO(future): Implement OCICluster CR creation
	// For now, return nil as we don't have CAPOCI types imported yet.
	// This allows the HostedCluster to reconcile without errors.
	return nil, nil
}

// ReconcileCredentials ensures OCI credentials are synced to the control plane namespace.
// The credentials secret contains OCI config and API signing key required for infrastructure operations.
func (o OCI) ReconcileCredentials(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN,
	hcluster *hyperv1.HostedCluster, controlPlaneNamespace string) error {

	if hcluster.Spec.Platform.OCI == nil {
		// No OCI platform spec, nothing to do
		return nil
	}

	// Source secret (in HostedCluster namespace)
	srcSecret := &corev1.Secret{}
	srcSecretName := hcluster.Spec.Platform.OCI.IdentityRef.Name
	if err := c.Get(ctx, client.ObjectKey{Namespace: hcluster.Namespace, Name: srcSecretName}, srcSecret); err != nil {
		return err
	}

	// Destination secret (in control plane namespace)
	destSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      "oci-credentials",
		},
	}

	// Sync credentials to control plane namespace
	_, err := createOrUpdate(ctx, c, destSecret, func() error {
		// Copy OCI credentials from source secret
		if destSecret.Data == nil {
			destSecret.Data = make(map[string][]byte)
		}
		destSecret.Data[credentialsConfigKey] = srcSecret.Data[credentialsConfigKey]
		destSecret.Data[credentialsKeyKey] = srcSecret.Data[credentialsKeyKey]
		destSecret.Type = corev1.SecretTypeOpaque
		return nil
	})

	return err
}

// DeleteCredentials cleans up OCI credentials from the control plane namespace.
func (o OCI) DeleteCredentials(ctx context.Context, c client.Client, hcluster *hyperv1.HostedCluster, controlPlaneNamespace string) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      "oci-credentials",
		},
	}
	if err := c.Delete(ctx, secret); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

// ReconcileSecretEncryption is a no-op for OCI MVP.
// Future enhancement: integrate with OCI Vault for secret encryption.
func (o OCI) ReconcileSecretEncryption(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN,
	hcluster *hyperv1.HostedCluster, controlPlaneNamespace string) error {
	// No-op for MVP
	// Future: OCI Vault integration for KMS encryption
	return nil
}

// CAPIProviderDeploymentSpec returns nil for OCI MVP.
// Future enhancement: return deployment spec for CAPOCI provider when integrated.
func (o OCI) CAPIProviderDeploymentSpec(hcluster *hyperv1.HostedCluster, hcp *hyperv1.HostedControlPlane) (*appsv1.DeploymentSpec, error) {
	// No-op for MVP - no CAPI provider deployment yet
	// Future: Deploy CAPOCI provider alongside the control plane
	return nil, nil
}

// CAPIProviderPolicyRules returns RBAC policy rules for the OCI CAPI provider.
// For MVP, returns nil as we don't have CAPOCI integration yet.
func (o OCI) CAPIProviderPolicyRules() []rbacv1.PolicyRule {
	// No additional policy rules needed for MVP
	// Future: Add RBAC rules when CAPOCI provider is integrated
	return nil
}
