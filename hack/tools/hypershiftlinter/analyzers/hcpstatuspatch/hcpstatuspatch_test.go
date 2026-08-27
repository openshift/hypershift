package hcpstatuspatch

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

func TestAnalyzer(t *testing.T) {
	testdata := analysistest.TestData()

	tests := []struct {
		name    string
		pattern string
	}{
		{
			name:    "When patches use statuspatching, optimistic lock, or non-HCP types, it should produce no diagnostics",
			pattern: "a/good",
		},
		{
			name:    "When HostedControlPlane status is updated or patched without an optimistic lock, it should produce diagnostics",
			pattern: "a/bad",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			analysistest.Run(t, testdata, Analyzer, tt.pattern)
		})
	}
}
