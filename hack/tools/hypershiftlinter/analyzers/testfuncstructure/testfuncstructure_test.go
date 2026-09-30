package testfuncstructure

import (
	"go/parser"
	"go/token"
	"testing"

	. "github.com/onsi/gomega"

	"golang.org/x/tools/go/analysis/analysistest"
)

func TestRun(t *testing.T) {
	legacyFixtures := []string{
		"a/legacy/production_test.go|TestReconcileErrors",
		"a/legacyambiguous/production_test.go|TestWorkflow",
	}
	for _, fixture := range legacyFixtures {
		legacyExceptions.Insert(fixture)
		defer legacyExceptions.Delete(fixture)
	}

	testdata := analysistest.TestData()
	t.Run("When unit tests are analyzed, it should distinguish supported mappings and structure violations", func(t *testing.T) {
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
	})
	t.Run("When external tests import only a namesake dependency, it should ignore that dependency", func(t *testing.T) {
		analysistest.Run(t, testdata, Analyzer, "a/unrelated/common")
	})
	t.Run("When external tests call helpers or generated code, it should not map those calls to production targets", func(t *testing.T) {
		analysistest.Run(t, testdata, Analyzer, "a/provenance")
	})
	t.Run("When internal and external tests share production targets, it should report each external duplicate with the internal location", func(t *testing.T) {
		g := NewWithT(t)
		results := analysistest.Run(t, testdata, Analyzer, "a/crosspackage")
		foundExternalPackage := false
		for _, result := range results {
			if result.Pass.Pkg.Name() != "crosspackage_test" {
				continue
			}
			foundExternalPackage = true
			g.Expect(result.Diagnostics).To(HaveLen(2))
			for _, diagnostic := range result.Diagnostics {
				g.Expect(diagnostic.Related).To(HaveLen(1))
				position := result.Pass.Fset.Position(diagnostic.Related[0].Pos)
				g.Expect(position.Filename).To(HaveSuffix("internal_test.go"))
			}
		}
		g.Expect(foundExternalPackage).To(BeTrue())
	})
	t.Run("When a new test reuses a removed baseline path and name, it should still report the violation", func(t *testing.T) {
		analysistest.Run(t, testdata, Analyzer, "support/util")
	})
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
