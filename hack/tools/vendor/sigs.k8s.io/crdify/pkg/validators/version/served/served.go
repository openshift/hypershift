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

package served

import (
	"fmt"
	"slices"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	versionhelper "k8s.io/apimachinery/pkg/version"
	"sigs.k8s.io/crdify/pkg/config"
	"sigs.k8s.io/crdify/pkg/validations"
	"sigs.k8s.io/crdify/pkg/validators/version"
)

// Validator validates Kubernetes CustomResourceDefinitions using the configured validations.
type Validator struct {
	comparators          []validations.Comparator[apiextensionsv1.JSONSchemaProps]
	conversionPolicy     config.ConversionPolicy
	unhandledEnforcement config.EnforcementPolicy
}

// ValidatorOption configures a Validator.
type ValidatorOption func(*Validator)

// WithComparators configures a Validator with the provided JSONSchemaProps Comparators.
// Each call to WithComparators is a replacement, not additive.
func WithComparators(comparators ...validations.Comparator[apiextensionsv1.JSONSchemaProps]) ValidatorOption {
	return func(v *Validator) {
		v.comparators = comparators
	}
}

// WithUnhandledEnforcementPolicy sets the unhandled enforcement policy for the validator.
func WithUnhandledEnforcementPolicy(policy config.EnforcementPolicy) ValidatorOption {
	return func(v *Validator) {
		if policy == "" {
			policy = config.EnforcementPolicyError
		}

		v.unhandledEnforcement = policy
	}
}

// WithConversionPolicy sets the conversion policy for the validator.
func WithConversionPolicy(policy config.ConversionPolicy) ValidatorOption {
	return func(v *Validator) {
		if policy == "" {
			policy = config.ConversionPolicyNone
		}

		v.conversionPolicy = policy
	}
}

// New creates a new Validator to validate the served versions of an old and new CustomResourceDefinition
// configured with the provided ValidatorOptions.
func New(opts ...ValidatorOption) *Validator {
	validator := &Validator{
		comparators:          []validations.Comparator[apiextensionsv1.JSONSchemaProps]{},
		conversionPolicy:     config.ConversionPolicyNone,
		unhandledEnforcement: config.EnforcementPolicyError,
	}

	for _, opt := range opts {
		opt(validator)
	}

	return validator
}

// Validate runs the validations configured in the Validator.
func (v *Validator) Validate(a, b *apiextensionsv1.CustomResourceDefinition) []version.VersionedPropertyComparisonResult {
	result := []version.VersionedPropertyComparisonResult{}

	// If conversion webhook is specified and conversion policy is ignore, pass check
	if v.conversionPolicy == config.ConversionPolicyIgnore && b.Spec.Conversion != nil && b.Spec.Conversion.Strategy == apiextensionsv1.WebhookConverter {
		return result
	}

	aResults := v.compareVersionPairs(a)
	bResults := v.compareVersionPairs(b)

	return subtractExistingIssues(bResults, aResults)
}

func (v *Validator) compareVersionPairs(crd *apiextensionsv1.CustomResourceDefinition) []version.VersionedPropertyComparisonResult {
	result := []version.VersionedPropertyComparisonResult{}

	for resultVersionPair, versions := range makeVersionPairs(crd) {
		result = append(result, version.VersionedPropertyComparisonResult{
			Version:             resultVersionPair,
			PropertyComparisons: validations.CompareVersions(versions[0], versions[1], v.unhandledEnforcement, v.comparators...),
		})
	}

	return result
}

func makeVersionPairs(crd *apiextensionsv1.CustomResourceDefinition) map[string][2]apiextensionsv1.CustomResourceDefinitionVersion {
	servedVersions := make([]apiextensionsv1.CustomResourceDefinitionVersion, 0, len(crd.Spec.Versions))

	for _, version := range crd.Spec.Versions {
		if version.Served {
			servedVersions = append(servedVersions, version)
		}
	}

	if len(servedVersions) < 2 {
		return nil
	}

	slices.SortFunc(servedVersions, func(a, b apiextensionsv1.CustomResourceDefinitionVersion) int {
		return versionhelper.CompareKubeAwareVersionStrings(a.Name, b.Name)
	})

	pairs := make(map[string][2]apiextensionsv1.CustomResourceDefinitionVersion, numUnidirectionalPermutations(servedVersions))

	for i, iVersion := range servedVersions[:len(servedVersions)-1] {
		for _, jVersion := range servedVersions[i+1:] {
			resultVersionPair := fmt.Sprintf("%s -> %s", iVersion.Name, jVersion.Name)
			pairs[resultVersionPair] = [2]apiextensionsv1.CustomResourceDefinitionVersion{iVersion, jVersion}
		}
	}

	return pairs
}

func numUnidirectionalPermutations[T any](in []T) int {
	n := len(in)

	return (n * (n - 1)) / 2
}

// subtractExistingIssues removes errors and warnings from b's results that are also found in a's results.
func subtractExistingIssues(b, a []version.VersionedPropertyComparisonResult) []version.VersionedPropertyComparisonResult {
	out := []version.VersionedPropertyComparisonResult{}

	for _, versionedPropertyComparisonResult := range b {
		ind := slices.IndexFunc(a, func(e version.VersionedPropertyComparisonResult) bool {
			return e.Version == versionedPropertyComparisonResult.Version
		})

		// if the comparison result isn't found in the known set,
		// keep it and continue looping.
		if ind == -1 {
			out = append(out, versionedPropertyComparisonResult)
			continue
		}

		out = append(out, filterKnownIssuesForVersionedPropertyComparisonResult(versionedPropertyComparisonResult, a[ind]))
	}

	return out
}

func filterKnownIssuesForVersionedPropertyComparisonResult(b, a version.VersionedPropertyComparisonResult) version.VersionedPropertyComparisonResult {
	return version.VersionedPropertyComparisonResult{
		Version:             b.Version,
		PropertyComparisons: filterKnownIssuesForPropertyComparisonResults(b.PropertyComparisons, a.PropertyComparisons),
	}
}

func filterKnownIssuesForPropertyComparisonResults(b, a []validations.PropertyComparisonResult) []validations.PropertyComparisonResult {
	out := []validations.PropertyComparisonResult{}

	for _, propertyComparisonResult := range b {
		ind := slices.IndexFunc(a, func(e validations.PropertyComparisonResult) bool {
			return e.Property == propertyComparisonResult.Property
		})

		// if the comparison result isn't found in the known set,
		// keep it and continue looping.
		if ind == -1 {
			out = append(out, propertyComparisonResult)
			continue
		}

		out = append(out, filterKnownIssuesForPropertyComparisonResult(propertyComparisonResult, a[ind]))
	}

	return out
}

func filterKnownIssuesForPropertyComparisonResult(b, a validations.PropertyComparisonResult) validations.PropertyComparisonResult {
	return validations.PropertyComparisonResult{
		Property:          b.Property,
		ComparisonResults: filterKnownIssuesForComparisonResults(b.ComparisonResults, a.ComparisonResults),
	}
}

func filterKnownIssuesForComparisonResults(b, a []validations.ComparisonResult) []validations.ComparisonResult {
	out := []validations.ComparisonResult{}

	for _, compResult := range b {
		ind := slices.IndexFunc(a, func(e validations.ComparisonResult) bool {
			return e.Name == compResult.Name
		})

		// if the comparison result isn't found in the known set,
		// keep it and continue looping.
		if ind == -1 {
			out = append(out, compResult)
			continue
		}

		out = append(out, filterKnownIssuesForComparisonResult(compResult, a[ind]))
	}

	return out
}

func filterKnownIssuesForComparisonResult(b, a validations.ComparisonResult) validations.ComparisonResult {
	return validations.ComparisonResult{
		Name: b.Name,
		Errors: slices.DeleteFunc(b.Errors, func(e string) bool {
			return slices.Contains(a.Errors, e)
		}),
		Warnings: slices.DeleteFunc(b.Warnings, func(e string) bool {
			return slices.Contains(a.Warnings, e)
		}),
	}
}
