package conditions

import (
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"slices"
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

	listTypeMap        = "listType=map"
	listMapKeyType     = "listMapKey=type"
	patchStrategy      = "patchStrategy"
	patchStrategyMerge = "patchStrategy=merge"
	patchMergeKey      = "patchMergeKey"
	patchMergeKeyType  = "patchMergeKey=type"
	optional           = "optional"

	expectedJSONTag     = "json:\"conditions,omitempty\""
	expectedPatchTag    = "patchStrategy:\"merge\" patchMergeKey:\"type\""
	expectedProtobufTag = "protobuf:\"bytes,%d,rep,name=conditions\""
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
	isFirstField     config.ConditionsFirstField
	useProtobuf      config.ConditionsUseProtobuf
	usePatchStrategy config.ConditionsUsePatchStrategy
}

// newAnalyzer creates a new analyzer.
func newAnalyzer(cfg config.ConditionsConfig) *analysis.Analyzer {
	defaultConfig(&cfg)

	a := &analyzer{
		isFirstField:     cfg.IsFirstField,
		useProtobuf:      cfg.UseProtobuf,
		usePatchStrategy: cfg.UsePatchStrategy,
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

	checkFieldMarkers(pass, field, fieldMarkers, a.usePatchStrategy)
	a.checkFieldTags(pass, index, field)

	if a.isFirstField == config.ConditionsFirstFieldWarn && index != 0 {
		pass.Reportf(field.Pos(), "Conditions field must be the first field in the struct")
	}
}

func checkFieldMarkers(pass *analysis.Pass, field *ast.Field, fieldMarkers markers.MarkerSet, usePatchStrategy config.ConditionsUsePatchStrategy) {
	missingMarkers := []string{}
	additionalMarkers := []markers.Marker{}

	if !fieldMarkers.HasWithValue(listTypeMap) {
		missingMarkers = append(missingMarkers, listTypeMap)
	}

	if !fieldMarkers.HasWithValue(listMapKeyType) {
		missingMarkers = append(missingMarkers, listMapKeyType)
	}

	patchMissingMarkers, patchAdditionalMarkers := checkPatchStrategyMarkers(fieldMarkers, usePatchStrategy)
	missingMarkers = append(missingMarkers, patchMissingMarkers...)
	additionalMarkers = append(additionalMarkers, patchAdditionalMarkers...)

	if !fieldMarkers.Has(optional) {
		missingMarkers = append(missingMarkers, optional)
	}

	if len(missingMarkers) != 0 {
		reportMissingMarkers(pass, field, missingMarkers, usePatchStrategy)
	}

	if len(additionalMarkers) != 0 {
		reportAdditionalMarkers(pass, field, additionalMarkers)
	}
}

func checkPatchStrategyMarkers(fieldMarkers markers.MarkerSet, usePatchStrategy config.ConditionsUsePatchStrategy) ([]string, []markers.Marker) {
	missingMarkers := []string{}
	additionalMarkers := []markers.Marker{}

	switch usePatchStrategy {
	case config.ConditionsUsePatchStrategySuggestFix, config.ConditionsUsePatchStrategyWarn:
		if !fieldMarkers.HasWithValue(patchStrategyMerge) {
			missingMarkers = append(missingMarkers, patchStrategyMerge)
		}

		if !fieldMarkers.HasWithValue(patchMergeKeyType) {
			missingMarkers = append(missingMarkers, patchMergeKeyType)
		}
	case config.ConditionsUsePatchStrategyIgnore:
		// If it's there, we don't care.
	case config.ConditionsUsePatchStrategyForbid:
		if fieldMarkers.HasWithValue(patchStrategyMerge) {
			additionalMarkers = append(additionalMarkers, fieldMarkers[patchStrategy]...)
		}

		if fieldMarkers.HasWithValue(patchMergeKeyType) {
			additionalMarkers = append(additionalMarkers, fieldMarkers[patchMergeKey]...)
		}
	default:
		panic("unexpected usePatchStrategy value")
	}

	return missingMarkers, additionalMarkers
}

func reportMissingMarkers(pass *analysis.Pass, field *ast.Field, missingMarkers []string, usePatchStrategy config.ConditionsUsePatchStrategy) {
	suggestedFixes := []analysis.SuggestedFix{}

	// If patch strategy is warn, and the only markers in the list are patchStrategy and patchMergeKeyType, we don't need to suggest a fix.
	if usePatchStrategy != config.ConditionsUsePatchStrategyWarn || slices.ContainsFunc[[]string, string](missingMarkers, func(marker string) bool {
		switch marker {
		case patchStrategyMerge, patchMergeKeyType:
			return false
		default:
			return true
		}
	}) {
		suggestedFixes = []analysis.SuggestedFix{
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
		}
	}

	pass.Report(analysis.Diagnostic{
		Pos:            field.Pos(),
		End:            field.End(),
		Message:        "Conditions field is missing the following markers: " + strings.Join(missingMarkers, ", "),
		SuggestedFixes: suggestedFixes,
	})
}

func reportAdditionalMarkers(pass *analysis.Pass, field *ast.Field, additionalMarkers []markers.Marker) {
	suggestedFixes := []analysis.SuggestedFix{}
	additionalMarkerValues := []string{}

	for _, marker := range additionalMarkers {
		additionalMarkerValues = append(additionalMarkerValues, marker.String())

		suggestedFixes = append(suggestedFixes, analysis.SuggestedFix{
			Message: "Remove additional marker",
			TextEdits: []analysis.TextEdit{
				{
					Pos:     marker.Pos,
					End:     marker.End + 1, // Add 1 to position to include the new line
					NewText: nil,
				},
			},
		})
	}

	pass.Report(analysis.Diagnostic{
		Pos:            field.Pos(),
		End:            field.End(),
		Message:        "Conditions field has the following additional markers: " + strings.Join(additionalMarkerValues, ", "),
		SuggestedFixes: suggestedFixes,
	})
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
		expectedTag := getExpectedTag(a.usePatchStrategy, a.useProtobuf, a.isFirstField, index)

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

	asExpected, shouldFix := tagIsAsExpected(field.Tag.Value, a.usePatchStrategy, a.useProtobuf, a.isFirstField, index)
	if !asExpected {
		expectedTag := getExpectedTag(a.usePatchStrategy, a.useProtobuf, a.isFirstField, index)

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

func getExpectedTag(usePatchStrategy config.ConditionsUsePatchStrategy, useProtobuf config.ConditionsUseProtobuf, isFirstField config.ConditionsFirstField, index int) string {
	expectedTag := fmt.Sprintf("`%s", expectedJSONTag)

	if usePatchStrategy == config.ConditionsUsePatchStrategySuggestFix || usePatchStrategy == config.ConditionsUsePatchStrategyWarn {
		expectedTag += fmt.Sprintf(" %s", expectedPatchTag)
	}

	if useProtobuf == config.ConditionsUseProtobufSuggestFix || useProtobuf == config.ConditionsUseProtobufWarn {
		expectedTag += fmt.Sprintf(" %s", getExpectedProtobufTag(isFirstField, index))
	}

	expectedTag += "`"

	return expectedTag
}

func getExpectedProtobufTag(isFirstField config.ConditionsFirstField, index int) string {
	i := 1
	if isFirstField == config.ConditionsFirstFieldIgnore {
		i = index + 1
	}

	return fmt.Sprintf(expectedProtobufTag, i)
}

func tagIsAsExpected(tag string, usePatchStrategy config.ConditionsUsePatchStrategy, useProtobuf config.ConditionsUseProtobuf, isFirstField config.ConditionsFirstField, index int) (bool, bool) {
	patchTagCorrect, patchShouldSuggestFix := patchStrategyTagIsAsExpected(tag, usePatchStrategy)
	protoTagCorrect, protoShouldSuggestFix := protobufTagIsAsExpected(tag, useProtobuf, isFirstField, index)

	return patchTagCorrect && protoTagCorrect, patchShouldSuggestFix || protoShouldSuggestFix
}

func patchStrategyTagIsAsExpected(tag string, usePatchStrategy config.ConditionsUsePatchStrategy) (bool, bool) {
	switch usePatchStrategy {
	case config.ConditionsUsePatchStrategySuggestFix:
		return strings.Contains(tag, expectedPatchTag), true
	case config.ConditionsUsePatchStrategyWarn:
		return strings.Contains(tag, expectedPatchTag), false
	case config.ConditionsUsePatchStrategyIgnore:
		return true, false
	case config.ConditionsUsePatchStrategyForbid:
		return !strings.Contains(tag, expectedPatchTag), true
	default:
		panic("unexpected usePatchStrategy value")
	}
}

func protobufTagIsAsExpected(tag string, useProtobuf config.ConditionsUseProtobuf, isFirstField config.ConditionsFirstField, index int) (bool, bool) {
	switch useProtobuf {
	case config.ConditionsUseProtobufSuggestFix:
		return strings.Contains(tag, getExpectedProtobufTag(isFirstField, index)), true
	case config.ConditionsUseProtobufWarn:
		return strings.Contains(tag, getExpectedProtobufTag(isFirstField, index)), false
	case config.ConditionsUseProtobufIgnore:
		return true, false
	case config.ConditionsUseProtobufForbid:
		return !strings.Contains(tag, getExpectedProtobufTag(isFirstField, index)), true
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

	if cfg.UsePatchStrategy == "" {
		cfg.UsePatchStrategy = config.ConditionsUsePatchStrategySuggestFix
	}
}
