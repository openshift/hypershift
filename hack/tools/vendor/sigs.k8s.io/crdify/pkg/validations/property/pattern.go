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

package property

import (
	"errors"
	"fmt"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/crdify/pkg/config"
	"sigs.k8s.io/crdify/pkg/validations"
)

var (
	_ validations.Validation                                  = (*Pattern)(nil)
	_ validations.Comparator[apiextensionsv1.JSONSchemaProps] = (*Pattern)(nil)
)

const patternValidationName = "pattern"

// RegisterPattern registers the Pattern validation
// with the provided validation registry.
func RegisterPattern(registry validations.Registry) {
	registry.Register(patternValidationName, patternFactory)
}

// patternFactory is a function used to initialize a Pattern validation
// implementation based on the provided configuration.
func patternFactory(cfg map[string]interface{}) (validations.Validation, error) {
	patternCfg := &PatternConfig{}

	err := ConfigToType(cfg, patternCfg)
	if err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	err = ValidatePatternConfig(patternCfg)
	if err != nil {
		return nil, fmt.Errorf("validating pattern config: %w", err)
	}

	return &Pattern{PatternConfig: *patternCfg}, nil
}

// ValidatePatternConfig ensures provided PatternConfig is valid and defaults missing values.
func ValidatePatternConfig(in *PatternConfig) error {
	if in == nil {
		return nil
	}

	switch in.RemovalPolicy {
	case PatternRemovalPolicyAllow, PatternRemovalPolicyDisallow:
		// valid entries
	case PatternRemovalPolicy(""):
		in.RemovalPolicy = PatternRemovalPolicyDisallow
	default:
		return fmt.Errorf("%w : %q (valid values: %q, %q)", errUnknownPatternRemovalPolicy, in.RemovalPolicy, PatternRemovalPolicyAllow, PatternRemovalPolicyDisallow)
	}

	return nil
}

var errUnknownPatternRemovalPolicy = errors.New("unknown removal policy")

// PatternRemovalPolicy represents how removing a pattern constraint should be evaluated.
type PatternRemovalPolicy string

const (
	// PatternRemovalPolicyAllow treats removing a pattern constraint as compatible.
	PatternRemovalPolicyAllow PatternRemovalPolicy = "Allow"
	// PatternRemovalPolicyDisallow treats removing a pattern constraint as incompatible.
	PatternRemovalPolicyDisallow PatternRemovalPolicy = "Disallow"
)

// PatternConfig contains additional configuration for the Pattern validation.
type PatternConfig struct {
	// RemovalPolicy dictates whether removing a pattern constraint is compatible.
	// Allowed values are Allow and Disallow. Defaults to Disallow.
	RemovalPolicy PatternRemovalPolicy `json:"removalPolicy,omitempty"`
}

// Pattern is a Validation that can be used to identify
// incompatible changes to the pattern constraints of CRD properties.
type Pattern struct {
	PatternConfig
	enforcement config.EnforcementPolicy
}

// Name returns the name of the Pattern validation.
func (p *Pattern) Name() string {
	return patternValidationName
}

// SetEnforcement sets the EnforcementPolicy for the Pattern validation.
func (p *Pattern) SetEnforcement(policy config.EnforcementPolicy) {
	p.enforcement = policy
}

// Compare compares an old and a new JSONSchemaProps, checking for incompatible changes to the pattern constraints of a property.
// In order for callers to determine if diffs to a JSONSchemaProps have been handled by this validation
// the JSONSchemaProps.pattern field will be reset to '""' as part of this method.
// It is highly recommended that only copies of the JSONSchemaProps to compare are provided to this method
// to prevent unintentional modifications.
func (p *Pattern) Compare(a, b *apiextensionsv1.JSONSchemaProps) validations.ComparisonResult {
	var err error

	switch {
	case a.Pattern == b.Pattern:
		// nothing to do
	case a.Pattern == "" && b.Pattern != "":
		err = fmt.Errorf("%w : %q -> %q", ErrPatternAdded, a.Pattern, b.Pattern)
	case a.Pattern != "" && b.Pattern == "" && p.RemovalPolicy != PatternRemovalPolicyAllow:
		err = fmt.Errorf("%w : %q -> %q", ErrPatternRemoved, a.Pattern, b.Pattern)
	case b.Pattern != "" && a.Pattern != b.Pattern:
		err = fmt.Errorf("%w : %q -> %q", ErrPatternChanged, a.Pattern, b.Pattern)
	}

	a.Pattern = ""
	b.Pattern = ""

	return validations.HandleErrors(p.Name(), p.enforcement, err)
}

// ErrPatternAdded represents an error state when a property Pattern was added.
var ErrPatternAdded = errors.New("pattern added")

// ErrPatternChanged represents an error state when a property Pattern changed.
var ErrPatternChanged = errors.New("pattern changed")

// ErrPatternRemoved represents an error state when a property Pattern was removed.
var ErrPatternRemoved = errors.New("pattern removed")
