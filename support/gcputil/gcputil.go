package gcputil

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"sort"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// MaxResourceLabels is the maximum number of labels GCP permits on a resource.
const MaxResourceLabels = 64

// ResourceLabels converts the HCP GCP resource-label list to the map format
// expected by GCP API calls. Returns nil when no labels are configured.
func ResourceLabels(hcp *hyperv1.HostedControlPlane) map[string]string {
	if hcp.Spec.Platform.GCP == nil || len(hcp.Spec.Platform.GCP.ResourceLabels) == 0 {
		return nil
	}
	labels := make(map[string]string, len(hcp.Spec.Platform.GCP.ResourceLabels))
	for _, label := range hcp.Spec.Platform.GCP.ResourceLabels {
		value := ""
		if label.Value != nil {
			value = *label.Value
		}
		labels[label.Key] = value
	}
	return labels
}

// MergeResourceLabels preserves labels that are not managed by HyperShift, removes
// withdrawn managed labels, and applies the desired labels. GCP permits at most
// 64 labels per resource.
func MergeResourceLabels(existing, desired map[string]string, previouslyManagedLabelKeys map[string]struct{}) (map[string]string, error) {
	merged := maps.Clone(existing)
	if merged == nil {
		merged = map[string]string{}
	}
	for key := range previouslyManagedLabelKeys {
		if _, stillManaged := desired[key]; !stillManaged {
			delete(merged, key)
		}
	}
	for key, value := range desired {
		merged[key] = value
	}
	if len(merged) > MaxResourceLabels {
		return nil, fmt.Errorf("merged resource labels exceed GCP limit of %d: %d labels", MaxResourceLabels, len(merged))
	}
	return merged, nil
}

// ManagedResourceLabelKeys returns the resource-label keys previously managed
// by HyperShift, stored as a comma-separated object annotation.
func ManagedResourceLabelKeys(annotations map[string]string, annotation string) map[string]struct{} {
	keys := map[string]struct{}{}
	for _, key := range strings.Split(annotations[annotation], ",") {
		if key != "" {
			keys[key] = struct{}{}
		}
	}
	return keys
}

// UpdateManagedResourceLabelKeys records the sorted desired resource-label keys
// in an object's annotation. It patches the object only when the annotation changes.
func UpdateManagedResourceLabelKeys(ctx context.Context, c client.Client, obj client.Object, annotation string, desiredLabels map[string]string) error {
	keys := make([]string, 0, len(desiredLabels))
	for key := range desiredLabels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	annotationValue := strings.Join(keys, ",")
	if obj.GetAnnotations()[annotation] == annotationValue {
		return nil
	}

	patch := client.MergeFrom(obj.DeepCopyObject().(client.Object))
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	if annotationValue == "" {
		delete(annotations, annotation)
	} else {
		annotations[annotation] = annotationValue
	}
	obj.SetAnnotations(annotations)
	return c.Patch(ctx, obj, patch)
}

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
