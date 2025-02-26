package nophase

import (
	"errors"
	"go/ast"
	"strings"

	"github.com/JoelSpeed/kal/pkg/analysis/helpers/extractjsontags"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

const name = "nophase"

var (
	errCouldNotGetInspector = errors.New("could not get inspector")
	errCouldNotGetJSONTags  = errors.New("could not get json tags")
)

// Analyzer is the analyzer for the nophase package.
// It checks that no struct fields named 'phase', or that contain phase as a
// substring are present.
var Analyzer = &analysis.Analyzer{
	Name:     name,
	Doc:      "phase fields are deprecated and conditions should be preferred, avoid phase like enum fields",
	Run:      run,
	Requires: []*analysis.Analyzer{inspect.Analyzer, extractjsontags.Analyzer},
}

func run(pass *analysis.Pass) (interface{}, error) {
	inspect, ok := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	if !ok {
		return nil, errCouldNotGetInspector
	}

	jsonTags, ok := pass.ResultOf[extractjsontags.Analyzer].(extractjsontags.StructFieldTags)
	if !ok {
		return nil, errCouldNotGetJSONTags
	}

	// Filter to fields so that we can iterate over fields in a struct.
	nodeFilter := []ast.Node{
		(*ast.Field)(nil),
	}

	// Preorder visits all the nodes of the AST in depth-first order. It calls
	// f(n) for each node n before it visits n's children.
	//
	// We use the filter defined above, ensuring we only look at struct fields.
	inspect.Preorder(nodeFilter, func(n ast.Node) {
		field, ok := n.(*ast.Field)
		if !ok {
			return
		}

		if field == nil || len(field.Names) == 0 {
			return
		}

		fieldName := field.Names[0].Name

		// First check if the struct field name contains 'phase'
		if strings.Contains(strings.ToLower(fieldName), "phase") {
			pass.Reportf(field.Pos(),
				"field %s: phase fields are deprecated and conditions should be preferred, avoid phase like enum fields",
				fieldName,
			)

			return
		}

		// Then check if the json serialization of the field contains 'phase'
		tagInfo := jsonTags.FieldTags(field)

		if strings.Contains(strings.ToLower(tagInfo.Name), "phase") {
			pass.Reportf(field.Pos(),
				"field %s: phase fields are deprecated and conditions should be preferred, avoid phase like enum fields",
				fieldName,
			)
		}
	})

	return nil, nil //nolint:nilnil
}
