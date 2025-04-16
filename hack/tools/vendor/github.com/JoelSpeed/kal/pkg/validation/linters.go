package validation

import (
	"fmt"
	"strings"

	"github.com/JoelSpeed/kal/pkg/analysis"
	"github.com/JoelSpeed/kal/pkg/config"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

// ValidateLinters is used to validate the configuration in the config.Linters struct.
//
//nolint:cyclop
func ValidateLinters(l config.Linters, fldPath *field.Path) field.ErrorList {
	fieldErrors := field.ErrorList{}

	enable := sets.New(l.Enable...)
	enablePath := fldPath.Child("enable")

	switch {
	case len(enable) != len(l.Enable):
		fieldErrors = append(fieldErrors, field.Invalid(enablePath, l.Enable, "values in 'enable' must be unique"))
	case enable.Has(config.Wildcard) && enable.Len() != 1:
		fieldErrors = append(fieldErrors, field.Invalid(enablePath, l.Enable, "wildcard ('*') must not be specified with other values"))
	case !enable.Has(config.Wildcard) && enable.Difference(analysis.NewRegistry().AllLinters()).Len() > 0:
		fieldErrors = append(fieldErrors, field.Invalid(enablePath, l.Enable, fmt.Sprintf("unknown linters: %s", strings.Join(enable.Difference(analysis.NewRegistry().AllLinters()).UnsortedList(), ","))))
	}

	disable := sets.New(l.Disable...)
	disablePath := fldPath.Child("disable")

	switch {
	case len(disable) != len(l.Disable):
		fieldErrors = append(fieldErrors, field.Invalid(disablePath, l.Disable, "values in 'disable' must be unique"))
	case disable.Has(config.Wildcard) && disable.Len() != 1:
		fieldErrors = append(fieldErrors, field.Invalid(disablePath, l.Disable, "wildcard ('*') must not be specified with other values"))
	case !disable.Has(config.Wildcard) && disable.Difference(analysis.NewRegistry().AllLinters()).Len() > 0:
		fieldErrors = append(fieldErrors, field.Invalid(disablePath, l.Disable, fmt.Sprintf("unknown linters: %s", strings.Join(disable.Difference(analysis.NewRegistry().AllLinters()).UnsortedList(), ","))))
	}

	if enable.Intersection(disable).Len() > 0 {
		fieldErrors = append(fieldErrors, field.Invalid(fldPath, l, fmt.Sprintf("values in 'enable' and 'disable may not overlap, overlapping values: %s", strings.Join(enable.Intersection(disable).UnsortedList(), ","))))
	}

	return fieldErrors
}
