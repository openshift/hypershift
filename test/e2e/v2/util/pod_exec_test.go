//go:build e2ev2

/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package util

import (
	"strings"
	"testing"

	dto "github.com/prometheus/client_model/go"
)

func TestValidateMetricPresence(t *testing.T) {
	metrics := map[string]*dto.MetricFamily{
		"example_metric": {
			Metric: []*dto.Metric{
				{Label: []*dto.LabelPair{{Name: stringPtr("name"), Value: stringPtr("example")}}},
			},
		},
	}

	tests := []struct {
		name       string
		metricName string
		labelKey   string
		labelValue string
		expected   bool
		wantErr    string
	}{
		{
			name:       "matching metric is present",
			metricName: "example_metric",
			labelKey:   "name",
			labelValue: "example",
			expected:   true,
		},
		{
			name:       "missing metric is reported",
			metricName: "missing_metric",
			expected:   true,
			wantErr:    `expected results for metric "missing_metric", found none`,
		},
		{
			name:       "unexpected metric is reported",
			metricName: "example_metric",
			expected:   false,
			wantErr:    `expected 0 results for metric "example_metric", found 1`,
		},
		{
			name:       "nonmatching label is reported",
			metricName: "example_metric",
			labelKey:   "name",
			labelValue: "other",
			expected:   true,
			wantErr:    `expected results for metric "example_metric", found none`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateMetricPresence(metrics, tt.metricName, tt.labelKey, tt.labelValue, tt.metricName, tt.expected)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateMetricPresence returned unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("ValidateMetricPresence error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func stringPtr(value string) *string {
	return &value
}
