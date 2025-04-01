package inspector

import (
	"errors"
	"reflect"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	astinspector "golang.org/x/tools/go/ast/inspector"

	"github.com/JoelSpeed/kal/pkg/analysis/helpers/extractjsontags"
	"github.com/JoelSpeed/kal/pkg/analysis/helpers/markers"
)

const name = "inspector"

var (
	errCouldNotGetInspector = errors.New("could not get inspector")
	errCouldNotGetJSONTags  = errors.New("could not get json tags")
	errCouldNotGetMarkers   = errors.New("could not get markers")
)

// Analyzer is the analyzer for the inspector package.
// It provides common functionality for analyzers that need to inspect fields and struct.
// Abstracting away filtering of fields that the analyzers should and shouldn't be worrying about.
var Analyzer = &analysis.Analyzer{
	Name:       name,
	Doc:        "Provides common functionality for analyzers that need to inspect fields and struct",
	Run:        run,
	Requires:   []*analysis.Analyzer{inspect.Analyzer, extractjsontags.Analyzer, markers.Analyzer},
	ResultType: reflect.TypeOf(newInspector(nil, nil, nil)),
}

func run(pass *analysis.Pass) (interface{}, error) {
	astinspector, ok := pass.ResultOf[inspect.Analyzer].(*astinspector.Inspector)
	if !ok {
		return nil, errCouldNotGetInspector
	}

	jsonTags, ok := pass.ResultOf[extractjsontags.Analyzer].(extractjsontags.StructFieldTags)
	if !ok {
		return nil, errCouldNotGetJSONTags
	}

	markersAccess, ok := pass.ResultOf[markers.Analyzer].(markers.Markers)
	if !ok {
		return nil, errCouldNotGetMarkers
	}

	return newInspector(astinspector, jsonTags, markersAccess), nil
}
