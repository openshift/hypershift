package validation

import (
	"github.com/JoelSpeed/kal/pkg/config"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

// ValidateGolangCIConfig is used to validate the provided configuration once
// extracted from golangci-lint.
func ValidateGolangCIConfig(g config.GolangCIConfig, fldPath *field.Path) error {
	if fldPath == nil {
		fldPath = field.NewPath("")
	}

	var fieldErrors field.ErrorList

	fieldErrors = append(fieldErrors, ValidateLinters(g.Linters, fldPath.Child("linters"))...)
	fieldErrors = append(fieldErrors, ValidateLintersConfig(g.LintersConfig, fldPath.Child("lintersConfig"))...)

	return fieldErrors.ToAggregate()
}
