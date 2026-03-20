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

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// gcpCredentialConfig defines configuration for a single credential secret.
type gcpCredentialConfig struct {
	manifestFunc        func() *corev1.Secret
	serviceAccountEmail string
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
			serviceAccountEmail: hcp.Spec.Platform.GCP.WorkloadIdentity.ServiceAccountsEmails.ImageRegistry,
			capabilityChecker:   capabilities.IsImageRegistryCapabilityEnabled,
			errorContext:        "guest cluster image-registry credential",
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

		secret := cfg.manifestFunc()

		ns := &corev1.Namespace{}
		if err := c.Get(ctx, client.ObjectKey{Name: secret.Namespace}, ns); err != nil {
			if apierrors.IsNotFound(err) {
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
				"service_account.json": []byte(credentialJSON),
			}
			secret.Type = corev1.SecretTypeOpaque
			return nil
		}); err != nil {
			errs = append(errs, fmt.Errorf("failed to reconcile %s: %w", cfg.errorContext, err))
		}
	}

	return errs
}
