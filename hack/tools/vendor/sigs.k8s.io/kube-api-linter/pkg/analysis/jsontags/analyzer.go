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
	"slices"
	"strings"
	"unicode"

	"golang.org/x/tools/go/analysis"
	kalerrors "sigs.k8s.io/kube-api-linter/pkg/analysis/errors"
	"sigs.k8s.io/kube-api-linter/pkg/analysis/helpers/extractjsontags"
	"sigs.k8s.io/kube-api-linter/pkg/analysis/helpers/inspector"
	"sigs.k8s.io/kube-api-linter/pkg/analysis/helpers/markers"
)

const (
	// camelCaseRegex is a regular expression that matches camel case strings.
	camelCaseRegex = "^[a-z][a-z0-9]*(?:[A-Z][a-z0-9]*)*$"

	name = "jsontags"
)

type analyzer struct {
	jsonTagRegex   *regexp.Regexp
	fieldNameMatch FieldNameMatchPolicy
}

// newAnalyzer creates a new analyzer with the given json tag regex.
func newAnalyzer(cfg *JSONTagsConfig) (*analysis.Analyzer, error) {
	if cfg == nil {
		cfg = &JSONTagsConfig{}
	}

	defaultConfig(cfg)

	jsonTagRegex, err := regexp.Compile(cfg.JSONTagRegex)
	if err != nil {
		return nil, fmt.Errorf("could not compile json tag regex: %w", err)
	}

	a := &analyzer{
		jsonTagRegex:   jsonTagRegex,
		fieldNameMatch: cfg.FieldNameMatch,
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

	inspect.InspectFieldsIncludingListTypes(func(field *ast.Field, jsonTagInfo extractjsontags.FieldTagInfo, _ markers.Markers, qualifiedFieldName string) {
		a.checkField(pass, field, jsonTagInfo, qualifiedFieldName)
	})

	return nil, nil //nolint:nilnil
}

func (a *analyzer) checkField(pass *analysis.Pass, field *ast.Field, tagInfo extractjsontags.FieldTagInfo, qualifiedFieldName string) {
	embedded := false
	prefix := "field %s"

	if len(field.Names) == 0 || field.Names[0] == nil {
		embedded = true
		prefix = "embedded field %s"
	}

	prefix = fmt.Sprintf(prefix, qualifiedFieldName)

	if tagInfo.Missing {
		pass.Reportf(field.Pos(), "%s is missing json tag", prefix)
		return
	}

	if tagInfo.Inline {
		if !embedded {
			pass.Reportf(field.Pos(), "%s has inline json tag, but is not embedded", prefix)
		}

		return
	}

	if tagInfo.Name == "" {
		if !embedded {
			pass.Reportf(field.Pos(), "%s has empty json tag", prefix)
		}

		return
	}

	if !a.jsonTagRegex.MatchString(tagInfo.Name) {
		pass.Reportf(field.Pos(), "%s json tag does not match pattern %q: %s", prefix, a.jsonTagRegex.String(), tagInfo.Name)
	}

	a.checkFieldNameMatch(pass, field, tagInfo, prefix)
}

func (a *analyzer) checkFieldNameMatch(pass *analysis.Pass, field *ast.Field, tagInfo extractjsontags.FieldTagInfo, prefix string) {
	if len(field.Names) == 0 || field.Names[0] == nil {
		return
	}

	fieldName := field.Names[0].Name
	if jsonTagMatchesFieldName(fieldName, tagInfo.Name) {
		return
	}

	expectedTagName := expectedJSONTagName(fieldName)
	message := fmt.Sprintf("%s json tag should match the camelCase field name %q: got %q", prefix, expectedTagName, tagInfo.Name)

	switch a.fieldNameMatch {
	case FieldNameMatchPolicyIgnore:
		return
	case FieldNameMatchPolicyWarn:
		pass.Reportf(field.Pos(), "%s", message)
	case FieldNameMatchPolicySuggestFix:
		pass.Report(analysis.Diagnostic{
			Pos:     field.Pos(),
			Message: message,
			SuggestedFixes: []analysis.SuggestedFix{
				{
					Message: fmt.Sprintf("replace json tag name with %q", expectedTagName),
					TextEdits: []analysis.TextEdit{
						{
							Pos:     tagInfo.Pos,
							End:     tagInfo.End,
							NewText: []byte(strings.Replace(tagInfo.RawValue, tagInfo.Name, expectedTagName, 1)),
						},
					},
				},
			},
		})
	default:
		panic(fmt.Sprintf("unknown field name match policy: %s", a.fieldNameMatch))
	}
}

func defaultConfig(cfg *JSONTagsConfig) {
	if cfg.JSONTagRegex == "" {
		cfg.JSONTagRegex = camelCaseRegex
	}

	if cfg.FieldNameMatch == "" {
		cfg.FieldNameMatch = FieldNameMatchPolicyIgnore
	}
}

func expectedJSONTagName(fieldName string) string {
	words := splitIdentifierWords(fieldName)
	if len(words) == 0 {
		return ""
	}

	var b strings.Builder

	b.WriteString(words[0])

	for _, word := range words[1:] {
		r := []rune(word)
		if len(r) == 0 {
			continue
		}

		r[0] = unicode.ToUpper(r[0])
		b.WriteString(string(r))
	}

	return b.String()
}

func jsonTagMatchesFieldName(fieldName, jsonTagName string) bool {
	return slices.Equal(splitIdentifierWords(fieldName), splitIdentifierWords(jsonTagName))
}

func splitIdentifierWords(in string) []string {
	if in == "" {
		return nil
	}

	runes := []rune(in)
	words := []string{}
	start := 0

	appendWord := func(end int) {
		if end <= start {
			return
		}

		words = append(words, strings.ToLower(string(runes[start:end])))
	}

	for i := 1; i < len(runes); i++ {
		if !isWordBoundary(runes, i, start) {
			continue
		}

		appendWord(i)
		start = i
	}

	appendWord(len(runes))

	return words
}

func isWordBoundary(runes []rune, i, start int) bool {
	prev := runes[i-1]
	curr := runes[i]

	switch {
	case unicode.IsLower(prev) && unicode.IsUpper(curr):
		return true
	case unicode.IsLetter(prev) && unicode.IsDigit(curr):
		return true
	case unicode.IsDigit(prev) && unicode.IsUpper(curr):
		return true
	case unicode.IsUpper(prev) && unicode.IsUpper(curr):
		return isAcronymBoundary(runes, i, start)
	}

	return false
}

func isAcronymBoundary(runes []rune, i, start int) bool {
	if i+1 >= len(runes) {
		return false
	}

	next := runes[i+1]

	if !unicode.IsLower(next) || i-start <= 1 {
		return false
	}

	// Keep pluralized acronyms together: WWIDs -> wwids, WWIDsBad -> wwidsBad, URLs -> urls.
	pluralizedAcronym := next == 's' && (i+2 == len(runes) || unicode.IsUpper(runes[i+2]))

	return !pluralizedAcronym
}
