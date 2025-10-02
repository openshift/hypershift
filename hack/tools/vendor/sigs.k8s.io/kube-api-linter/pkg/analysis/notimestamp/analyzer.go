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

package notimestamp

import (
	"fmt"
	"go/ast"
	"go/token"
	"regexp"
	"strings"

	"golang.org/x/tools/go/analysis"
	kalerrors "sigs.k8s.io/kube-api-linter/pkg/analysis/errors"
	"sigs.k8s.io/kube-api-linter/pkg/analysis/helpers/extractjsontags"
	"sigs.k8s.io/kube-api-linter/pkg/analysis/helpers/inspector"
	markershelper "sigs.k8s.io/kube-api-linter/pkg/analysis/helpers/markers"
	"sigs.k8s.io/kube-api-linter/pkg/analysis/utils"
)

const name = "notimestamp"

// Analyzer is the analyzer for the notimestamp package.
// It checks that no struct fields named 'timestamp', or that contain timestamp as a
// substring are present.
var Analyzer = &analysis.Analyzer{
	Name:     name,
	Doc:      "Suggest the usage of the term 'time' over 'timestamp'",
	Run:      run,
	Requires: []*analysis.Analyzer{inspector.Analyzer},
}

// case-insensitive regular expression to match 'timestamp' string in field or json tag.
var timeStampRegEx = regexp.MustCompile("(?i)timestamp")

func run(pass *analysis.Pass) (any, error) {
	inspect, ok := pass.ResultOf[inspector.Analyzer].(inspector.Inspector)
	if !ok {
		return nil, kalerrors.ErrCouldNotGetInspector
	}

	inspect.InspectFields(func(field *ast.Field, stack []ast.Node, jsonTagInfo extractjsontags.FieldTagInfo, markersAccess markershelper.Markers) {
		checkFieldsAndTags(pass, field, jsonTagInfo)
	})

	return nil, nil //nolint:nilnil
}

func checkFieldsAndTags(pass *analysis.Pass, field *ast.Field, tagInfo extractjsontags.FieldTagInfo) {
	fieldName := utils.FieldName(field)
	if fieldName == "" {
		return
	}

	var suggestedFixes []analysis.SuggestedFix

	// check if filed name contains timestamp in it.
	fieldReplacementName := timeStampRegEx.ReplaceAllString(fieldName, "Time")
	if fieldReplacementName != fieldName {
		suggestedFixes = append(suggestedFixes, analysis.SuggestedFix{
			Message: fmt.Sprintf("replace %s with %s", fieldName, fieldReplacementName),
			TextEdits: []analysis.TextEdit{
				{
					Pos:     field.Pos(),
					NewText: []byte(fieldReplacementName),
					End:     field.Pos() + token.Pos(len(fieldName)),
				},
			},
		})
	}

	// check if the tag contains timestamp in it.
	tagReplacementName := timeStampRegEx.ReplaceAllString(tagInfo.Name, "Time")
	if strings.HasPrefix(strings.ToLower(tagInfo.Name), "time") {
		// If the tag starts with 'timeStamp', the replacement should be 'time' not 'Time'.
		tagReplacementName = timeStampRegEx.ReplaceAllString(tagInfo.Name, "time")
	}

	if tagReplacementName != tagInfo.Name {
		suggestedFixes = append(suggestedFixes, analysis.SuggestedFix{
			Message: fmt.Sprintf("replace %s json tag with %s", tagInfo.Name, tagReplacementName),
			TextEdits: []analysis.TextEdit{
				{
					Pos:     tagInfo.Pos,
					NewText: []byte(tagReplacementName),
					End:     tagInfo.Pos + token.Pos(len(tagInfo.Name)),
				},
			},
		})
	}

	if len(suggestedFixes) > 0 {
		pass.Report(analysis.Diagnostic{
			Pos:            field.Pos(),
			Message:        fmt.Sprintf("field %s: prefer use of the term time over timestamp", fieldName),
			SuggestedFixes: suggestedFixes,
		})
	}
}
