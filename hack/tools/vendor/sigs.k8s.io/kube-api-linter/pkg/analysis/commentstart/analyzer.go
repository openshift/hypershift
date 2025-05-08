/*
Copyright 2025 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package commentstart

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
	kalerrors "sigs.k8s.io/kube-api-linter/pkg/analysis/errors"
	"sigs.k8s.io/kube-api-linter/pkg/analysis/helpers/extractjsontags"
	"sigs.k8s.io/kube-api-linter/pkg/analysis/helpers/inspector"
	"sigs.k8s.io/kube-api-linter/pkg/analysis/helpers/markers"
)

const name = "commentstart"

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
		return nil, kalerrors.ErrCouldNotGetInspector
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
