package util

import (
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	supportawsutil "github.com/openshift/hypershift/support/awsutil"
)

func TestResolveSharedOIDCProviderTags(t *testing.T) {
	const providerID = "e2e-oidc-provider-abc123"
	tests := []struct {
		name           string
		additionalTags []string
		e2eTags        map[string]string
		expected       []string
		expectError    bool
	}{
		{
			name:     "When no optional tags are supplied, it should use the provider identity",
			expected: []string{supportawsutil.HypershiftInfraIDTagKey + "=" + providerID},
		},
		{
			name:     "When running locally, it should include the e2e source without a Prow job ID",
			e2eTags:  map[string]string{supportawsutil.HypershiftSourceTagKey: "e2e"},
			expected: []string{supportawsutil.HypershiftInfraIDTagKey + "=" + providerID, supportawsutil.HypershiftSourceTagKey + "=e2e"},
		},
		{
			name:           "When running in Prow, it should include the job ID and preserve custom tags",
			additionalTags: []string{"owner=CI team/platform=AWS"},
			e2eTags:        map[string]string{supportawsutil.HypershiftSourceTagKey: "e2e", supportawsutil.HypershiftProwJobIDTagKey: "123456789"},
			expected:       []string{supportawsutil.HypershiftInfraIDTagKey + "=" + providerID, supportawsutil.HypershiftProwJobIDTagKey + "=123456789", supportawsutil.HypershiftSourceTagKey + "=e2e", "owner=CI team/platform=AWS"},
		},
		{
			name:           "When custom tags conflict with provenance, it should use the actual provenance without duplicate keys",
			additionalTags: []string{supportawsutil.HypershiftSourceTagKey + "=cli", supportawsutil.HypershiftProwJobIDTagKey + "=wrong-job", supportawsutil.HypershiftInfraIDTagKey + "=wrong-provider"},
			e2eTags:        map[string]string{supportawsutil.HypershiftSourceTagKey: "e2e", supportawsutil.HypershiftProwJobIDTagKey: "123456789", supportawsutil.HypershiftInfraIDTagKey: "wrong-environment-provider"},
			expected:       []string{supportawsutil.HypershiftInfraIDTagKey + "=" + providerID, supportawsutil.HypershiftProwJobIDTagKey + "=123456789", supportawsutil.HypershiftSourceTagKey + "=e2e"},
		},
		{
			name:           "When a custom tag is malformed, it should return a parsing error",
			additionalTags: []string{"malformed"},
			expectError:    true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			originalTags := slices.Clone(test.additionalTags)
			originalE2ETags := maps.Clone(test.e2eTags)
			tags, err := resolveSharedOIDCProviderTags(providerID, test.additionalTags, test.e2eTags)
			if test.expectError {
				if err == nil || !strings.Contains(err.Error(), "failed to parse additional tags") {
					t.Fatalf("expected a parsing error, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(tags, test.expected) {
				t.Fatalf("expected tags %v, got %v", test.expected, tags)
			}
			if !reflect.DeepEqual(originalTags, test.additionalTags) || !reflect.DeepEqual(originalE2ETags, test.e2eTags) {
				t.Fatal("tag resolution mutated its inputs")
			}
		})
	}
}
