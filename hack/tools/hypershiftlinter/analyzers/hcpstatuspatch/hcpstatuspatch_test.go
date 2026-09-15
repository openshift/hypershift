package hcpstatuspatch

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

func TestRun(t *testing.T) {
	t.Parallel()

	testdata := analysistest.TestData()

	tests := []struct {
		name    string
		pattern string
	}{
		{
			name:    "When patches use statuspatching, optimistic lock, or unrelated types, it should produce no diagnostics",
			pattern: "a/good",
		},
		{
			name:    "When HostedCluster or HostedControlPlane status is updated or patched without an optimistic lock, it should produce diagnostics",
			pattern: "a/bad",
		},
		{
			name:    "When a patch is passed as a function parameter, it should produce no diagnostics",
			pattern: "a/interprocedural",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(subtest *testing.T) {
			subtest.Parallel()
			analysistest.Run(subtest, testdata, Analyzer, tt.pattern)
		})
	}
}
