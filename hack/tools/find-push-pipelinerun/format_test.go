package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestFormatPipelineRuns_BasicTable(t *testing.T) {
	prs := []PipelineRun{
		{
			Metadata: ObjectMeta{
				Name:              "my-pipeline-on-push-abc12",
				CreationTimestamp: "2026-06-26T13:47:51Z",
				Annotations:       map[string]string{logURLAnnotation: "https://example.com/run/abc"},
			},
			Status: PipelineRunStatus{
				Conditions: []Condition{{Type: "Succeeded", Reason: "Completed"}},
			},
		},
	}

	var buf bytes.Buffer
	ok := FormatPipelineRuns(&buf, prs)
	if !ok {
		t.Fatal("expected output")
	}
	out := buf.String()
	for _, want := range []string{"NAME", "STATUS", "my-pipeline-on-push-abc12", "Completed", "https://example.com/run/abc"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestFormatPipelineRuns_ImageColumn(t *testing.T) {
	prs := []PipelineRun{
		{
			Metadata: ObjectMeta{Name: "plr", CreationTimestamp: "2026-06-26T13:00:00Z"},
			Status: PipelineRunStatus{
				Conditions: []Condition{{Type: "Succeeded", Reason: "Completed"}},
				Results: []Result{
					{Name: "IMAGE_URL", Value: "quay.io/org/img:tag"},
					{Name: "IMAGE_DIGEST", Value: "sha256:abc123"},
				},
			},
		},
	}

	var buf bytes.Buffer
	FormatPipelineRuns(&buf, prs)
	if !strings.Contains(buf.String(), "quay.io/org/img:tag@sha256:abc123") {
		t.Errorf("output missing image:\n%s", buf.String())
	}
}

func TestFormatPipelineRuns_ImageURLOnly(t *testing.T) {
	prs := []PipelineRun{
		{
			Metadata: ObjectMeta{Name: "plr", CreationTimestamp: "2026-06-26T13:00:00Z"},
			Status: PipelineRunStatus{
				Conditions: []Condition{{Type: "Succeeded", Reason: "Completed"}},
				Results:    []Result{{Name: "IMAGE_URL", Value: "quay.io/org/img:tag"}},
			},
		},
	}

	var buf bytes.Buffer
	FormatPipelineRuns(&buf, prs)
	if !strings.Contains(buf.String(), "quay.io/org/img:tag") {
		t.Errorf("output missing image URL:\n%s", buf.String())
	}
}

func TestFormatPipelineRuns_SortsByTimestamp(t *testing.T) {
	prs := []PipelineRun{
		{
			Metadata: ObjectMeta{Name: "second", CreationTimestamp: "2026-06-26T14:00:00Z"},
			Status:   PipelineRunStatus{Conditions: []Condition{{Type: "Succeeded", Reason: "Running"}}},
		},
		{
			Metadata: ObjectMeta{Name: "first", CreationTimestamp: "2026-06-26T13:00:00Z"},
			Status:   PipelineRunStatus{Conditions: []Condition{{Type: "Succeeded", Reason: "Completed"}}},
		},
	}

	var buf bytes.Buffer
	FormatPipelineRuns(&buf, prs)
	out := buf.String()
	firstIdx := strings.Index(out, "first")
	secondIdx := strings.Index(out, "second")
	if firstIdx > secondIdx {
		t.Errorf("expected 'first' before 'second' in output:\n%s", out)
	}
}

func TestFormatPipelineRuns_EmptyReturnsFalse(t *testing.T) {
	var buf bytes.Buffer
	if FormatPipelineRuns(&buf, nil) {
		t.Error("expected false for empty input")
	}
}

func TestFormatPipelineRuns_MissingConditions(t *testing.T) {
	prs := []PipelineRun{
		{
			Metadata: ObjectMeta{Name: "no-status", CreationTimestamp: "2026-06-26T13:00:00Z"},
		},
	}

	var buf bytes.Buffer
	FormatPipelineRuns(&buf, prs)
	if !strings.Contains(buf.String(), "<none>") {
		t.Errorf("expected <none> for missing conditions:\n%s", buf.String())
	}
}

func TestHasPending_WithRunning(t *testing.T) {
	prs := []PipelineRun{
		{Status: PipelineRunStatus{Conditions: []Condition{{Type: "Succeeded", Reason: "Completed"}}}},
		{Status: PipelineRunStatus{Conditions: []Condition{{Type: "Succeeded", Reason: "Running"}}}},
	}
	if !HasPending(prs) {
		t.Error("expected HasPending=true when a run is still Running")
	}
}

func TestHasPending_AllTerminal(t *testing.T) {
	prs := []PipelineRun{
		{Status: PipelineRunStatus{Conditions: []Condition{{Type: "Succeeded", Reason: "Failed"}}}},
		{Status: PipelineRunStatus{Conditions: []Condition{{Type: "Succeeded", Reason: "Completed"}}}},
	}
	if HasPending(prs) {
		t.Error("expected HasPending=false when all runs are terminal")
	}
}

func TestFilterByComponent(t *testing.T) {
	prs := []PipelineRun{
		{Metadata: ObjectMeta{Name: "hypershift-operator-main-abc"}},
		{Metadata: ObjectMeta{Name: "hypershift-cli-main-def"}},
		{Metadata: ObjectMeta{Name: "hypershift-operator-mce-50-ghi"}},
	}

	filtered := FilterByComponent(prs, "hypershift-operator")
	if len(filtered) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(filtered))
	}
	if filtered[0].Metadata.Name != "hypershift-operator-main-abc" {
		t.Errorf("first match = %q", filtered[0].Metadata.Name)
	}
}

func TestFilterByComponent_NoMatch(t *testing.T) {
	prs := []PipelineRun{
		{Metadata: ObjectMeta{Name: "hypershift-operator-main-abc"}},
	}
	filtered := FilterByComponent(prs, "nonexistent")
	if len(filtered) != 0 {
		t.Errorf("expected 0 matches, got %d", len(filtered))
	}
}

// --- Release formatting ---

func TestFormatReleases_BasicTable(t *testing.T) {
	releases := []Release{
		{
			Metadata: ObjectMeta{
				Name:              "rel-abc-12345",
				CreationTimestamp: "2026-07-06T09:58:17Z",
				Labels:            map[string]string{"appstudio.openshift.io/component": "hypershift-release-mce-50"},
			},
			Spec: ReleaseSpec{ReleasePlan: "dev-publish-mce-50"},
			Status: ReleaseStatus{
				Conditions: []Condition{{Type: "Released", Reason: "Succeeded"}},
			},
		},
	}

	var buf bytes.Buffer
	ok := FormatReleases(&buf, releases)
	if !ok {
		t.Fatal("expected output")
	}
	out := buf.String()
	for _, want := range []string{"NAME", "PLAN", "COMPONENT", "STATUS", "rel-abc-12345", "dev-publish-mce-50", "Succeeded", "hypershift-release-mce-50"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestFormatReleases_SortsByTimestamp(t *testing.T) {
	releases := []Release{
		{
			Metadata: ObjectMeta{Name: "second-rel", CreationTimestamp: "2026-07-06T10:00:00Z"},
			Status:   ReleaseStatus{Conditions: []Condition{{Type: "Released", Reason: "Succeeded"}}},
		},
		{
			Metadata: ObjectMeta{Name: "first-rel", CreationTimestamp: "2026-07-06T09:00:00Z"},
			Status:   ReleaseStatus{Conditions: []Condition{{Type: "Released", Reason: "Succeeded"}}},
		},
	}

	var buf bytes.Buffer
	FormatReleases(&buf, releases)
	out := buf.String()
	if strings.Index(out, "first-rel") > strings.Index(out, "second-rel") {
		t.Errorf("expected first-rel before second-rel:\n%s", out)
	}
}

func TestFormatReleases_EmptyReturnsFalse(t *testing.T) {
	var buf bytes.Buffer
	if FormatReleases(&buf, nil) {
		t.Error("expected false for empty input")
	}
}

func TestFormatReleases_MissingConditions(t *testing.T) {
	releases := []Release{
		{
			Metadata: ObjectMeta{Name: "no-status", CreationTimestamp: "2026-07-06T09:00:00Z"},
		},
	}

	var buf bytes.Buffer
	FormatReleases(&buf, releases)
	if !strings.Contains(buf.String(), "<none>") {
		t.Errorf("expected <none> for missing conditions:\n%s", buf.String())
	}
}

func TestFormatReleases_SelectsReleasedCondition(t *testing.T) {
	releases := []Release{
		{
			Metadata: ObjectMeta{Name: "rel-1", CreationTimestamp: "2026-07-06T09:00:00Z"},
			Status: ReleaseStatus{
				Conditions: []Condition{
					{Type: "Processed", Reason: "Succeeded"},
					{Type: "Released", Reason: "Progressing"},
				},
			},
		},
	}

	var buf bytes.Buffer
	FormatReleases(&buf, releases)
	out := buf.String()
	if !strings.Contains(out, "Progressing") {
		t.Errorf("expected Released condition reason (Progressing), got:\n%s", out)
	}
}

func TestHasPendingReleases_WithProgressing(t *testing.T) {
	releases := []Release{
		{Status: ReleaseStatus{Conditions: []Condition{{Type: "Released", Reason: "Succeeded"}}}},
		{Status: ReleaseStatus{Conditions: []Condition{{Type: "Released", Reason: "Progressing"}}}},
	}
	if !HasPendingReleases(releases) {
		t.Error("expected HasPendingReleases=true with Progressing release")
	}
}

func TestHasPendingReleases_AllTerminal(t *testing.T) {
	releases := []Release{
		{Status: ReleaseStatus{Conditions: []Condition{{Type: "Released", Reason: "Succeeded"}}}},
		{Status: ReleaseStatus{Conditions: []Condition{{Type: "Released", Reason: "Failed"}}}},
	}
	if HasPendingReleases(releases) {
		t.Error("expected HasPendingReleases=false when all terminal")
	}
}

// --- Release PipelineRun formatting ---

func TestFormatReleasePipelineRuns_BasicTable(t *testing.T) {
	prs := []PipelineRun{
		{
			Metadata: ObjectMeta{
				Name:              "managed-abc12",
				CreationTimestamp: "2026-07-06T09:58:28Z",
				Labels:            map[string]string{"release.appstudio.openshift.io/name": "rel-abc-12345"},
			},
			Status: PipelineRunStatus{
				Conditions: []Condition{{Type: "Succeeded", Reason: "Completed"}},
			},
		},
	}

	var buf bytes.Buffer
	ok := FormatReleasePipelineRuns(&buf, prs)
	if !ok {
		t.Fatal("expected output")
	}
	out := buf.String()
	for _, want := range []string{"NAME", "RELEASE", "managed-abc12", "rel-abc-12345", "Completed"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestFormatReleasePipelineRuns_EmptyReturnsFalse(t *testing.T) {
	var buf bytes.Buffer
	if FormatReleasePipelineRuns(&buf, nil) {
		t.Error("expected false for empty input")
	}
}

// --- Destination image resolution ---

func TestResolveDestImage(t *testing.T) {
	q := &mockQuerier{
		getReleasePlanFn: func(name string) (*ReleasePlan, error) {
			rp := &ReleasePlan{}
			rp.Spec.Data.Mapping = &Mapping{
				Components: []MappingComponent{
					{
						Name:         "hypershift-release-mce-50",
						Repositories: []MappingRepository{{URL: "quay.io/acm-d/hypershift-rhel9-operator"}},
					},
				},
			}
			return rp, nil
		},
		getSnapshotFn: func(name string) (*Snapshot, error) {
			return &Snapshot{
				Spec: struct {
					Components []SnapshotComponent `json:"components"`
				}{
					Components: []SnapshotComponent{
						{Name: "hypershift-release-mce-50", ContainerImage: "quay.io/redhat/img@sha256:abc123"},
					},
				},
			}, nil
		},
	}

	releases := []Release{
		{
			Metadata: ObjectMeta{
				Name:   "rel-1",
				Labels: map[string]string{"appstudio.openshift.io/component": "hypershift-release-mce-50"},
			},
			Spec: ReleaseSpec{ReleasePlan: "dev-pub", Snapshot: "snap-1"},
		},
	}

	images := ResolveDestImages(q, releases)
	want := "quay.io/acm-d/hypershift-rhel9-operator@sha256:abc123"
	if images["rel-1"] != want {
		t.Errorf("dest image = %q, want %q", images["rel-1"], want)
	}
}

func TestResolveDestImage_ComponentNotFound(t *testing.T) {
	q := &mockQuerier{
		getReleasePlanFn: func(name string) (*ReleasePlan, error) {
			rp := &ReleasePlan{}
			rp.Spec.Data.Mapping = &Mapping{
				Components: []MappingComponent{
					{
						Name:         "other-component",
						Repositories: []MappingRepository{{URL: "quay.io/acm-d/other"}},
					},
				},
			}
			return rp, nil
		},
		getSnapshotFn: func(name string) (*Snapshot, error) {
			return &Snapshot{
				Spec: struct {
					Components []SnapshotComponent `json:"components"`
				}{
					Components: []SnapshotComponent{
						{Name: "hypershift-release-mce-50", ContainerImage: "quay.io/redhat/img@sha256:abc123"},
					},
				},
			}, nil
		},
	}

	releases := []Release{
		{
			Metadata: ObjectMeta{
				Name:   "rel-1",
				Labels: map[string]string{"appstudio.openshift.io/component": "hypershift-release-mce-50"},
			},
			Spec: ReleaseSpec{ReleasePlan: "dev-pub", Snapshot: "snap-1"},
		},
	}

	images := ResolveDestImages(q, releases)
	if images["rel-1"] != "" {
		t.Errorf("expected empty dest image, got %q", images["rel-1"])
	}
}
