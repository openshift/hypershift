//go:build e2ev2

package lifecycle

import (
	"testing"
)

func TestDeriveClusterName(t *testing.T) {
	tests := []struct {
		name      string
		jobID     string
		variant   string
		otherJob  string
		otherVar  string
		wantSame  bool
		wantEmpty bool
	}{
		{
			name:     "When the same inputs are provided, it should return the same name",
			jobID:    "job-123",
			variant:  "public",
			otherJob: "job-123",
			otherVar: "public",
			wantSame: true,
		},
		{
			name:     "When different job IDs are provided, it should return different names",
			jobID:    "job-123",
			variant:  "public",
			otherJob: "job-456",
			otherVar: "public",
			wantSame: false,
		},
		{
			name:     "When different variants are provided, it should return different names",
			jobID:    "job-123",
			variant:  "public",
			otherJob: "job-123",
			otherVar: "upgrade",
			wantSame: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DeriveClusterName(tt.jobID, tt.variant)
			if got == "" {
				t.Fatal("expected non-empty cluster name")
			}
			other := DeriveClusterName(tt.otherJob, tt.otherVar)
			if tt.wantSame && got != other {
				t.Errorf("expected same name, got %q and %q", got, other)
			}
			if !tt.wantSame && got == other {
				t.Errorf("expected different names, both got %q", got)
			}
		})
	}
}

func TestFilterClusterSpecs(t *testing.T) {
	specs := []ClusterSpec{
		{Variant: "public", OutputFile: "cluster-name-public"},
		{Variant: "private", OutputFile: "cluster-name-private"},
		{Variant: "upgrade", OutputFile: "cluster-name-upgrade"},
	}

	tests := []struct {
		name         string
		variants     string
		wantCount    int
		wantVariants []string
	}{
		{
			name:      "When an empty filter is provided, it should return all specs",
			variants:  "",
			wantCount: 3,
		},
		{
			name:         "When a single variant is specified, it should return only that variant",
			variants:     "public",
			wantCount:    1,
			wantVariants: []string{"public"},
		},
		{
			name:         "When multiple variants are specified, it should return all matching variants",
			variants:     "public,upgrade",
			wantCount:    2,
			wantVariants: []string{"public", "upgrade"},
		},
		{
			name:      "When variants have surrounding whitespace, it should trim and match",
			variants:  " public , upgrade ",
			wantCount: 2,
		},
		{
			name:      "When a non-existent variant is specified, it should return no specs",
			variants:  "nonexistent",
			wantCount: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FilterClusterSpecs(specs, tt.variants)
			if len(got) != tt.wantCount {
				t.Errorf("expected %d specs, got %d", tt.wantCount, len(got))
			}
			if len(tt.wantVariants) > 0 {
				have := map[string]bool{}
				for _, s := range got {
					have[s.Variant] = true
				}
				for _, v := range tt.wantVariants {
					if !have[v] {
						t.Errorf("expected variant %q in results", v)
					}
				}
			}
		})
	}
}

func TestFilterTestMatrix(t *testing.T) {
	matrix := TestMatrix{
		Parallel: []TestGroup{
			{Name: "public-tests", ClusterFile: "cluster-name-public", LabelFilter: "public"},
			{Name: "private-tests", ClusterFile: "cluster-name-private", LabelFilter: "private"},
		},
		Sequential: []SequentialGroup{
			{
				Name: "upgrade-and-chaos",
				Steps: []TestGroup{
					{Name: "upgrade", ClusterFile: "cluster-name-upgrade", LabelFilter: "upgrade"},
					{Name: "chaos", ClusterFile: "cluster-name-upgrade", LabelFilter: "chaos"},
				},
			},
		},
	}

	tests := []struct {
		name           string
		specs          []ClusterSpec
		wantParallel   int
		wantSequential int
		wantSteps      int
	}{
		{
			name: "When all cluster files are present, it should keep all groups",
			specs: []ClusterSpec{
				{Variant: "public", OutputFile: "cluster-name-public"},
				{Variant: "private", OutputFile: "cluster-name-private"},
				{Variant: "upgrade", OutputFile: "cluster-name-upgrade"},
			},
			wantParallel:   2,
			wantSequential: 1,
			wantSteps:      2,
		},
		{
			name: "When a parallel group's cluster file is missing, it should drop that group",
			specs: []ClusterSpec{
				{Variant: "public", OutputFile: "cluster-name-public"},
				{Variant: "upgrade", OutputFile: "cluster-name-upgrade"},
			},
			wantParallel:   1,
			wantSequential: 1,
			wantSteps:      2,
		},
		{
			name: "When all steps' cluster files are missing, it should drop the sequential group",
			specs: []ClusterSpec{
				{Variant: "public", OutputFile: "cluster-name-public"},
				{Variant: "private", OutputFile: "cluster-name-private"},
			},
			wantParallel:   2,
			wantSequential: 0,
		},
		{
			name:           "When no specs are provided, it should return an empty matrix",
			specs:          nil,
			wantParallel:   0,
			wantSequential: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FilterTestMatrix(matrix, tt.specs)
			if len(got.Parallel) != tt.wantParallel {
				t.Errorf("expected %d parallel groups, got %d", tt.wantParallel, len(got.Parallel))
			}
			if len(got.Sequential) != tt.wantSequential {
				t.Errorf("expected %d sequential groups, got %d", tt.wantSequential, len(got.Sequential))
			}
			if tt.wantSteps > 0 && len(got.Sequential) > 0 {
				if len(got.Sequential[0].Steps) != tt.wantSteps {
					t.Errorf("expected %d steps, got %d", tt.wantSteps, len(got.Sequential[0].Steps))
				}
			}
		})
	}
}
