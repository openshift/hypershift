package testfuncstructure

import (
	"go/token"
	"go/types"
	"path/filepath"

	"golang.org/x/tools/go/analysis"
)

// productionMetadata preserves declaration provenance when an external test
// package imports the package under test, including its internal test variant.
type productionMetadata struct {
	SymbolIDs []string
	Tests     []testMetadata
}

type testMetadata struct {
	SymbolID   string
	Name       string
	Filename   string
	Kind       resolutionKind
	Priority   int
	Suppressed bool
}

// AFact marks productionMetadata as a serializable analysis fact.
func (*productionMetadata) AFact() {}

// String describes the fact in analysistest fixture expectations.
func (*productionMetadata) String() string {
	return "testfuncstructure production symbols"
}

func exportProductionMetadata(pass *analysis.Pass, symbols []*productionSymbol, groups map[string][]*testFunction) {
	metadata := &productionMetadata{SymbolIDs: make([]string, 0, len(symbols))}
	for _, symbol := range symbols {
		metadata.SymbolIDs = append(metadata.SymbolIDs, symbol.id)
		for _, test := range groups[symbol.id] {
			metadata.Tests = append(metadata.Tests, testMetadata{
				SymbolID:   symbol.id,
				Name:       test.name,
				Filename:   filepath.Base(test.filename),
				Kind:       test.resolution.kind,
				Priority:   test.resolution.priority,
				Suppressed: test.suppressed,
			})
		}
	}
	pass.ExportPackageFact(metadata)
}

func collectImportedTests(pass *analysis.Pass, symbolsByID map[string]*productionSymbol) []*testFunction {
	target := externalPackageUnderTest(pass)
	if target == nil || len(pass.Files) == 0 {
		return nil
	}
	var metadata productionMetadata
	if !pass.ImportPackageFact(target, &metadata) {
		return nil
	}

	directory := filepath.Dir(pass.Fset.Position(pass.Files[0].Pos()).Filename)
	tests := make([]*testFunction, 0, len(metadata.Tests))
	for _, recorded := range metadata.Tests {
		symbol := symbolsByID[recorded.SymbolID]
		if symbol == nil {
			continue
		}

		filename := filepath.Join(directory, recorded.Filename)
		namePos, nameEnd := token.NoPos, token.NoPos
		if function, ok := target.Scope().Lookup(recorded.Name).(*types.Func); ok {
			if file := pass.Fset.File(function.Pos()); file != nil {
				filename = file.Name()
				namePos = function.Pos()
				nameEnd = min(namePos+token.Pos(len(recorded.Name)), file.Pos(file.Size()))
			}
		}
		tests = append(tests, &testFunction{
			filename:   filename,
			name:       recorded.Name,
			namePos:    namePos,
			nameEnd:    nameEnd,
			imported:   true,
			suppressed: recorded.Suppressed,
			resolution: resolution{
				kind:     recorded.Kind,
				target:   symbol,
				priority: recorded.Priority,
			},
		})
	}
	return tests
}
