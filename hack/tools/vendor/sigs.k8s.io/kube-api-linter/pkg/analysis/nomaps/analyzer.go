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
package nomaps

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/analysis"
	kalerrors "sigs.k8s.io/kube-api-linter/pkg/analysis/errors"
	"sigs.k8s.io/kube-api-linter/pkg/analysis/helpers/extractjsontags"
	"sigs.k8s.io/kube-api-linter/pkg/analysis/helpers/inspector"
	"sigs.k8s.io/kube-api-linter/pkg/analysis/helpers/markers"
	"sigs.k8s.io/kube-api-linter/pkg/analysis/utils"
)

const (
	name = "nomaps"
)

type analyzer struct {
	policy NoMapsPolicy
}

// newAnalyzer creates a new analyzer.
func newAnalyzer(cfg *NoMapsConfig) *analysis.Analyzer {
	if cfg == nil {
		cfg = &NoMapsConfig{}
	}

	defaultConfig(cfg)

	a := &analyzer{
		policy: cfg.Policy,
	}

	return &analysis.Analyzer{
		Name:     name,
		Doc:      "Checks for usage of map types. Maps are discouraged apart from `map[string]string` which is used for labels and annotations. Use a list of named objects instead.",
		Run:      a.run,
		Requires: []*analysis.Analyzer{inspector.Analyzer},
	}
}

func (a *analyzer) run(pass *analysis.Pass) (any, error) {
	inspect, ok := pass.ResultOf[inspector.Analyzer].(inspector.Inspector)
	if !ok {
		return nil, kalerrors.ErrCouldNotGetInspector
	}

	inspect.InspectFields(func(field *ast.Field, stack []ast.Node, jsonTagInfo extractjsontags.FieldTagInfo, markersAccess markers.Markers) {
		a.checkField(pass, field)
	})

	return nil, nil //nolint:nilnil
}

func (a *analyzer) checkField(pass *analysis.Pass, field *ast.Field) {
	underlyingType := pass.TypesInfo.TypeOf(field.Type).Underlying()

	if ptr, ok := underlyingType.(*types.Pointer); ok {
		underlyingType = ptr.Elem().Underlying()
	}

	m, ok := underlyingType.(*types.Map)
	if !ok {
		return
	}

	if a.policy == NoMapsEnforce {
		report(pass, field.Pos(), utils.FieldName(field))
		return
	}

	if a.policy == NoMapsAllowStringToStringMaps {
		if types.AssignableTo(m.Elem().Underlying(), types.Typ[types.String]) &&
			types.AssignableTo(m.Key().Underlying(), types.Typ[types.String]) {
			return
		}

		report(pass, field.Pos(), utils.FieldName(field))
	}

	if a.policy == NoMapsIgnore {
		key := m.Key().Underlying()
		_, ok := key.(*types.Basic)

		elm := m.Elem().Underlying()
		_, ok2 := elm.(*types.Basic)

		if ok && ok2 {
			return
		}

		report(pass, field.Pos(), utils.FieldName(field))
	}
}

func report(pass *analysis.Pass, pos token.Pos, fieldName string) {
	pass.Report(analysis.Diagnostic{
		Pos:     pos,
		Message: fmt.Sprintf("%s should not use a map type, use a list type with a unique name/identifier instead", fieldName),
	})
}

func defaultConfig(cfg *NoMapsConfig) {
	if cfg.Policy == "" {
		cfg.Policy = NoMapsAllowStringToStringMaps
	}
}
