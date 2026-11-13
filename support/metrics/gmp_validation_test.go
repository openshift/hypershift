package metrics

import (
	"testing"

	"k8s.io/utils/ptr"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSanitizeGMPRelabelConfigs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		source      []prometheusoperatorv1.RelabelConfig
		expected    []prometheusoperatorv1.RelabelConfig
		expectedErr string
	}{
		{
			name: "When a rule drops every metric, it should keep nothing instead",
			source: []prometheusoperatorv1.RelabelConfig{{
				Action:       "drop",
				Regex:        "(.*)",
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__"},
			}},
			expected: []prometheusoperatorv1.RelabelConfig{{
				Action:       "keep",
				Regex:        "^$",
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__"},
			}},
		},
		{
			name: "When a catch-all drop has no source labels, it should pin the rewrite to a label every series has",
			source: []prometheusoperatorv1.RelabelConfig{{
				Action: "drop",
				Regex:  ".*",
			}},
			expected: []prometheusoperatorv1.RelabelConfig{{
				Action:       "keep",
				Regex:        "^$",
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__"},
			}},
		},
		{
			name: "When a catch-all drop references a label a series may omit, it should pin the rewrite instead of keeping everything",
			source: []prometheusoperatorv1.RelabelConfig{{
				Action:       "drop",
				Regex:        "(.*)",
				SourceLabels: []prometheusoperatorv1.LabelName{"optional_label"},
			}},
			expected: []prometheusoperatorv1.RelabelConfig{{
				Action:       "keep",
				Regex:        "^$",
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__"},
			}},
		},
		{
			name: "When a drop rule omits its regex, it should be admitted unchanged because GMP does not inspect it",
			source: []prometheusoperatorv1.RelabelConfig{{
				Action:       "drop",
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__"},
			}},
			expected: []prometheusoperatorv1.RelabelConfig{{
				Action:       "drop",
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__"},
			}},
		},
		{
			name: "When a replace rule omits its regex, it should be admitted unchanged",
			source: []prometheusoperatorv1.RelabelConfig{{
				Action:       "replace",
				TargetLabel:  "cluster_id",
				Replacement:  ptr.To("01234567-89ab-cdef-0123-456789abcdef"),
				SourceLabels: nil,
			}},
			expected: []prometheusoperatorv1.RelabelConfig{{
				Action:      "replace",
				TargetLabel: "cluster_id",
				Replacement: ptr.To("01234567-89ab-cdef-0123-456789abcdef"),
			}},
		},
		{
			name: "When a rule keeps every metric, it should be dropped as a no-op",
			source: []prometheusoperatorv1.RelabelConfig{{
				Action:       "keep",
				Regex:        ".*",
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__"},
			}},
			expected: nil,
		},
		{
			name: "When a regex cannot match a protected label, it should be preserved",
			source: []prometheusoperatorv1.RelabelConfig{{
				Action:       "drop",
				Regex:        "etcd_(debugging|disk|server).*",
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__"},
			}},
			expected: []prometheusoperatorv1.RelabelConfig{{
				Action:       "drop",
				Regex:        "etcd_(debugging|disk|server).*",
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__"},
			}},
		},
		{
			name: "When a regex mentions job but nothing admission protects, it should be preserved",
			source: []prometheusoperatorv1.RelabelConfig{{
				Action:       "drop",
				Regex:        "kube_job_.*",
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__"},
			}},
			expected: []prometheusoperatorv1.RelabelConfig{{
				Action:       "drop",
				Regex:        "kube_job_.*",
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__"},
			}},
		},
		{
			name: "When a regex matches a protected label without matching everything, it should return an error",
			source: []prometheusoperatorv1.RelabelConfig{{
				Action:       "drop",
				Regex:        "(project_id|location)",
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__"},
			}},
			expectedErr: `matches protected label`,
		},
		{
			name: "When a regex matches only part of a protected label name, it should return an error",
			source: []prometheusoperatorv1.RelabelConfig{{
				Action:       "drop",
				Regex:        "id",
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__"},
			}},
			expectedErr: `matches protected label "project_id"`,
		},
		{
			name: "When a broad regex only matches the sample probes, it should return an error instead of being rewritten",
			source: []prometheusoperatorv1.RelabelConfig{{
				Action:       "drop",
				Regex:        "[a-z_]*",
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__"},
			}},
			expectedErr: `matches protected label`,
		},
		{
			name: "When a labelkeep rule sets an explicit regex, it should return an error because GMP cannot admit and apply it",
			source: []prometheusoperatorv1.RelabelConfig{{
				Action: "labelkeep",
				Regex:  "foo",
			}},
			expectedErr: "GMP cannot represent a labelkeep rule",
		},
		{
			name: "When a labelkeep rule omits its regex, it should be dropped as a no-op",
			source: []prometheusoperatorv1.RelabelConfig{{
				Action: "labelkeep",
			}},
			expected: nil,
		},
		{
			name: "When a labeldrop regex matches a label the collector protects, it should return an error",
			source: []prometheusoperatorv1.RelabelConfig{{
				Action: "labeldrop",
				Regex:  "job",
			}},
			expectedErr: `collector refuses to drop`,
		},
		{
			name: "When a replace action uses a catch-all regex, it should return an error",
			source: []prometheusoperatorv1.RelabelConfig{{
				Action:       "replace",
				Regex:        "(.*)",
				TargetLabel:  "app",
				SourceLabels: []prometheusoperatorv1.LabelName{"__name__"},
			}},
			expectedErr: "not representable in GMP",
		},
		{
			name: "When a rule relabels onto a label GMP protects, it should return an error",
			source: []prometheusoperatorv1.RelabelConfig{{
				TargetLabel: "namespace",
				Replacement: ptr.To("openshift-monitoring"),
			}},
			expectedErr: `target label "namespace" is protected by GMP`,
		},
		{
			name:     "When the source is empty, it should return nil",
			source:   nil,
			expected: nil,
		},
		{
			name: "When a rule is structurally invalid, it should be passed through for the converter to reject",
			source: []prometheusoperatorv1.RelabelConfig{{
				Action: "bogus",
				Regex:  "anything",
			}},
			expected: []prometheusoperatorv1.RelabelConfig{{
				Action: "bogus",
				Regex:  "anything",
			}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			actual, err := SanitizeGMPRelabelConfigs(tc.source)
			if tc.expectedErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectedErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.expected, actual)
		})
	}
}

// TestTargetLabelProtectedLabelsAreTheUnion pins the target-label set to the
// union of the admission and collector sets so a change to either one is caught
// here rather than in admission at apply time.
func TestTargetLabelProtectedLabelsAreTheUnion(t *testing.T) {
	t.Parallel()

	union := make(map[string]struct{}, len(admissionProtectedLabels)+len(collectorProtectedLabels))
	for name := range admissionProtectedLabels {
		union[name] = struct{}{}
	}
	for name := range collectorProtectedLabels {
		union[name] = struct{}{}
	}

	assert.Equal(t, union, targetLabelProtectedLabels)
}
