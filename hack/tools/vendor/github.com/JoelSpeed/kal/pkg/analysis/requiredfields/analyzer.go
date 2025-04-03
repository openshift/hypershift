package requiredfields

import (
	"errors"
	"fmt"
	"go/ast"
	"strings"

	"github.com/JoelSpeed/kal/pkg/analysis/helpers/extractjsontags"
	"github.com/JoelSpeed/kal/pkg/analysis/helpers/inspector"
	"github.com/JoelSpeed/kal/pkg/analysis/helpers/markers"
	"github.com/JoelSpeed/kal/pkg/config"
	"golang.org/x/tools/go/analysis"
)

const (
	name = "requiredfields"

	requiredMarker            = "required"
	kubebuilderRequiredMarker = "kubebuilder:validation:Required"
)

func init() {
	markers.DefaultRegistry().Register(requiredMarker, kubebuilderRequiredMarker)
}

var (
	errCouldNotGetInspector = errors.New("could not get inspector")
)

type analyzer struct {
	pointerPolicy config.RequiredFieldPointerPolicy
}

// newAnalyzer creates a new analyzer.
func newAnalyzer(cfg config.RequiredFieldsConfig) *analysis.Analyzer {
	defaultConfig(&cfg)

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

func (a *analyzer) run(pass *analysis.Pass) (interface{}, error) {
	inspect, ok := pass.ResultOf[inspector.Analyzer].(inspector.Inspector)
	if !ok {
		return nil, errCouldNotGetInspector
	}

	inspect.InspectFields(func(field *ast.Field, stack []ast.Node, jsonTagInfo extractjsontags.FieldTagInfo, markersAccess markers.Markers) {
		a.checkField(pass, field, markersAccess.FieldMarkers(field), jsonTagInfo)
	})

	return nil, nil //nolint:nilnil
}

func (a *analyzer) checkField(pass *analysis.Pass, field *ast.Field, fieldMarkers markers.MarkerSet, fieldTagInfo extractjsontags.FieldTagInfo) {
	if field == nil || len(field.Names) == 0 {
		return
	}

	fieldName := field.Names[0].Name

	if !fieldMarkers.Has(requiredMarker) && !fieldMarkers.Has(kubebuilderRequiredMarker) {
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
		case config.RequiredFieldPointerWarn:
			// Do not suggest a fix.
		case config.RequiredFieldPointerSuggestFix:
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

func defaultConfig(cfg *config.RequiredFieldsConfig) {
	if cfg.PointerPolicy == "" {
		cfg.PointerPolicy = config.RequiredFieldPointerSuggestFix
	}
}
