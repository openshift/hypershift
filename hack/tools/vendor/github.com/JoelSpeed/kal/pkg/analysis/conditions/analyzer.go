package conditions

import (
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"strings"

	"github.com/JoelSpeed/kal/pkg/analysis/helpers/extractjsontags"
	"github.com/JoelSpeed/kal/pkg/analysis/helpers/markers"
	"github.com/JoelSpeed/kal/pkg/config"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

const (
	name = "conditions"

	listTypeMarkerID      = "listType"
	listMapKeyMarkerID    = "listMapKey"
	patchStrategyMarkerID = "patchStrategy"
	patchMergeKeyMarkerID = "patchMergeKey"

	listTypeMap       = "listType=map"
	listMapKeyType    = "listMapKey=type"
	patchStrategy     = "patchStrategy=merge"
	patchMergeKeyType = "patchMergeKey=type"
	optional          = "optional"

	expectedTagWithProtobufFmt = "`json:\"conditions,omitempty\" patchStrategy:\"merge\" patchMergeKey:\"type\" protobuf:\"bytes,%d,rep,name=conditions\"`"
	expectedTagWithoutProtobuf = "`json:\"conditions,omitempty\" patchStrategy:\"merge\" patchMergeKey:\"type\"`"
)

func init() {
	markers.DefaultRegistry().Register(
		listTypeMarkerID,
		listMapKeyMarkerID,
		patchStrategyMarkerID,
		patchMergeKeyMarkerID,
		optional,
	)
}

var (
	errCouldNotGetInspector = errors.New("could not get inspector")
	errCouldNotGetMarkers   = errors.New("could not get markers")
)

type analyzer struct {
	isFirstField config.ConditionsFirstField
	useProtobuf  config.ConditionsUseProtobuf
}

// newAnalyzer creates a new analyzer.
func newAnalyzer(cfg config.ConditionsConfig) *analysis.Analyzer {
	defaultConfig(&cfg)

	a := &analyzer{
		isFirstField: cfg.IsFirstField,
		useProtobuf:  cfg.UseProtobuf,
	}

	return &analysis.Analyzer{
		Name:     name,
		Doc:      `Checks that all conditions type fields conform to the required conventions.`,
		Run:      a.run,
		Requires: []*analysis.Analyzer{inspect.Analyzer, markers.Analyzer, extractjsontags.Analyzer},
	}
}

func (a *analyzer) run(pass *analysis.Pass) (interface{}, error) {
	inspect, ok := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	if !ok {
		return nil, errCouldNotGetInspector
	}

	markersAccess, ok := pass.ResultOf[markers.Analyzer].(markers.Markers)
	if !ok {
		return nil, errCouldNotGetMarkers
	}

	// Filter to structs so that we can iterate over fields in a struct.
	// We need a struct here so that we can tell where in the struct the field is.
	nodeFilter := []ast.Node{
		(*ast.StructType)(nil),
	}

	inspect.Preorder(nodeFilter, func(n ast.Node) {
		sTyp, ok := n.(*ast.StructType)
		if !ok {
			return
		}

		if sTyp.Fields == nil {
			return
		}

		for i, field := range sTyp.Fields.List {
			fieldMarkers := markersAccess.FieldMarkers(field)

			a.checkField(pass, i, field, fieldMarkers)
		}
	})

	return nil, nil //nolint:nilnil
}

func (a *analyzer) checkField(pass *analysis.Pass, index int, field *ast.Field, fieldMarkers markers.MarkerSet) {
	if !fieldIsCalledConditions(field) {
		return
	}

	if !isSliceMetaV1Condition(field) {
		pass.Reportf(field.Pos(), "Conditions field must be a slice of metav1.Condition")
		return
	}

	checkFieldMarkers(pass, field, fieldMarkers)
	a.checkFieldTags(pass, index, field)

	if a.isFirstField == config.ConditionsFirstFieldWarn && index != 0 {
		pass.Reportf(field.Pos(), "Conditions field must be the first field in the struct")
	}
}

func checkFieldMarkers(pass *analysis.Pass, field *ast.Field, fieldMarkers markers.MarkerSet) {
	missingMarkers := []string{}

	if !fieldMarkers.HasWithValue(listTypeMap) {
		missingMarkers = append(missingMarkers, listTypeMap)
	}

	if !fieldMarkers.HasWithValue(listMapKeyType) {
		missingMarkers = append(missingMarkers, listMapKeyType)
	}

	if !fieldMarkers.HasWithValue(patchStrategy) {
		missingMarkers = append(missingMarkers, patchStrategy)
	}

	if !fieldMarkers.HasWithValue(patchMergeKeyType) {
		missingMarkers = append(missingMarkers, patchMergeKeyType)
	}

	if !fieldMarkers.Has(optional) {
		missingMarkers = append(missingMarkers, optional)
	}

	if len(missingMarkers) != 0 {
		pass.Report(analysis.Diagnostic{
			Pos:     field.Pos(),
			End:     field.End(),
			Message: "Conditions field is missing the following markers: " + strings.Join(missingMarkers, ", "),
			SuggestedFixes: []analysis.SuggestedFix{
				{
					Message: "Add missing markers",
					TextEdits: []analysis.TextEdit{
						{
							Pos:     field.Pos(),
							End:     token.NoPos,
							NewText: getNewMarkers(missingMarkers),
						},
					},
				},
			},
		})
	}
}

func getNewMarkers(missingMarkers []string) []byte {
	var out string

	for _, marker := range missingMarkers {
		out += "// +" + marker + "\n"
	}

	return []byte(out)
}

func (a *analyzer) checkFieldTags(pass *analysis.Pass, index int, field *ast.Field) {
	if field.Tag == nil {
		expectedTag := getExpectedTag(a.useProtobuf, a.isFirstField, index)

		pass.Report(analysis.Diagnostic{
			Pos:     field.Pos(),
			End:     field.End(),
			Message: "Conditions field is missing tags, should be: " + expectedTag,
			SuggestedFixes: []analysis.SuggestedFix{
				{
					Message: "Add missing tags",
					TextEdits: []analysis.TextEdit{
						{
							Pos:     field.End(),
							End:     token.NoPos,
							NewText: []byte(expectedTag),
						},
					},
				},
			},
		})

		return
	}

	asExpected, shouldFix := tagIsAsExpected(field.Tag.Value, a.useProtobuf, a.isFirstField, index)
	if !asExpected {
		expectedTag := getExpectedTag(a.useProtobuf, a.isFirstField, index)

		if !shouldFix {
			pass.Reportf(field.Tag.ValuePos, "Conditions field has incorrect tags, should be: %s", expectedTag)
		} else {
			pass.Report(analysis.Diagnostic{
				Pos:     field.Tag.ValuePos,
				End:     field.Tag.End(),
				Message: "Conditions field has incorrect tags, should be: " + expectedTag,
				SuggestedFixes: []analysis.SuggestedFix{
					{
						Message: "Update tags",
						TextEdits: []analysis.TextEdit{
							{
								Pos:     field.Tag.ValuePos,
								End:     field.Tag.End(),
								NewText: []byte(expectedTag),
							},
						},
					},
				},
			})
		}
	}
}

func getExpectedTag(useProtobuf config.ConditionsUseProtobuf, isFirstField config.ConditionsFirstField, index int) string {
	if useProtobuf == config.ConditionsUseProtobufSuggestFix || useProtobuf == config.ConditionsUseProtobufWarn {
		i := 1
		if isFirstField == config.ConditionsFirstFieldIgnore {
			i = index + 1
		}

		return fmt.Sprintf(expectedTagWithProtobufFmt, i)
	}

	return expectedTagWithoutProtobuf
}

func tagIsAsExpected(tag string, useProtobuf config.ConditionsUseProtobuf, isFirstField config.ConditionsFirstField, index int) (bool, bool) {
	switch useProtobuf {
	case config.ConditionsUseProtobufSuggestFix:
		return tag == getExpectedTag(config.ConditionsUseProtobufSuggestFix, isFirstField, index), true
	case config.ConditionsUseProtobufWarn:
		return tag == getExpectedTag(config.ConditionsUseProtobufWarn, isFirstField, index), false
	case config.ConditionsUseProtobufIgnore:
		return tag == getExpectedTag(config.ConditionsUseProtobufIgnore, isFirstField, index) || tag == getExpectedTag(config.ConditionsUseProtobufSuggestFix, isFirstField, index), true
	default:
		panic("unexpected useProtobuf value")
	}
}

func fieldIsCalledConditions(field *ast.Field) bool {
	if field == nil {
		return false
	}

	return len(field.Names) != 0 && field.Names[0] != nil && field.Names[0].Name == "Conditions"
}

func isSliceMetaV1Condition(field *ast.Field) bool {
	if field == nil {
		return false
	}

	// Field is not an array type.
	arr, ok := field.Type.(*ast.ArrayType)
	if !ok {
		return false
	}

	// Array element is not imported.
	selector, ok := arr.Elt.(*ast.SelectorExpr)
	if !ok {
		return false
	}

	pkg, ok := selector.X.(*ast.Ident)
	if !ok {
		return false
	}

	// Array element is not imported from metav1.
	if selector.X == nil || pkg.Name != "metav1" {
		return false
	}

	// Array element is not a metav1.Condition.
	if selector.Sel == nil || selector.Sel.Name != "Condition" {
		return false
	}

	return true
}

func defaultConfig(cfg *config.ConditionsConfig) {
	if cfg.IsFirstField == "" {
		cfg.IsFirstField = config.ConditionsFirstFieldWarn
	}

	if cfg.UseProtobuf == "" {
		cfg.UseProtobuf = config.ConditionsUseProtobufSuggestFix
	}
}
