package gcputil

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

// LBResourceLabelsAnnotation is the Service annotation read by the GCP cloud-controller-manager
// (openshift/cloud-provider-gcp) to apply labels to the GCP forwarding rules
// created for that Service.
// Format: comma-separated "key=value" pairs, e.g. "goog-partner-solution=foo,env=prod".
const LBResourceLabelsAnnotation = "cloud.google.com/load-balancer-resource-labels"

// ManagedLBResourceLabelsAnnotation records the resource-label keys in
// LBResourceLabelsAnnotation that HyperShift manages. It lets HyperShift remove
// withdrawn HCP labels without removing labels supplied by the Service owner.
const ManagedLBResourceLabelsAnnotation = "hypershift.openshift.io/managed-gcp-lb-resource-label-keys"

const maxGCPResourceLabels = 64

// ResourceLabels converts the HCP GCP resource-label list to the map[string]string format
// expected by GCP API calls (e.g. SetLabels on ForwardingRules, Addresses).
// Returns nil when no labels are configured.
func ResourceLabels(hcp *hyperv1.HostedControlPlane) map[string]string {
	if hcp.Spec.Platform.GCP == nil {
		return nil
	}
	return ResourceLabelsToMap(hcp.Spec.Platform.GCP.ResourceLabels)
}

// ResourceLabelsToMap converts GCP resource labels to the map format expected
// by GCP APIs. Labels without a value are represented by an empty string.
// Returns nil when no labels are configured.
func ResourceLabelsToMap(resourceLabels []hyperv1.GCPResourceLabel) map[string]string {
	if len(resourceLabels) == 0 {
		return nil
	}

	labels := make(map[string]string, len(resourceLabels))
	for _, label := range resourceLabels {
		value := ""
		if label.Value != nil {
			value = *label.Value
		}
		labels[label.Key] = value
	}
	return labels
}

// LBResourceLabelsAnnotationValue serializes a GCP resource-label map into the
// comma-separated "key=value" string expected by LBResourceLabelsAnnotation.
// Returns an empty string when labels is nil or empty.
func LBResourceLabelsAnnotationValue(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	// Sort keys for a deterministic annotation value.
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, k+"="+labels[k])
	}
	return strings.Join(pairs, ",")
}

// ReconcileLBResourceLabelAnnotations merges HCP labels into a Service's GCP
// load-balancer resource-label annotation. Labels not previously managed by
// HyperShift are preserved. It returns the desired annotations and whether they
// differ from annotations.
func ReconcileLBResourceLabelAnnotations(annotations map[string]string, desired map[string]string) (map[string]string, bool, error) {
	existing, annotationPresent, err := ParseLBResourceLabelsAnnotation(annotations)
	if err != nil {
		return nil, false, err
	}
	managed, err := managedLBResourceLabelKeys(annotations)
	if err != nil {
		return nil, false, err
	}
	if err := validateGCPResourceLabels(desired); err != nil {
		return nil, false, fmt.Errorf("invalid desired GCP resource labels: %w", err)
	}

	// With no prior HyperShift ownership and no desired HCP labels, leave the
	// Service-owner annotation exactly as it is. In particular, an absent native
	// annotation means that CCM leaves forwarding-rule labels unmanaged.
	if len(managed) == 0 && len(desired) == 0 {
		return annotations, false, nil
	}

	merged := maps.Clone(existing)
	if merged == nil {
		merged = map[string]string{}
	}
	for key := range managed {
		if _, stillManaged := desired[key]; !stillManaged {
			delete(merged, key)
		}
	}
	for key, value := range desired {
		merged[key] = value
	}
	if len(merged) > maxGCPResourceLabels {
		return nil, false, fmt.Errorf("merged GCP resource labels exceed the limit of %d", maxGCPResourceLabels)
	}

	result := maps.Clone(annotations)
	if result == nil {
		result = map[string]string{}
	}
	// Preserve an explicitly empty annotation. CCM interprets it as a request to
	// clear forwarding-rule labels, whereas an absent annotation leaves them
	// unmanaged. A previously managed final key must therefore become empty, not
	// be deleted.
	if annotationPresent || len(managed) > 0 || len(desired) > 0 {
		result[LBResourceLabelsAnnotation] = LBResourceLabelsAnnotationValue(merged)
	}
	if len(desired) == 0 {
		delete(result, ManagedLBResourceLabelsAnnotation)
	} else {
		result[ManagedLBResourceLabelsAnnotation] = managedLBResourceLabelKeysValue(desired)
	}

	return result, !reflect.DeepEqual(annotations, result), nil
}

// ParseLBResourceLabelsAnnotation parses the native GCP Service annotation.
// The format and validation intentionally match cloud-provider-gcp's accepted
// key=value[,key=value] representation.
func ParseLBResourceLabelsAnnotation(annotations map[string]string) (map[string]string, bool, error) {
	value, present := annotations[LBResourceLabelsAnnotation]
	if !present {
		return nil, false, nil
	}
	labels := map[string]string{}
	if value == "" {
		return labels, true, nil
	}
	pairs := strings.Split(value, ",")
	if len(pairs) > maxGCPResourceLabels {
		return nil, true, fmt.Errorf("%s permits at most %d labels", LBResourceLabelsAnnotation, maxGCPResourceLabels)
	}
	for _, pair := range pairs {
		key, value, found := strings.Cut(pair, "=")
		if !found {
			return nil, true, fmt.Errorf("%s label %q must use key=value format", LBResourceLabelsAnnotation, pair)
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if err := validateGCPResourceLabel(key, value); err != nil {
			return nil, true, fmt.Errorf("invalid %s: %w", LBResourceLabelsAnnotation, err)
		}
		if _, exists := labels[key]; exists {
			return nil, true, fmt.Errorf("invalid %s: duplicate label key %q", LBResourceLabelsAnnotation, key)
		}
		labels[key] = value
	}
	return labels, true, nil
}

func managedLBResourceLabelKeys(annotations map[string]string) (map[string]struct{}, error) {
	keys := map[string]struct{}{}
	for _, key := range strings.Split(annotations[ManagedLBResourceLabelsAnnotation], ",") {
		if key == "" {
			continue
		}
		if err := validateGCPResourceLabel(key, ""); err != nil {
			return nil, fmt.Errorf("invalid %s: %w", ManagedLBResourceLabelsAnnotation, err)
		}
		keys[key] = struct{}{}
	}
	return keys, nil
}

func managedLBResourceLabelKeysValue(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

func validateGCPResourceLabels(labels map[string]string) error {
	if len(labels) > maxGCPResourceLabels {
		return fmt.Errorf("at most %d labels are allowed", maxGCPResourceLabels)
	}
	for key, value := range labels {
		if err := validateGCPResourceLabel(key, value); err != nil {
			return err
		}
	}
	return nil
}

func validateGCPResourceLabel(key, value string) error {
	if !utf8.ValidString(key) || !utf8.ValidString(value) {
		return fmt.Errorf("label key and value must be valid UTF-8")
	}
	if utf8.RuneCountInString(key) == 0 || utf8.RuneCountInString(key) > 63 {
		return fmt.Errorf("label key %q must contain 1 to 63 characters", key)
	}
	if utf8.RuneCountInString(value) > 63 {
		return fmt.Errorf("label value %q must contain at most 63 characters", value)
	}
	for i, r := range key {
		if i == 0 && !isGCPResourceLabelLetter(r) {
			return fmt.Errorf("label key %q must start with a lowercase or international letter", key)
		}
		if !isGCPResourceLabelCharacter(r) {
			return fmt.Errorf("label key %q contains invalid character %q", key, r)
		}
	}
	for _, r := range value {
		if !isGCPResourceLabelCharacter(r) {
			return fmt.Errorf("label value %q contains invalid character %q", value, r)
		}
	}
	return nil
}

func isGCPResourceLabelLetter(r rune) bool {
	return unicode.IsLetter(r) && !unicode.IsUpper(r) && !unicode.IsTitle(r)
}

func isGCPResourceLabelCharacter(r rune) bool {
	return isGCPResourceLabelLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-'
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
