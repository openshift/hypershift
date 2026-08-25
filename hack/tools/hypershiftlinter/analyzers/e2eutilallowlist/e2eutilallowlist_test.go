package e2eutilallowlist

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
			name:    "When symbols are in the allowlist, it should produce no diagnostics",
			pattern: "test/e2e/v2/good",
		},
		{
			name:    "When symbols are not in the allowlist, it should produce diagnostics including for aliased imports",
			pattern: "test/e2e/v2/bad",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			analysistest.Run(t, testdata, Analyzer, tt.pattern)
		})
	}
}

func TestIsAllowed(t *testing.T) {
	const utilPkg = "github.com/openshift/hypershift/test/e2e/util"

	tests := []struct {
		name       string
		pkgPath    string
		symbolName string
		want       bool
	}{
		{
			name:       "When symbol is explicitly allowlisted, it should be allowed",
			pkgPath:    utilPkg,
			symbolName: "GetConfig",
			want:       true,
		},
		{
			name:       "When symbol has Version prefix, it should be allowed via wildcard",
			pkgPath:    utilPkg,
			symbolName: "VersionFuture",
			want:       true,
		},
		{
			name:       "When symbol is not in the allowlist, it should not be allowed",
			pkgPath:    utilPkg,
			symbolName: "IsLessThan",
			want:       false,
		},
		{
			name:       "When package is unknown, it should not be allowed",
			pkgPath:    "github.com/other/pkg",
			symbolName: "GetConfig",
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isAllowed(tt.pkgPath, tt.symbolName); got != tt.want {
				t.Errorf("isAllowed(%q, %q) = %v, want %v", tt.pkgPath, tt.symbolName, got, tt.want)
			}
		})
	}
}
