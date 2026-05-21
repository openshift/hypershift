package gcputil

import (
	"encoding/json"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

// CredentialSource represents the credential source configuration for GCP external account credentials.
type CredentialSource struct {
	File   string                 `json:"file"`
	Format CredentialSourceFormat `json:"format"`
}

// CredentialSourceFormat represents the format of the credential source.
type CredentialSourceFormat struct {
	Type string `json:"type"`
}

// ExternalAccountCredential represents the complete GCP external account credential configuration
// for Workload Identity Federation. This follows the Google Cloud credential configuration format.
type ExternalAccountCredential struct {
	Type                           string           `json:"type"`
	Audience                       string           `json:"audience"`
	SubjectTokenType               string           `json:"subject_token_type"`
	TokenURL                       string           `json:"token_url"`
	ServiceAccountImpersonationURL string           `json:"service_account_impersonation_url"`
	CredentialSource               CredentialSource `json:"credential_source"`
}

// BuildWorkloadIdentityCredentials creates the credential configuration JSON for Google Cloud SDK
// to use Workload Identity Federation with a specific service account email.
func BuildWorkloadIdentityCredentials(wif hyperv1.GCPWorkloadIdentityConfig, serviceAccountEmail string) (string, error) {
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

	credConfig := ExternalAccountCredential{
		Type:                           "external_account",
		Audience:                       fmt.Sprintf("//iam.googleapis.com/projects/%s/locations/global/workloadIdentityPools/%s/providers/%s", wif.ProjectNumber, wif.PoolID, wif.ProviderID),
		SubjectTokenType:               "urn:ietf:params:oauth:token-type:jwt",
		TokenURL:                       "https://sts.googleapis.com/v1/token",
		ServiceAccountImpersonationURL: fmt.Sprintf("https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/%s:generateAccessToken", serviceAccountEmail),
		CredentialSource: CredentialSource{
			File: "/var/run/secrets/openshift/serviceaccount/token",
			Format: CredentialSourceFormat{
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
