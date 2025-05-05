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
package jsontags

import (
	"fmt"
	"go/ast"
	"regexp"

	kalerrors "sigs.k8s.io/kube-api-linter/pkg/analysis/errors"
	"sigs.k8s.io/kube-api-linter/pkg/analysis/helpers/extractjsontags"
	"sigs.k8s.io/kube-api-linter/pkg/analysis/helpers/inspector"
	"sigs.k8s.io/kube-api-linter/pkg/analysis/helpers/markers"
	"sigs.k8s.io/kube-api-linter/pkg/config"

	"golang.org/x/tools/go/analysis"
)

const (
	// camelCaseRegex is a regular expression that matches camel case strings.
	camelCaseRegex = "^[a-z][a-z0-9]*(?:[A-Z][a-z0-9]*)*$"

	name = "jsontags"
)

type analyzer struct {
	jsonTagRegex *regexp.Regexp
}

// newAnalyzer creates a new analyzer with the given json tag regex.
func newAnalyzer(cfg config.JSONTagsConfig) (*analysis.Analyzer, error) {
	defaultConfig(&cfg)

	jsonTagRegex, err := regexp.Compile(cfg.JSONTagRegex)
	if err != nil {
		return nil, fmt.Errorf("could not compile json tag regex: %w", err)
	}

	a := &analyzer{
		jsonTagRegex: jsonTagRegex,
	}

	return &analysis.Analyzer{
		Name:     name,
		Doc:      "Check that all struct fields in an API are tagged with json tags",
		Run:      a.run,
		Requires: []*analysis.Analyzer{inspector.Analyzer},
	}, nil
}

func (a *analyzer) run(pass *analysis.Pass) (any, error) {
	inspect, ok := pass.ResultOf[inspector.Analyzer].(inspector.Inspector)
	if !ok {
		return nil, kalerrors.ErrCouldNotGetInspector
	}

	inspect.InspectFields(func(field *ast.Field, stack []ast.Node, jsonTagInfo extractjsontags.FieldTagInfo, markersAccess markers.Markers) {
		a.checkField(pass, field, jsonTagInfo)
	})

	return nil, nil //nolint:nilnil
}

func (a *analyzer) checkField(pass *analysis.Pass, field *ast.Field, tagInfo extractjsontags.FieldTagInfo) {
	var prefix string
	if len(field.Names) > 0 && field.Names[0] != nil {
		prefix = fmt.Sprintf("field %s", field.Names[0].Name)
	} else if ident, ok := field.Type.(*ast.Ident); ok {
		prefix = fmt.Sprintf("embedded field %s", ident.Name)
	}

	if tagInfo.Missing {
		pass.Reportf(field.Pos(), "%s is missing json tag", prefix)
		return
	}

	if tagInfo.Inline {
		return
	}

	if tagInfo.Name == "" {
		pass.Reportf(field.Pos(), "%s has empty json tag", prefix)
		return
	}

	matched := a.jsonTagRegex.Match([]byte(tagInfo.Name))
	if !matched {
		pass.Reportf(field.Pos(), "%s json tag does not match pattern %q: %s", prefix, a.jsonTagRegex.String(), tagInfo.Name)
	}
}

func defaultConfig(cfg *config.JSONTagsConfig) {
	if cfg.JSONTagRegex == "" {
		cfg.JSONTagRegex = camelCaseRegex
	}
}
