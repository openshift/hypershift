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
	_ validations.Validation                                  = (*Nullable)(nil)
	_ validations.Comparator[apiextensionsv1.JSONSchemaProps] = (*Nullable)(nil)
)

const nullableValidationName = "nullable"

// RegisterNullable registers the Nullable validation
// with the provided validation registry.
func RegisterNullable(registry validations.Registry) {
	registry.Register(nullableValidationName, nullableFactory)
}

// nullableFactory is a function used to initialize a Nullable validation
// implementation based on the provided configuration.
func nullableFactory(cfg map[string]interface{}) (validations.Validation, error) {
	nullableCfg := &NullableConfig{}

	err := ConfigToType(cfg, nullableCfg)
	if err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	err = ValidateNullableConfig(nullableCfg)
	if err != nil {
		return nil, fmt.Errorf("validating nullable config: %w", err)
	}

	return &Nullable{NullableConfig: *nullableCfg}, nil
}

// ValidateNullableConfig ensures provided NullableConfig is valid and defaults missing values.
func ValidateNullableConfig(in *NullableConfig) error {
	if in == nil {
		return nil
	}

	switch in.AdditionPolicy {
	case NullableAdditionPolicyAllow, NullableAdditionPolicyDisallow:
		// valid entries
	case NullableAdditionPolicy(""):
		in.AdditionPolicy = NullableAdditionPolicyDisallow
	default:
		return fmt.Errorf("%w : %q (valid values: %q, %q)", errUnknownNullableAdditionPolicy, in.AdditionPolicy, NullableAdditionPolicyAllow, NullableAdditionPolicyDisallow)
	}

	switch in.RemovalPolicy {
	case NullableRemovalPolicyAllow, NullableRemovalPolicyDisallow:
		// valid entries
	case NullableRemovalPolicy(""):
		in.RemovalPolicy = NullableRemovalPolicyDisallow
	default:
		return fmt.Errorf("%w : %q (valid values: %q, %q)", errUnknownNullableRemovalPolicy, in.RemovalPolicy, NullableRemovalPolicyAllow, NullableRemovalPolicyDisallow)
	}

	return nil
}

var errUnknownNullableAdditionPolicy = errors.New("unknown addition policy")
var errUnknownNullableRemovalPolicy = errors.New("unknown removal policy")

// NullableAdditionPolicy represents how allowing null values should be evaluated.
type NullableAdditionPolicy string

const (
	// NullableAdditionPolicyAllow treats allowing nulls when they were previously disallowed as compatible.
	NullableAdditionPolicyAllow NullableAdditionPolicy = "Allow"
	// NullableAdditionPolicyDisallow treats allowing nulls when they were previously disallowed as incompatible.
	NullableAdditionPolicyDisallow NullableAdditionPolicy = "Disallow"
)

// NullableRemovalPolicy represents how disallowing null values should be evaluated.
type NullableRemovalPolicy string

const (
	// NullableRemovalPolicyAllow treats disallowing nulls when they were previously allowed as compatible.
	NullableRemovalPolicyAllow NullableRemovalPolicy = "Allow"
	// NullableRemovalPolicyDisallow treats disallowing nulls when they were previously allowed as incompatible.
	NullableRemovalPolicyDisallow NullableRemovalPolicy = "Disallow"
)

// NullableConfig contains additional configuration for the Nullable validation.
type NullableConfig struct {
	// AdditionPolicy dictates whether allowing nulls when they were previously disallowed is compatible.
	// Allowed values are Allow and Disallow. Defaults to Disallow.
	AdditionPolicy NullableAdditionPolicy `json:"additionPolicy,omitempty"`
	// RemovalPolicy dictates whether disallowing nulls when they were previously allowed is compatible.
	// Allowed values are Allow and Disallow. Defaults to Disallow.
	RemovalPolicy NullableRemovalPolicy `json:"removalPolicy,omitempty"`
}

// Nullable is a Validation that can be used to identify
// incompatible changes to the nullable constraint of CRD properties.
type Nullable struct {
	NullableConfig
	enforcement config.EnforcementPolicy
}

// Name returns the name of the Nullable validation.
func (n *Nullable) Name() string {
	return nullableValidationName
}

// SetEnforcement sets the EnforcementPolicy for the Nullable validation.
func (n *Nullable) SetEnforcement(policy config.EnforcementPolicy) {
	n.enforcement = policy
}

// Compare compares an old and a new JSONSchemaProps, checking for incompatible changes to the nullable constraint of a property.
// In order for callers to determine if diffs to a JSONSchemaProps have been handled by this validation
// the JSONSchemaProps.Nullable field will be reset to 'false' as part of this method.
// It is highly recommended that only copies of the JSONSchemaProps to compare are provided to this method
// to prevent unintentional modifications.
func (n *Nullable) Compare(a, b *apiextensionsv1.JSONSchemaProps) validations.ComparisonResult {
	var err error

	switch {
	case a.Nullable == b.Nullable:
		// nothing to do
	case !a.Nullable && b.Nullable && n.AdditionPolicy != NullableAdditionPolicyAllow:
		err = fmt.Errorf("%w : %t -> %t", ErrNullableAllowed, a.Nullable, b.Nullable)
	case a.Nullable && !b.Nullable && n.RemovalPolicy != NullableRemovalPolicyAllow:
		err = fmt.Errorf("%w : %t -> %t", ErrNullableDisallowed, a.Nullable, b.Nullable)
	}

	a.Nullable = false
	b.Nullable = false

	return validations.HandleErrors(n.Name(), n.enforcement, err)
}

// ErrNullableAllowed represents an error state when a property transitions from not nullable to nullable.
var ErrNullableAllowed = errors.New("nullable added")

// ErrNullableDisallowed represents an error state when a property transitions from nullable to not nullable.
var ErrNullableDisallowed = errors.New("nullable removed")
