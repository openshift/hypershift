package testfuncstructure

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"

	. "github.com/onsi/gomega"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/analysistest"
)

func TestRun(t *testing.T) {
	legacyFixtures := []string{
		"a/legacy/production_test.go|TestReconcileErrors",
		"a/legacy/production_test.go|TestWorker_Run",
		"a/legacyambiguous/production_test.go|TestWorkflow",
		"a/externallegacy/internal_test.go|TestValidateErrors",
		"a/externallegacy/internal_test.go|TestWorker_Run",
		"a/reversecross/external_test.go|TestValidateErrors",
		"a/bothlegacy/internal_test.go|TestValidate",
		"a/bothlegacy/external_test.go|TestValidateErrors",
		// Explicit exceptions must take precedence over baseline accounting.
		"a/exception/production_test.go|TestWorker_Run",
		"a/externalexception/internal_test.go|TestWorker_Run",
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
			"a/unexportedreceiver",
			"a/identity",
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
	t.Run("When an external scenario is baselined and a new internal canonical test is added, it should report the new internal declaration", func(t *testing.T) {
		g := NewWithT(t)
		results := analysistest.Run(&discardTesting{}, testdata, Analyzer, "a/reversecross")

		var externalResult *analysistest.Result
		diagnosticCount := 0
		for _, result := range results {
			diagnosticCount += len(result.Diagnostics)
			if result.Pass.Pkg.Name() == "reversecross_test" {
				externalResult = result
			}
		}
		g.Expect(externalResult).ToNot(BeNil())
		g.Expect(diagnosticCount).To(Equal(1))
		g.Expect(externalResult.Diagnostics).To(HaveLen(1))

		diagnostic := externalResult.Diagnostics[0]
		g.Expect(diagnostic.Message).To(ContainSubstring(`while external test "TestValidateErrors" still exists`))
		position := externalResult.Pass.Fset.Position(diagnostic.Pos)
		internalTestFile := filepath.Join(testdata, "src", "a", "reversecross", "internal_test.go")
		g.Expect(position.Filename).To(Equal(internalTestFile))
		g.Expect(position.Line).To(Equal(declarationLine(t, internalTestFile, "TestValidate")))

		g.Expect(diagnostic.Related).To(HaveLen(1))
		relatedPosition := externalResult.Pass.Fset.Position(diagnostic.Related[0].Pos)
		g.Expect(relatedPosition.Filename).To(Equal(filepath.Join(testdata, "src", "a", "reversecross", "external_test.go")))
		g.Expect(relatedPosition.Line).To(Equal(declarationLine(t, relatedPosition.Filename, "TestValidateErrors")))

		changedInternal := changedLineDiagnostics(externalResult, map[string]map[int]bool{
			position.Filename: {position.Line: true},
		})
		g.Expect(changedInternal).To(HaveLen(1))
		changedExternalOnly := changedLineDiagnostics(externalResult, map[string]map[int]bool{
			relatedPosition.Filename: {relatedPosition.Line: true},
		})
		g.Expect(changedExternalOnly).To(BeEmpty())
	})
	t.Run("When a new test reuses a removed baseline path and name, it should still report the violation", func(t *testing.T) {
		analysistest.Run(t, testdata, Analyzer, "support/util")
	})
	t.Run("When internal and external split tests are both baselined, it should report no diagnostics", func(t *testing.T) {
		g := NewWithT(t)
		results := analysistest.Run(t, testdata, Analyzer, "a/bothlegacy")
		for _, result := range results {
			g.Expect(result.Diagnostics).To(BeEmpty())
		}
	})
	t.Run("When internal tests have explicit exceptions, it should allow an external canonical test and report remaining duplicates", func(t *testing.T) {
		analysistest.Run(t, testdata, Analyzer, "a/externalexception")
	})
	t.Run("When canonical tests have explicit exceptions, it should exclude them from duplicate accounting", func(t *testing.T) {
		analysistest.Run(t, testdata, Analyzer, "a/exception")
	})
	t.Run("When internal tests are only baselined, it should still reject additional external tests", func(t *testing.T) {
		analysistest.Run(t, testdata, Analyzer, "a/externallegacy")
	})
	t.Run("When tests use direct and chained testing.T aliases, it should still enforce one top-level test per production function", func(t *testing.T) {
		analysistest.Run(t, testdata, Analyzer, "a/testingalias")
	})
}

// analysistest matches // want comments against the package being analyzed. The
// reverse cross-package diagnostic intentionally points into the imported
// internal test file, so discard expectation mismatches and assert the result.
type discardTesting struct{}

func (*discardTesting) Errorf(string, ...any) {}

func declarationLine(t *testing.T, filename, name string) int {
	t.Helper()
	fileset := token.NewFileSet()
	file, err := parser.ParseFile(fileset, filename, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", filename, err)
	}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name.Name == name {
			return fileset.Position(function.Name.Pos()).Line
		}
	}
	t.Fatalf("declaration %s not found in %s", name, filename)
	return 0
}

// changedLineDiagnostics mirrors golangci-lint's --new-from-rev position filter.
func changedLineDiagnostics(result *analysistest.Result, changed map[string]map[int]bool) []analysis.Diagnostic {
	var diagnostics []analysis.Diagnostic
	for _, diagnostic := range result.Diagnostics {
		position := result.Pass.Fset.Position(diagnostic.Pos)
		if changed[position.Filename][position.Line] {
			diagnostics = append(diagnostics, diagnostic)
		}
	}
	return diagnostics
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
