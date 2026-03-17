package hostedcontrolplane

import (
	"fmt"
	"reflect"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/google/go-cmp/cmp"
)

// makeHistory creates a slice of ControlPlaneUpdateHistory entries for pruning tests.
// Entries are ordered newest-first (index 0 = most recent). Callers provide version
// strings in the form "4.17.2" and states (Completed or Partial).
func makeHistory(entries []struct {
	version string
	state   configv1.UpdateState
}) []hyperv1.ControlPlaneUpdateHistory {
	result := make([]hyperv1.ControlPlaneUpdateHistory, len(entries))
	for i, e := range entries {
		h := hyperv1.ControlPlaneUpdateHistory{
			State:       e.state,
			StartedTime: metav1.Now(),
			Version:     e.version,
			Image:       fmt.Sprintf("quay.io/ocp/release:%s", e.version),
		}
		if e.state == configv1.CompletedUpdate {
			now := metav1.Now()
			h.CompletionTime = now
		}
		result[i] = h
	}
	return result
}

// makeSimpleHistory creates a slice of ControlPlaneUpdateHistory with just state and version,
// matching the CVO test style where only State and Version matter for pruning logic.
func makeSimpleHistory(entries []struct {
	state   configv1.UpdateState
	version string
}) []hyperv1.ControlPlaneUpdateHistory {
	result := make([]hyperv1.ControlPlaneUpdateHistory, len(entries))
	for i, e := range entries {
		result[i] = hyperv1.ControlPlaneUpdateHistory{
			State:   e.state,
			Version: e.version,
		}
	}
	return result
}

// historyVersions extracts state+version pairs for comparison, ignoring time fields.
func historyVersions(h []hyperv1.ControlPlaneUpdateHistory) []struct {
	state   configv1.UpdateState
	version string
} {
	result := make([]struct {
		state   configv1.UpdateState
		version string
	}, len(h))
	for i, e := range h {
		result[i] = struct {
			state   configv1.UpdateState
			version string
		}{state: e.State, version: e.Version}
	}
	return result
}

// testMaxHistory is the max history size used in the CVO-ported prune test cases.
// CVO tests use 10 (with 11-entry inputs) to keep test data small.
const testMaxHistory = 10

// TestPruneHistory ports CVO's Test_prune test cases.
func TestPruneHistory(t *testing.T) {
	type entry = struct {
		state   configv1.UpdateState
		version string
	}
	tests := []struct {
		name    string
		history []entry
		want    []entry
	}{
		{
			name: "When a partial update exists within a minor transition, it should prune the z-stream partial",
			history: []entry{
				{configv1.CompletedUpdate, "4.6.3"},
				{configv1.CompletedUpdate, "4.5.3"},
				{configv1.CompletedUpdate, "4.4.3"},
				{configv1.CompletedUpdate, "4.3.3"},
				{configv1.CompletedUpdate, "4.3.2"},
				{configv1.CompletedUpdate, "4.3.1"},
				{configv1.CompletedUpdate, "4.2.1"},
				{configv1.PartialUpdate, "4.1.4"},
				{configv1.PartialUpdate, "4.1.3"},
				{configv1.PartialUpdate, "4.1.2"},
				{configv1.CompletedUpdate, "4.1.1"},
			},
			want: []entry{
				{configv1.CompletedUpdate, "4.6.3"},
				{configv1.CompletedUpdate, "4.5.3"},
				{configv1.CompletedUpdate, "4.4.3"},
				{configv1.CompletedUpdate, "4.3.3"},
				{configv1.CompletedUpdate, "4.3.2"},
				{configv1.CompletedUpdate, "4.3.1"},
				{configv1.CompletedUpdate, "4.2.1"},
				{configv1.PartialUpdate, "4.1.4"},
				{configv1.PartialUpdate, "4.1.3"},
				{configv1.CompletedUpdate, "4.1.1"},
			},
		},
		{
			name: "When a partial update exists within a z-stream transition, it should prune the z-stream partial",
			history: []entry{
				{configv1.CompletedUpdate, "4.5.3"},
				{configv1.CompletedUpdate, "4.4.3"},
				{configv1.CompletedUpdate, "4.4.3"},
				{configv1.CompletedUpdate, "4.4.2"},
				{configv1.CompletedUpdate, "4.1.10"},
				{configv1.CompletedUpdate, "4.1.9"},
				{configv1.CompletedUpdate, "4.1.4"},
				{configv1.PartialUpdate, "4.1.3"},
				{configv1.CompletedUpdate, "4.1.2"},
				{configv1.PartialUpdate, "4.0.1"},
				{configv1.CompletedUpdate, "4.0.1"},
			},
			want: []entry{
				{configv1.CompletedUpdate, "4.5.3"},
				{configv1.CompletedUpdate, "4.4.3"},
				{configv1.CompletedUpdate, "4.4.3"},
				{configv1.CompletedUpdate, "4.4.2"},
				{configv1.CompletedUpdate, "4.1.10"},
				{configv1.CompletedUpdate, "4.1.9"},
				{configv1.CompletedUpdate, "4.1.4"},
				{configv1.CompletedUpdate, "4.1.2"},
				{configv1.PartialUpdate, "4.0.1"},
				{configv1.CompletedUpdate, "4.0.1"},
			},
		},
		{
			name: "When all entries are completed across different minors, it should prune the oldest not in mostImportantWeight set",
			history: []entry{
				{configv1.CompletedUpdate, "4.11.0"},
				{configv1.CompletedUpdate, "4.10.0"},
				{configv1.CompletedUpdate, "4.9.0"},
				{configv1.CompletedUpdate, "4.8.0"},
				{configv1.CompletedUpdate, "4.7.0"},
				{configv1.CompletedUpdate, "4.6.0"},
				{configv1.CompletedUpdate, "4.5.0"},
				{configv1.CompletedUpdate, "4.4.0"},
				{configv1.CompletedUpdate, "4.3.0"},
				{configv1.CompletedUpdate, "4.2.0"},
				{configv1.CompletedUpdate, "4.1.0"},
			},
			want: []entry{
				{configv1.CompletedUpdate, "4.11.0"},
				{configv1.CompletedUpdate, "4.10.0"},
				{configv1.CompletedUpdate, "4.9.0"},
				{configv1.CompletedUpdate, "4.8.0"},
				{configv1.CompletedUpdate, "4.7.0"},
				{configv1.CompletedUpdate, "4.6.0"},
				{configv1.CompletedUpdate, "4.5.0"},
				{configv1.CompletedUpdate, "4.4.0"},
				{configv1.CompletedUpdate, "4.3.0"},
				{configv1.CompletedUpdate, "4.1.0"},
			},
		},
		{
			name: "When a partial exists not in mostImportantWeight set, it should prune only the partial",
			history: []entry{
				{configv1.CompletedUpdate, "4.11.0"},
				{configv1.PartialUpdate, "4.10.0"},
				{configv1.CompletedUpdate, "4.9.0"},
				{configv1.CompletedUpdate, "4.8.0"},
				{configv1.CompletedUpdate, "4.7.0"},
				{configv1.PartialUpdate, "4.6.0"},
				{configv1.CompletedUpdate, "4.5.0"},
				{configv1.CompletedUpdate, "4.4.0"},
				{configv1.CompletedUpdate, "4.3.0"},
				{configv1.CompletedUpdate, "4.2.0"},
				{configv1.CompletedUpdate, "4.1.0"},
			},
			want: []entry{
				{configv1.CompletedUpdate, "4.11.0"},
				{configv1.PartialUpdate, "4.10.0"},
				{configv1.CompletedUpdate, "4.9.0"},
				{configv1.CompletedUpdate, "4.8.0"},
				{configv1.CompletedUpdate, "4.7.0"},
				{configv1.CompletedUpdate, "4.5.0"},
				{configv1.CompletedUpdate, "4.4.0"},
				{configv1.CompletedUpdate, "4.3.0"},
				{configv1.CompletedUpdate, "4.2.0"},
				{configv1.CompletedUpdate, "4.1.0"},
			},
		},
		{
			name: "When both z-stream and minor partials exist, it should prune the z-stream partial over the minor partial",
			history: []entry{
				{configv1.CompletedUpdate, "4.11.0"},
				{configv1.CompletedUpdate, "4.10.0"},
				{configv1.CompletedUpdate, "4.9.0"},
				{configv1.CompletedUpdate, "4.8.0"},
				{configv1.CompletedUpdate, "4.6.1"},
				{configv1.PartialUpdate, "4.6.1"},
				{configv1.CompletedUpdate, "4.6.0"},
				{configv1.CompletedUpdate, "4.4.0"},
				{configv1.PartialUpdate, "4.3.0"},
				{configv1.CompletedUpdate, "4.2.0"},
				{configv1.CompletedUpdate, "4.1.0"},
			},
			want: []entry{
				{configv1.CompletedUpdate, "4.11.0"},
				{configv1.CompletedUpdate, "4.10.0"},
				{configv1.CompletedUpdate, "4.9.0"},
				{configv1.CompletedUpdate, "4.8.0"},
				{configv1.CompletedUpdate, "4.6.1"},
				{configv1.CompletedUpdate, "4.6.0"},
				{configv1.CompletedUpdate, "4.4.0"},
				{configv1.PartialUpdate, "4.3.0"},
				{configv1.CompletedUpdate, "4.2.0"},
				{configv1.CompletedUpdate, "4.1.0"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := makeSimpleHistory(tt.history)
			got := pruneHistoryWithMax(input, testMaxHistory)
			gotVersions := historyVersions(got)
			if !reflect.DeepEqual(tt.want, gotVersions) {
				t.Fatalf("unexpected result:\n%s", cmp.Diff(tt.want, gotVersions))
			}
		})
	}
}

// TestPruneHistory_UnderMaxHistoryUnchanged verifies that when history has fewer
// than maxHistory entries, pruning is a no-op.
func TestPruneHistory_UnderMaxHistoryUnchanged(t *testing.T) {
	entries := make([]struct {
		version string
		state   configv1.UpdateState
	}, 50)
	for i := 0; i < 50; i++ {
		entries[i] = struct {
			version string
			state   configv1.UpdateState
		}{
			version: fmt.Sprintf("4.17.%d", 49-i),
			state:   configv1.CompletedUpdate,
		}
	}
	history := makeHistory(entries)

	pruned := pruneHistory(history)

	if len(pruned) != 50 {
		t.Errorf("When fewer than %d entries exist, it should not prune, got %d entries", maxHistory, len(pruned))
	}
}

// TestPruneHistory_ExactlyMaxHistoryUnchanged verifies that when history has exactly
// maxHistory entries, pruning is a no-op.
func TestPruneHistory_ExactlyMaxHistoryUnchanged(t *testing.T) {
	entries := make([]struct {
		version string
		state   configv1.UpdateState
	}, maxHistory)
	for i := 0; i < maxHistory; i++ {
		entries[i] = struct {
			version string
			state   configv1.UpdateState
		}{
			version: fmt.Sprintf("4.17.%d", 99-i),
			state:   configv1.CompletedUpdate,
		}
	}
	history := makeHistory(entries)

	pruned := pruneHistory(history)

	if len(pruned) != maxHistory {
		t.Errorf("When exactly %d entries exist, it should not prune, got %d entries", maxHistory, len(pruned))
	}
}

// TestPruneHistory_CapAtMaxHistory verifies that when more than maxHistory entries
// exist, pruning reduces the list to exactly maxHistory.
func TestPruneHistory_CapAtMaxHistory(t *testing.T) {
	entries := make([]struct {
		version string
		state   configv1.UpdateState
	}, 105)
	for i := 0; i < 105; i++ {
		entries[i] = struct {
			version string
			state   configv1.UpdateState
		}{
			version: fmt.Sprintf("4.17.%d", 104-i),
			state:   configv1.CompletedUpdate,
		}
	}
	history := makeHistory(entries)

	pruned := pruneHistory(history)

	if len(pruned) != maxHistory {
		t.Errorf("When more than maxHistory entries exist, it should prune to %d, got %d", maxHistory, len(pruned))
	}
}

// TestPruneHistory_DeterministicResults verifies that the pruning algorithm
// produces deterministic results when called multiple times with the same input.
func TestPruneHistory_DeterministicResults(t *testing.T) {
	entries := make([]struct {
		version string
		state   configv1.UpdateState
	}, 105)
	for i := 0; i < 105; i++ {
		entries[i] = struct {
			version string
			state   configv1.UpdateState
		}{
			version: fmt.Sprintf("4.17.%d", 104-i),
			state:   configv1.CompletedUpdate,
		}
	}
	history := makeHistory(entries)

	pruned1 := pruneHistory(history)
	pruned2 := pruneHistory(makeHistory(entries))

	if len(pruned1) != len(pruned2) {
		t.Fatalf("When pruning the same input twice, it should produce same length, got %d vs %d",
			len(pruned1), len(pruned2))
	}
	for i := range pruned1 {
		if pruned1[i].Version != pruned2[i].Version {
			t.Errorf("When pruning the same input twice, it should produce deterministic results at index %d: %s vs %s",
				i, pruned1[i].Version, pruned2[i].Version)
		}
	}
}
