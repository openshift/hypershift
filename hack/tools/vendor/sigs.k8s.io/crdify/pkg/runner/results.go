// Copyright 2025 The Kubernetes Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// 	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package runner

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"gopkg.in/yaml.v2"
	"sigs.k8s.io/crdify/pkg/validations"
	"sigs.k8s.io/crdify/pkg/validators/version"
)

// Results is a utility type to hold the validation results of
// running different validators.
type Results struct {
	// CRDValidation is the set of validation comparison results
	// at the whole CustomResourceDefinition scope
	CRDValidation []validations.ComparisonResult `json:"crdValidation,omitempty"`

	// SameVersionValidation is the set of validation comparison
	// results at the CustomResourceDefinition version level. Specifically
	// for same version comparisons across an old and new CustomResourceDefinition
	// instance (i.e comparing v1alpha1 with v1alpha1)
	SameVersionValidation []version.VersionedPropertyComparisonResult `json:"sameVersionValidation,omitempty"`

	// ServedVersionValidation is the set of validation comparison
	// results at the CustomResourceDefinition version level. Specifically
	// for served version comparisons across an old and new CustomResourceDefinition
	// instance (i.e comparing v1alpha1 with v1 if both are served)
	ServedVersionValidation []version.VersionedPropertyComparisonResult `json:"servedVersionValidation,omitempty"`
}

// MarshalJSON is a custom JSON marshalling function
// to ensure that we only include in the JSON/YAML rendered
// output the set of validations that returned some form
// of information (warnings/errors).
func (rr *Results) MarshalJSON() ([]byte, error) {
	out := &struct {
		CRDValidation           []validations.ComparisonResult              `json:"crdValidation,omitempty"`
		SameVersionValidation   []version.VersionedPropertyComparisonResult `json:"sameVersionValidation,omitempty"`
		ServedVersionValidation []version.VersionedPropertyComparisonResult `json:"servedVersionValidation,omitempty"`
	}{}

	out.CRDValidation = slices.DeleteFunc(rr.CRDValidation, func(e validations.ComparisonResult) bool {
		return e.IsZero()
	})
	slices.SortFunc(out.CRDValidation, func(a, b validations.ComparisonResult) int {
		return strings.Compare(a.Name, b.Name)
	})

	out.SameVersionValidation = dropZeroVersionedPropertyComparisonResults(rr.SameVersionValidation...)
	out.ServedVersionValidation = dropZeroVersionedPropertyComparisonResults(rr.ServedVersionValidation...)

	return json.Marshal(out) //nolint:wrapcheck
}

func dropZeroVersionedPropertyComparisonResults(vpcrs ...version.VersionedPropertyComparisonResult) []version.VersionedPropertyComparisonResult {
	out := []version.VersionedPropertyComparisonResult{}

	for _, vpcr := range vpcrs {
		if vpcr.IsZero() {
			continue
		}

		pcrs := dropZeroPropertyComparisonResults(vpcr.PropertyComparisons...)
		if len(pcrs) == 0 {
			continue
		}

		out = append(out, version.VersionedPropertyComparisonResult{
			Version:             vpcr.Version,
			PropertyComparisons: pcrs,
		})
	}

	// sort for deterministic output
	slices.SortFunc(out, func(a, b version.VersionedPropertyComparisonResult) int {
		return strings.Compare(a.Version, b.Version)
	})

	return out
}

func dropZeroPropertyComparisonResults(pcrs ...validations.PropertyComparisonResult) []validations.PropertyComparisonResult {
	out := []validations.PropertyComparisonResult{}

	for _, pcr := range pcrs {
		if pcr.IsZero() {
			continue
		}

		crs := dropZeroComparisonResults(pcr.ComparisonResults...)
		if len(crs) == 0 {
			continue
		}

		out = append(out, validations.PropertyComparisonResult{
			Property:          pcr.Property,
			ComparisonResults: crs,
		})
	}

	// sort for deterministic output
	slices.SortFunc(out, func(a, b validations.PropertyComparisonResult) int {
		return strings.Compare(a.Property, b.Property)
	})

	return out
}

func dropZeroComparisonResults(crs ...validations.ComparisonResult) []validations.ComparisonResult {
	out := []validations.ComparisonResult{}

	for _, cr := range crs {
		if cr.IsZero() {
			continue
		}

		out = append(out, cr)
	}

	// sort for deterministic output
	slices.SortFunc(out, func(a, b validations.ComparisonResult) int {
		return strings.Compare(a.Name, b.Name)
	})

	return out
}

// Format is a representation of an output format.
type Format string

const (
	// FormatJSON represents a JSON output format.
	FormatJSON Format = "json"

	// FormatYAML represents a YAML output format.
	FormatYAML Format = "yaml"

	// FormatPlainText represents a PlainText output format.
	FormatPlainText Format = "plaintext"

	// FormatMarkdown represents a Markdown output format.
	FormatMarkdown Format = "markdown"
)

// Render returns the string representation of the provided
// format or an error if one is encountered.
// Currently supported render formats are json, yaml, plaintext, and markdown.
// Unknown formats will result in an error.
func (rr *Results) Render(format Format) (string, error) {
	switch format {
	case FormatJSON:
		return rr.RenderJSON()
	case FormatYAML:
		return rr.RenderYAML()
	case FormatMarkdown:
		return rr.RenderMarkdown(), nil
	case FormatPlainText:
		return rr.RenderPlainText(), nil
	default:
		return "", fmt.Errorf("%w : %q", errUnknownRenderFormat, format)
	}
}

var errUnknownRenderFormat = errors.New("unknown render format")

// RenderJSON returns a string of the results rendered in JSON or an error.
func (rr *Results) RenderJSON() (string, error) {
	outBytes, err := json.MarshalIndent(rr, "", " ")
	return string(outBytes), err
}

// RenderYAML returns a string of the results rendered in YAML or an error.
func (rr *Results) RenderYAML() (string, error) {
	outBytes, err := yaml.Marshal(rr)
	return string(outBytes), err
}

// RenderMarkdown returns a string of the results rendered as Markdown
//
//nolint:dupl
func (rr *Results) RenderMarkdown() string {
	var out strings.Builder

	for _, result := range rr.CRDValidation {
		if result.IsZero() {
			continue
		}

		for _, err := range result.Errors {
			out.WriteString(fmt.Sprintf("- **%s** - `ERROR` - %s\n", result.Name, err))
		}

		for _, err := range result.Warnings {
			out.WriteString(fmt.Sprintf("- **%s** - `WARNING` - %s\n", result.Name, err))
		}
	}

	processFunc := func(version, property string, comparisonResult validations.ComparisonResult) {
		if comparisonResult.IsZero() {
			return
		}

		for _, err := range comparisonResult.Errors {
			out.WriteString(fmt.Sprintf("- **%s** - *%s* - %s - `ERROR` - %s\n", version, property, comparisonResult.Name, err))
		}

		for _, err := range comparisonResult.Warnings {
			out.WriteString(fmt.Sprintf("- **%s** - *%s* - %s - `WARNING` - %s\n", version, property, comparisonResult.Name, err))
		}
	}

	processVersionedComparisonResults(rr.SameVersionValidation, processFunc)
	processVersionedComparisonResults(rr.ServedVersionValidation, processFunc)

	return out.String()
}

// RenderPlainText returns a string of the results rendered as PlainText
//
//nolint:dupl
func (rr *Results) RenderPlainText() string {
	var out strings.Builder

	for _, result := range rr.CRDValidation {
		if result.IsZero() {
			continue
		}

		for _, err := range result.Errors {
			out.WriteString(fmt.Sprintf("- %s - ERROR - %s\n", result.Name, err))
		}

		for _, err := range result.Warnings {
			out.WriteString(fmt.Sprintf("- %s - WARNING - %s\n", result.Name, err))
		}
	}

	processFunc := func(version, property string, comparisonResult validations.ComparisonResult) {
		if comparisonResult.IsZero() {
			return
		}

		for _, err := range comparisonResult.Errors {
			out.WriteString(fmt.Sprintf("- %s - %s - %s - ERROR - %s\n", version, property, comparisonResult.Name, err))
		}

		for _, err := range comparisonResult.Warnings {
			out.WriteString(fmt.Sprintf("- %s - %s - %s - WARNING - %s\n", version, property, comparisonResult.Name, err))
		}
	}

	processVersionedComparisonResults(rr.SameVersionValidation, processFunc)
	processVersionedComparisonResults(rr.ServedVersionValidation, processFunc)

	return out.String()
}

func processVersionedComparisonResults(vcrs []version.VersionedPropertyComparisonResult, processFunc func(version, property string, comparisonResult validations.ComparisonResult)) {
	// sort along the way for determinism
	slices.SortFunc(vcrs, func(a, b version.VersionedPropertyComparisonResult) int {
		return strings.Compare(a.Version, b.Version)
	})

	for _, versionResult := range vcrs {
		slices.SortFunc(versionResult.PropertyComparisons, func(a, b validations.PropertyComparisonResult) int {
			return strings.Compare(a.Property, b.Property)
		})

		for _, propertyResult := range versionResult.PropertyComparisons {
			slices.SortFunc(propertyResult.ComparisonResults, func(a, b validations.ComparisonResult) int {
				return strings.Compare(a.Name, b.Name)
			})

			for _, comparisonResult := range propertyResult.ComparisonResults {
				processFunc(versionResult.Version, propertyResult.Property, comparisonResult)
			}
		}
	}
}

// HasFailures returns a boolean signaling if any of the validation results contain any errors.
func (rr *Results) HasFailures() bool {
	return rr.HasCRDValidationFailures() || rr.HasSameVersionValidationFailures() || rr.HasServedVersionValidationFailures()
}

// HasCRDValidationFailures returns a boolean signaling if the CRD scoped validations contain any errors.
func (rr *Results) HasCRDValidationFailures() bool {
	for _, result := range rr.CRDValidation {
		if len(result.Errors) > 0 {
			return true
		}
	}

	return false
}

// HasSameVersionValidationFailures returns a boolean signaling if the same version validations contain any errors.
func (rr *Results) HasSameVersionValidationFailures() bool {
	for _, versionResults := range rr.SameVersionValidation {
		for _, propertyResults := range versionResults.PropertyComparisons {
			for _, result := range propertyResults.ComparisonResults {
				if len(result.Errors) > 0 {
					return true
				}
			}
		}
	}

	return false
}

// HasServedVersionValidationFailures returns a boolean signaling if the served version validations contain any errors.
func (rr *Results) HasServedVersionValidationFailures() bool {
	for _, versionResults := range rr.ServedVersionValidation {
		for _, propertyResults := range versionResults.PropertyComparisons {
			for _, result := range propertyResults.ComparisonResults {
				if len(result.Errors) > 0 {
					return true
				}
			}
		}
	}

	return false
}
