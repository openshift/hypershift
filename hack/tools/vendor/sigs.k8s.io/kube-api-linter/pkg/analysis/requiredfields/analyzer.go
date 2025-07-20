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
package requiredfields

import (
	"fmt"
	"go/ast"
	"strings"

	"golang.org/x/tools/go/analysis"
	kalerrors "sigs.k8s.io/kube-api-linter/pkg/analysis/errors"
	"sigs.k8s.io/kube-api-linter/pkg/analysis/helpers/extractjsontags"
	"sigs.k8s.io/kube-api-linter/pkg/analysis/helpers/inspector"
	markershelper "sigs.k8s.io/kube-api-linter/pkg/analysis/helpers/markers"
	"sigs.k8s.io/kube-api-linter/pkg/analysis/utils"
	"sigs.k8s.io/kube-api-linter/pkg/markers"
)

const (
	name = "requiredfields"
)

func init() {
	markershelper.DefaultRegistry().Register(markers.RequiredMarker, markers.KubebuilderRequiredMarker)
}

type analyzer struct {
	pointerPolicy RequiredFieldPointerPolicy
}

// newAnalyzer creates a new analyzer.
func newAnalyzer(cfg *RequiredFieldsConfig) *analysis.Analyzer {
	if cfg == nil {
		cfg = &RequiredFieldsConfig{}
	}

	defaultConfig(cfg)

	a := &analyzer{
		pointerPolicy: cfg.PointerPolicy,
	}

	return &analysis.Analyzer{
		Name:     name,
		Doc:      "Checks that all required fields are not pointers, and do not have the omitempty tag.",
		Run:      a.run,
		Requires: []*analysis.Analyzer{inspector.Analyzer},
	}
}

func (a *analyzer) run(pass *analysis.Pass) (any, error) {
	inspect, ok := pass.ResultOf[inspector.Analyzer].(inspector.Inspector)
	if !ok {
		return nil, kalerrors.ErrCouldNotGetInspector
	}

	inspect.InspectFields(func(field *ast.Field, stack []ast.Node, jsonTagInfo extractjsontags.FieldTagInfo, markersAccess markershelper.Markers) {
		a.checkField(pass, field, markersAccess.FieldMarkers(field), jsonTagInfo)
	})

	return nil, nil //nolint:nilnil
}

func (a *analyzer) checkField(pass *analysis.Pass, field *ast.Field, fieldMarkers markershelper.MarkerSet, fieldTagInfo extractjsontags.FieldTagInfo) {
	fieldName := utils.FieldName(field)
	if fieldName == "" {
		return
	}

	if !fieldMarkers.Has(markers.RequiredMarker) && !fieldMarkers.Has(markers.KubebuilderRequiredMarker) {
		// The field is not marked required, so we don't need to check it.
		return
	}

	if fieldTagInfo.OmitEmpty {
		pass.Report(analysis.Diagnostic{
			Pos:     field.Pos(),
			Message: fmt.Sprintf("field %s is marked as required, but has the omitempty tag", fieldName),
			SuggestedFixes: []analysis.SuggestedFix{
				{
					Message: "should remove the omitempty tag",
					TextEdits: []analysis.TextEdit{
						{
							Pos:     fieldTagInfo.Pos,
							End:     fieldTagInfo.End,
							NewText: []byte(strings.Replace(fieldTagInfo.RawValue, ",omitempty", "", 1)),
						},
					},
				},
			},
		})
	}

	if field.Type == nil {
		// The field has no type? We can't check if it's a pointer.
		return
	}

	if starExpr, ok := field.Type.(*ast.StarExpr); ok {
		var suggestedFixes []analysis.SuggestedFix

		switch a.pointerPolicy {
		case RequiredFieldPointerWarn:
			// Do not suggest a fix.
		case RequiredFieldPointerSuggestFix:
			suggestedFixes = append(suggestedFixes, analysis.SuggestedFix{
				Message: "should remove the pointer",
				TextEdits: []analysis.TextEdit{
					{
						Pos:     starExpr.Pos(),
						End:     starExpr.X.Pos(),
						NewText: nil,
					},
				},
			})
		}

		pass.Report(analysis.Diagnostic{
			Pos:            field.Pos(),
			Message:        fmt.Sprintf("field %s is marked as required, should not be a pointer", fieldName),
			SuggestedFixes: suggestedFixes,
		})
	}
}

func defaultConfig(cfg *RequiredFieldsConfig) {
	if cfg.PointerPolicy == "" {
		cfg.PointerPolicy = RequiredFieldPointerSuggestFix
	}
}
