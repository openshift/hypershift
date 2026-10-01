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
			name:    "When v2 code has no v1 util imports, it should produce no diagnostics",
			pattern: "test/e2e/v2/good",
		},
		{
			name:    "When v2 code references v1 util symbols, it should produce diagnostics including for aliased imports",
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
			name:       "When allowlist is empty, no symbol from v1 util should be allowed",
			pkgPath:    utilPkg,
			symbolName: "GetConfig",
			want:       false,
		},
		{
			name:       "When allowlist is empty, Version-prefixed symbols are also disallowed",
			pkgPath:    utilPkg,
			symbolName: "VersionFuture",
			want:       false,
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
