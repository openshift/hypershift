//go:build e2ev2

package lifecycle

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	sigyaml "sigs.k8s.io/yaml"
)

// TestPlan is a declarative specification of which cluster variants to
// create and which tests to run against them. It is the external API
// for composing e2e runs — each (PlatformConfig) owns the details of
// how each variant is created, while the TestPlan selects and arranges them.
type TestPlan struct {
	Name       string     `json:"name"`
	Platform   string     `json:"platform"`
	TestMatrix TestMatrix `json:"testMatrix"`
}

// Validate checks that all variants referenced by the TestMatrix
// exist in allSpecs. Returns an error listing all problems.
func (p *TestPlan) Validate(allSpecs []ClusterSpec) error {
	known := make(map[string]bool, len(allSpecs))
	for _, s := range allSpecs {
		known[s.Variant] = true
	}

	var errs []error
	if err := p.TestMatrix.Validate(); err != nil {
		errs = append(errs, err)
	}
	for _, v := range p.TestMatrix.Variants() {
		if !known[v] {
			errs = append(errs, fmt.Errorf("variant %q not found", v))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("test plan %q validation failed:\n%w", p.Name, errors.Join(errs...))
	}
	return nil
}

// FilterClusterSpecs returns the subset of allSpecs whose variants
// are referenced by the TestMatrix. The plan must be validated
// against allSpecs via Validate before calling this method;
// unknown variants are silently skipped.
func (p *TestPlan) FilterClusterSpecs(allSpecs []ClusterSpec) []ClusterSpec {
	byVariant := make(map[string]ClusterSpec, len(allSpecs))
	for _, s := range allSpecs {
		byVariant[s.Variant] = s
	}

	variants := p.TestMatrix.Variants()
	result := make([]ClusterSpec, 0, len(variants))
	for _, v := range variants {
		if spec, ok := byVariant[v]; ok {
			result = append(result, spec)
		}
	}
	return result
}

// ResolveTestPlan loads a TestPlan from the given path, or returns
// the platform's default plan if path is empty. Callers are
// responsible for validation appropriate to their context.
func ResolveTestPlan(path string, platform PlatformConfig) (*TestPlan, error) {
	if path != "" {
		return ReadTestPlan(path)
	}
	plan := platform.DefaultTestPlan()
	return &plan, nil
}

// ParseTestPlan deserializes a TestPlan from JSON or YAML bytes.
// Set yaml to true for YAML input.
func ParseTestPlan(data []byte, yaml bool) (*TestPlan, error) {
	var plan TestPlan
	if yaml {
		if err := sigyaml.UnmarshalStrict(data, &plan); err != nil {
			return nil, fmt.Errorf("unmarshaling YAML test plan: %w", err)
		}
	} else {
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&plan); err != nil {
			return nil, fmt.Errorf("unmarshaling JSON test plan: %w", err)
		}
	}
	return &plan, nil
}

// ReadTestPlan reads a TestPlan from a JSON or YAML file. The format
// is determined by the file extension (.yaml/.yml for YAML, everything
// else is treated as JSON).
func ReadTestPlan(path string) (*TestPlan, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading test plan from %s: %w", path, err)
	}
	ext := strings.ToLower(filepath.Ext(path))
	isYAML := ext == ".yaml" || ext == ".yml"
	return ParseTestPlan(data, isYAML)
}
