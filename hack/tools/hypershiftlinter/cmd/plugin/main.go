package main

import (
	"fmt"

	"golang.org/x/tools/go/analysis"

	"github.com/openshift/hypershift/hack/tools/hypershiftlinter"
)

// New is the entry point for the golangci-lint Go plugin.
// golangci-lint loads the .so and calls plugin.Lookup("New") to find this function.
// See: https://golangci-lint.run/docs/plugins/go-plugins/
func New(pluginSettings any) ([]*analysis.Analyzer, error) {
	analyzers, err := hypershiftlinter.BuildAnalyzers(pluginSettings)
	if err != nil {
		return nil, fmt.Errorf("hypershiftlinter: %w", err)
	}
	return analyzers, nil
}
