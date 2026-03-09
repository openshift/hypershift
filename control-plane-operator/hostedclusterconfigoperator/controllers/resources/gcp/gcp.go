package gcp

import (
	"context"
	"encoding/json"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	"github.com/openshift/hypershift/support/capabilities"
	"github.com/openshift/hypershift/support/upsert"

	corev1 "k8s.io/api/core/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// WIF credential JSON structs — same format as HO's gcp.go, duplicated because that code is in an internal package.
type gcpCredentialSource struct {
	File   string                    `json:"file"`
	Format gcpCredentialSourceFormat `json:"format"`
}

type gcpCredentialSourceFormat struct {
	Type string `json:"type"`
}

type gcpExternalAccountCredential struct {
	Type                           string              `json:"type"`
	Audience                       string              `json:"audience"`
	SubjectTokenType               string              `json:"subject_token_type"`
	TokenURL                       string              `json:"token_url"`
	ServiceAccountImpersonationURL string              `json:"service_account_impersonation_url"`
	CredentialSource               gcpCredentialSource `json:"credential_source"`
}

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

		credentialJSON, err := buildGCPWorkloadIdentityCredentials(hcp.Spec.Platform.GCP.WorkloadIdentity, cfg.serviceAccountEmail)
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

func buildGCPWorkloadIdentityCredentials(wif hyperv1.GCPWorkloadIdentityConfig, serviceAccountEmail string) (string, error) {
	if wif.ProjectNumber == "" {
		return "", fmt.Errorf("project number cannot be empty in GCP Workload Identity Federation credentials")
	}
	if wif.PoolID == "" {
		return "", fmt.Errorf("pool ID cannot be empty in GCP Workload Identity Federation credentials")
	}
	if wif.ProviderID == "" {
		return "", fmt.Errorf("provider ID cannot be empty in GCP Workload Identity Federation credentials")
	}
	if serviceAccountEmail == "" {
		return "", fmt.Errorf("service account email cannot be empty in GCP Workload Identity Federation credentials")
	}

	credConfig := gcpExternalAccountCredential{
		Type:                           "external_account",
		Audience:                       fmt.Sprintf("//iam.googleapis.com/projects/%s/locations/global/workloadIdentityPools/%s/providers/%s", wif.ProjectNumber, wif.PoolID, wif.ProviderID),
		SubjectTokenType:               "urn:ietf:params:oauth:token-type:jwt",
		TokenURL:                       "https://sts.googleapis.com/v1/token",
		ServiceAccountImpersonationURL: fmt.Sprintf("https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/%s:generateAccessToken", serviceAccountEmail),
		CredentialSource: gcpCredentialSource{
			File: "/var/run/secrets/openshift/serviceaccount/token",
			Format: gcpCredentialSourceFormat{
				Type: "text",
			},
		},
	}

	credentialJSON, err := json.Marshal(credConfig)
	if err != nil {
		return "", fmt.Errorf("failed to marshal GCP credential configuration: %w", err)
	}

	return string(credentialJSON), nil
}
