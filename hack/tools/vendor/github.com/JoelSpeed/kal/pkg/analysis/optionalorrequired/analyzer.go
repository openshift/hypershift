package optionalorrequired

import (
	"errors"
	"fmt"
	"go/ast"

	"github.com/JoelSpeed/kal/pkg/analysis/helpers/extractjsontags"
	"github.com/JoelSpeed/kal/pkg/analysis/helpers/inspector"
	"github.com/JoelSpeed/kal/pkg/analysis/helpers/markers"
	"github.com/JoelSpeed/kal/pkg/config"
	"golang.org/x/tools/go/analysis"
)

const (
	name = "optionalorrequired"

	// OptionalMarker is the marker that indicates that a field is optional.
	OptionalMarker = "optional"

	// RequiredMarker is the marker that indicates that a field is required.
	RequiredMarker = "required"

	// KubebuilderOptionalMarker is the marker that indicates that a field is optional in kubebuilder.
	KubebuilderOptionalMarker = "kubebuilder:validation:Optional"

	// KubebuilderRequiredMarker is the marker that indicates that a field is required in kubebuilder.
	KubebuilderRequiredMarker = "kubebuilder:validation:Required"
)

func init() {
	markers.DefaultRegistry().Register(
		OptionalMarker,
		RequiredMarker,
		KubebuilderOptionalMarker,
		KubebuilderRequiredMarker,
	)
}

var (
	errCouldNotGetInspector = errors.New("could not get inspector")
)

type analyzer struct {
	primaryOptionalMarker   string
	secondaryOptionalMarker string

	primaryRequiredMarker   string
	secondaryRequiredMarker string
}

// newAnalyzer creates a new analyzer with the given configuration.
func newAnalyzer(cfg config.OptionalOrRequiredConfig) *analysis.Analyzer {
	defaultConfig(&cfg)

	a := &analyzer{}

	switch cfg.PreferredOptionalMarker {
	case OptionalMarker:
		a.primaryOptionalMarker = OptionalMarker
		a.secondaryOptionalMarker = KubebuilderOptionalMarker
	case KubebuilderOptionalMarker:
		a.primaryOptionalMarker = KubebuilderOptionalMarker
		a.secondaryOptionalMarker = OptionalMarker
	}

	switch cfg.PreferredRequiredMarker {
	case RequiredMarker:
		a.primaryRequiredMarker = RequiredMarker
		a.secondaryRequiredMarker = KubebuilderRequiredMarker
	case KubebuilderRequiredMarker:
		a.primaryRequiredMarker = KubebuilderRequiredMarker
		a.secondaryRequiredMarker = RequiredMarker
	}

	return &analysis.Analyzer{
		Name:     name,
		Doc:      "Checks that all struct fields are marked either with the optional or required markers.",
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

//nolint:cyclop
func (a *analyzer) checkField(pass *analysis.Pass, field *ast.Field, fieldMarkers markers.MarkerSet, fieldTagInfo extractjsontags.FieldTagInfo) {
	if fieldTagInfo.Inline {
		// Inline fields would have no effect if they were marked as optional/required.
		return
	}

	var prefix string
	if len(field.Names) > 0 && field.Names[0] != nil {
		prefix = fmt.Sprintf("field %s", field.Names[0].Name)
	} else if ident, ok := field.Type.(*ast.Ident); ok {
		prefix = fmt.Sprintf("embedded field %s", ident.Name)
	}

	hasPrimaryOptional := fieldMarkers.Has(a.primaryOptionalMarker)
	hasPrimaryRequired := fieldMarkers.Has(a.primaryRequiredMarker)

	hasSecondaryOptional := fieldMarkers.Has(a.secondaryOptionalMarker)
	hasSecondaryRequired := fieldMarkers.Has(a.secondaryRequiredMarker)

	hasEitherOptional := hasPrimaryOptional || hasSecondaryOptional
	hasEitherRequired := hasPrimaryRequired || hasSecondaryRequired

	hasBothOptional := hasPrimaryOptional && hasSecondaryOptional
	hasBothRequired := hasPrimaryRequired && hasSecondaryRequired

	switch {
	case hasEitherOptional && hasEitherRequired:
		pass.Reportf(field.Pos(), "%s must not be marked as both optional and required", prefix)
	case hasSecondaryOptional:
		marker := fieldMarkers[a.secondaryOptionalMarker]
		if hasBothOptional {
			pass.Report(reportShouldRemoveSecondaryMarker(field, marker, a.primaryOptionalMarker, a.secondaryOptionalMarker, prefix))
		} else {
			pass.Report(reportShouldReplaceSecondaryMarker(field, marker, a.primaryOptionalMarker, a.secondaryOptionalMarker, prefix))
		}
	case hasSecondaryRequired:
		marker := fieldMarkers[a.secondaryRequiredMarker]
		if hasBothRequired {
			pass.Report(reportShouldRemoveSecondaryMarker(field, marker, a.primaryRequiredMarker, a.secondaryRequiredMarker, prefix))
		} else {
			pass.Report(reportShouldReplaceSecondaryMarker(field, marker, a.primaryRequiredMarker, a.secondaryRequiredMarker, prefix))
		}
	case hasPrimaryOptional || hasPrimaryRequired:
		// This is the correct state.
	default:
		pass.Reportf(field.Pos(), "%s must be marked as %s or %s", prefix, a.primaryOptionalMarker, a.primaryRequiredMarker)
	}
}

func reportShouldReplaceSecondaryMarker(field *ast.Field, marker []markers.Marker, primaryMarker, secondaryMarker, prefix string) analysis.Diagnostic {
	textEdits := make([]analysis.TextEdit, len(marker))

	for i, m := range marker {
		if i == 0 {
			textEdits[i] = analysis.TextEdit{
				Pos:     m.Pos,
				End:     m.End,
				NewText: []byte(fmt.Sprintf("// +%s", primaryMarker)),
			}

			continue
		}

		textEdits[i] = analysis.TextEdit{
			Pos:     m.Pos,
			End:     m.End + 1, // Add 1 to position to include the new line
			NewText: nil,
		}
	}

	return analysis.Diagnostic{
		Pos:     field.Pos(),
		Message: fmt.Sprintf("%s should use marker %s instead of %s", prefix, primaryMarker, secondaryMarker),
		SuggestedFixes: []analysis.SuggestedFix{
			{
				Message:   fmt.Sprintf("should replace `%s` with `%s`", secondaryMarker, primaryMarker),
				TextEdits: textEdits,
			},
		},
	}
}

func reportShouldRemoveSecondaryMarker(field *ast.Field, marker []markers.Marker, primaryMarker, secondaryMarker, prefix string) analysis.Diagnostic {
	textEdits := make([]analysis.TextEdit, len(marker))

	for i, m := range marker {
		textEdits[i] = analysis.TextEdit{
			Pos:     m.Pos,
			End:     m.End + 1, // Add 1 to position to include the new line
			NewText: nil,
		}
	}

	return analysis.Diagnostic{
		Pos:     field.Pos(),
		Message: fmt.Sprintf("%s should use only the marker %s, %s is not required", prefix, primaryMarker, secondaryMarker),
		SuggestedFixes: []analysis.SuggestedFix{
			{
				Message:   fmt.Sprintf("should remove `// +%s`", secondaryMarker),
				TextEdits: textEdits,
			},
		},
	}
}

func defaultConfig(cfg *config.OptionalOrRequiredConfig) {
	if cfg.PreferredOptionalMarker == "" {
		cfg.PreferredOptionalMarker = OptionalMarker
	}

	if cfg.PreferredRequiredMarker == "" {
		cfg.PreferredRequiredMarker = RequiredMarker
	}
}
