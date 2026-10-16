package gcp

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	"github.com/openshift/hypershift/support/capabilities"
	"github.com/openshift/hypershift/support/gcputil"
	"github.com/openshift/hypershift/support/upsert"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// defaultCredentialSecretKey is the data key OpenShift's GCP-facing operators
// (e.g. the image-registry operator) conventionally expect a GCP credential
// secret to use.
const defaultCredentialSecretKey = "service_account.json"

// applicationDefaultCredentialsSecretKey is the data key the GCP PD CSI
// driver operator/controller expect, matching the key the HyperShift
// operator uses when it populates the equivalent control-plane-namespace
// secret (see hypershift-operator/controllers/hostedcluster/internal/platform/gcp).
// Keeping these two secrets consistent avoids the credential-key mismatch
// bug found while smoke-testing GCP PD CSI (application_default_credentials.json
// vs service_account.json).
const applicationDefaultCredentialsSecretKey = "application_default_credentials.json"

// gcpCredentialConfig defines configuration for a single credential secret.
type gcpCredentialConfig struct {
	manifestFunc        func() *corev1.Secret
	serviceAccountEmail string
	secretKey           string
	capabilityChecker   func(*hyperv1.Capabilities) bool
	errorContext        string
}

// SetupOperandCredentials ensures that the required GCP operand credential secrets are created or updated
// for the guest cluster's components based on the HostedControlPlane configuration.
func SetupOperandCredentials(
	ctx context.Context,
	c client.Client,
	upsertProvider upsert.CreateOrUpdateProvider,
	hcp *hyperv1.HostedControlPlane,
) []error {
	configs := []gcpCredentialConfig{
		{
			manifestFunc:        manifests.GCPImageRegistryCloudCredsSecret,
			serviceAccountEmail: string(hcp.Spec.Platform.GCP.WorkloadIdentity.ServiceAccountsEmails.ImageRegistry),
			secretKey:           defaultCredentialSecretKey,
			capabilityChecker:   capabilities.IsImageRegistryCapabilityEnabled,
			errorContext:        "guest cluster image-registry credential",
		},
		{
			manifestFunc:        manifests.GCPPDCSICloudCredsSecret,
			serviceAccountEmail: string(hcp.Spec.Platform.GCP.WorkloadIdentity.ServiceAccountsEmails.Storage),
			secretKey:           applicationDefaultCredentialsSecretKey,
			capabilityChecker:   nil, // Always enabled
			errorContext:        "guest cluster CSI storage credential",
		},
	}
	return reconcileGCPCredentials(ctx, c, upsertProvider, hcp, configs)
}

func reconcileGCPCredentials(
	ctx context.Context,
	c client.Client,
	upsertProvider upsert.CreateOrUpdateProvider,
	hcp *hyperv1.HostedControlPlane,
	configs []gcpCredentialConfig,
) []error {
	var errs []error

	for _, cfg := range configs {
		if cfg.capabilityChecker != nil && !cfg.capabilityChecker(hcp.Spec.Capabilities) {
			continue
		}

		if cfg.secretKey == "" {
			errs = append(errs, fmt.Errorf("missing secretKey in credential config for %s", cfg.errorContext))
			continue
		}

		secret := cfg.manifestFunc()

		ns := &corev1.Namespace{}
		if err := c.Get(ctx, client.ObjectKey{Name: secret.Namespace}, ns); err != nil {
			if apierrors.IsNotFound(err) {
				ctrl.LoggerFrom(ctx).Info("WARNING: cannot sync cloud credential secret because namespace does not exist",
					"secret", client.ObjectKeyFromObject(secret),
					"context", cfg.errorContext)
				continue
			}
			errs = append(errs, fmt.Errorf("failed to get namespace %s for %s: %w", secret.Namespace, cfg.errorContext, err))
			continue
		}

		credentialJSON, err := gcputil.BuildWorkloadIdentityCredentials(hcp.Spec.Platform.GCP.WorkloadIdentity, cfg.serviceAccountEmail)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to build %s: %w", cfg.errorContext, err))
			continue
		}

		if _, err := upsertProvider.CreateOrUpdate(ctx, c, secret, func() error {
			secret.Data = map[string][]byte{
				cfg.secretKey: []byte(credentialJSON),
			}
			secret.Type = corev1.SecretTypeOpaque
			return nil
		}); err != nil {
			errs = append(errs, fmt.Errorf("failed to reconcile %s: %w", cfg.errorContext, err))
		}
	}

	return errs
}
