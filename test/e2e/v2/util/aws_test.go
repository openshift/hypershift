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
	"testing"

	supportawsutil "github.com/openshift/hypershift/support/awsutil"
)

func TestE2ETagsFromEnvironment(t *testing.T) {
	tests := []struct {
		name      string
		prowJobID string
		wantJobID bool
	}{
		{
			name: "When PROW_JOB_ID is unset, it should return only the e2e source tag",
		},
		{
			name:      "When PROW_JOB_ID is set, it should include the Prow job tag",
			prowJobID: "job-123",
			wantJobID: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("PROW_JOB_ID", tt.prowJobID)
			tags := E2ETagsFromEnvironment()
			if tags[supportawsutil.HypershiftSourceTagKey] != "e2e" {
				t.Fatalf("source tag = %q, want %q", tags[supportawsutil.HypershiftSourceTagKey], "e2e")
			}
			jobID, found := tags[supportawsutil.HypershiftProwJobIDTagKey]
			if found != tt.wantJobID {
				t.Fatalf("Prow job tag present = %t, want %t", found, tt.wantJobID)
			}
			if tt.wantJobID && jobID != tt.prowJobID {
				t.Fatalf("Prow job tag = %q, want %q", jobID, tt.prowJobID)
			}
		})
	}
}
