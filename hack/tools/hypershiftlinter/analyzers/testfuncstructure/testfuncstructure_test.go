package testfuncstructure

import (
	"go/parser"
	"go/token"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

func TestRun(t *testing.T) {
	legacyFixtures := []string{
		"a/legacy/production_test.go|TestReconcileErrors",
		"a/legacyambiguous/production_test.go|TestWorkflow",
	}
	for _, fixture := range legacyFixtures {
		legacyExceptions[fixture] = struct{}{}
		defer delete(legacyExceptions, fixture)
	}

	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, Analyzer,
		"a/good",
		"a/split",
		"a/ambiguous",
		"a/external",
		"a/legacy",
		"a/legacyambiguous",
		"a/precedence",
		"a/scenarioonly",
		"test/e2e/good",
		"test/envtest/good",
		"test/integration/good",
		"test/reqserving-e2e/good",
	)
}

func TestHasExcludedBuildTag(t *testing.T) {
	tests := []struct {
		name     string
		source   string
		excluded bool
	}{
		{
			name:     "When an envtest build tag is present, it should exclude the file",
			source:   "//go:build envtest\n\npackage fixture\n",
			excluded: true,
		},
		{
			name:     "When an excluded tag is negated, it should retain the unit test file",
			source:   "//go:build linux && !envtest\n\npackage fixture\n",
			excluded: false,
		},
		{
			name:     "When an excluded tag is optional, it should retain the unit test file",
			source:   "//go:build linux || envtest\n\npackage fixture\n",
			excluded: false,
		},
		{
			name:     "When every build path requires an excluded tag, it should exclude the file",
			source:   "//go:build (linux && envtest) || integration\n\npackage fixture\n",
			excluded: true,
		},
		{
			name:     "When a legacy build constraint requires envtest, it should exclude the file",
			source:   "// +build envtest\n\npackage fixture\n",
			excluded: true,
		},
		{
			name:     "When no build tag is present, it should retain the unit test file",
			source:   "package fixture\n",
			excluded: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), "fixture_test.go", test.source, parser.ParseComments)
			if err != nil {
				t.Fatalf("parse fixture: %v", err)
			}
			if actual := hasExcludedBuildTag(file); actual != test.excluded {
				t.Fatalf("expected excluded=%t, got %t", test.excluded, actual)
			}
		})
	}
}
