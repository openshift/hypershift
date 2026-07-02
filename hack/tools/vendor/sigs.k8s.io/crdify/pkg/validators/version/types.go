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

package version

import "sigs.k8s.io/crdify/pkg/validations"

// VersionedPropertyComparisonResult represents the results
// from running validations against a given version of
// a CustomResourceDefinition.
type VersionedPropertyComparisonResult struct {
	// version is the version(s) compared across the CustomResourceDefinitions
	// that were validated.
	// Version may be a singular version like "v1" when comparing
	// the exact same versions, but it may also be
	// a pair of versions like "v1 -> v2" when comparing
	// compatibility of two different versions
	// (i.e comparing served version backwards compatibility)
	Version string `json:"version"`

	// propertyComparisons is the set of property-based
	// validation results.
	PropertyComparisons []validations.PropertyComparisonResult `json:"propertyComparisons,omitempty"`
}

// IsZero returns whether or not a VersionedPropertyComparisonResult
// contains any property-based validation results.
// If it does contain property-based validation results, it means
// that some incompatibility was found and an error or warning
// should be issued and returns `false`.
// If it does not contain property-based validation results,
// no incompatibilities were found and returns `true`.
func (vpcr VersionedPropertyComparisonResult) IsZero() bool {
	return len(vpcr.PropertyComparisons) == 0
}
