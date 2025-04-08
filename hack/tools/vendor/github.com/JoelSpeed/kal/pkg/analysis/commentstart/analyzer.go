package commentstart

import (
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"github.com/JoelSpeed/kal/pkg/analysis/helpers/extractjsontags"
	"github.com/JoelSpeed/kal/pkg/analysis/helpers/inspector"
	"github.com/JoelSpeed/kal/pkg/analysis/helpers/markers"
	"golang.org/x/tools/go/analysis"
)

const name = "commentstart"

var (
	errCouldNotGetInspector = errors.New("could not get inspector")
)

// Analyzer is the analyzer for the commentstart package.
// It checks that all struct fields in an API have a godoc, and that the godoc starts with the serialised field name.
var Analyzer = &analysis.Analyzer{
	Name:     name,
	Doc:      "Check that all struct fields in an API have a godoc, and that the godoc starts with the serialised field name",
	Run:      run,
	Requires: []*analysis.Analyzer{inspector.Analyzer},
}

func run(pass *analysis.Pass) (interface{}, error) {
	inspect, ok := pass.ResultOf[inspector.Analyzer].(inspector.Inspector)
	if !ok {
		return nil, errCouldNotGetInspector
	}

	inspect.InspectFields(func(field *ast.Field, stack []ast.Node, jsonTagInfo extractjsontags.FieldTagInfo, markersAccess markers.Markers) {
		checkField(pass, field, jsonTagInfo)
	})

	return nil, nil //nolint:nilnil
}

func checkField(pass *analysis.Pass, field *ast.Field, tagInfo extractjsontags.FieldTagInfo) {
	if tagInfo.Name == "" {
		return
	}

	var fieldName string
	if len(field.Names) > 0 {
		fieldName = field.Names[0].Name
	} else {
		fieldName = types.ExprString(field.Type)
	}

	if field.Doc == nil {
		pass.Reportf(field.Pos(), "field %s is missing godoc comment", fieldName)
		return
	}

	firstLine := field.Doc.List[0]
	if !strings.HasPrefix(firstLine.Text, "// "+tagInfo.Name+" ") {
		if strings.HasPrefix(strings.ToLower(firstLine.Text), strings.ToLower("// "+tagInfo.Name+" ")) {
			// The comment start is correct, apart from the casing, we can fix that.
			pass.Report(analysis.Diagnostic{
				Pos:     firstLine.Pos(),
				Message: fmt.Sprintf("godoc for field %s should start with '%s ...'", fieldName, tagInfo.Name),
				SuggestedFixes: []analysis.SuggestedFix{
					{
						Message: fmt.Sprintf("should replace first word with `%s`", tagInfo.Name),
						TextEdits: []analysis.TextEdit{
							{
								Pos:     firstLine.Pos(),
								End:     firstLine.Pos() + token.Pos(len(tagInfo.Name)) + token.Pos(4),
								NewText: []byte("// " + tagInfo.Name + " "),
							},
						},
					},
				},
			})
		} else {
			pass.Reportf(field.Doc.List[0].Pos(), "godoc for field %s should start with '%s ...'", fieldName, tagInfo.Name)
		}
	}
}
