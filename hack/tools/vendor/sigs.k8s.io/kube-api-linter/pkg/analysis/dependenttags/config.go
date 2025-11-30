/*
Copyright 2025 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package dependenttags

// DependencyType defines the type of dependency rule.
type DependencyType string

const (
	// DependencyTypeAll indicates that all dependent markers are required.
	DependencyTypeAll DependencyType = "All"
	// DependencyTypeAny indicates that at least one of the dependent markers is required.
	DependencyTypeAny DependencyType = "Any"
)

// Config defines the configuration for the dependenttags linter.
type Config struct {
	// Rules defines the dependency rules between markers.
	Rules []Rule `mapstructure:"rules"`
}

// Rule defines a dependency rule where a main marker requires a set of dependent markers.
type Rule struct {
	// Identifier is the marker that requires other markers.
	Identifier string `mapstructure:"identifier"`
	// Dependents are the markers that are required by Main.
	Dependents []string `mapstructure:"dependents"`
	// Type defines how to interpret the dependents list.
	// When set to All, every dependent in the list must be present when the identifier is present on a field or type.
	// When set to Any, at least one of the listed dependents must be present when the identifier is present on a field or type.
	Type DependencyType `mapstructure:"type,omitempty"`
}
