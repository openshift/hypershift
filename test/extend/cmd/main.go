package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	cmd "github.com/openshift-eng/openshift-tests-extension/pkg/cmd"
	e "github.com/openshift-eng/openshift-tests-extension/pkg/extension"
	et "github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"
	g "github.com/openshift-eng/openshift-tests-extension/pkg/ginkgo"
	// Import the extend test package to register Ginkgo specs
	_ "github.com/openshift/hypershift/test/extend"
)

const (
	extensionName = "hypershift"
)

func main() {
	// Create registry and extension
	registry := e.NewRegistry()
	extension := e.NewExtension("openshift", "payload", extensionName)

	// Build test specs from Ginkgo suite
	specs, err := g.BuildExtensionTestSpecsFromOpenShiftGinkgoSuite()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error building test specs: %v\n", err)
		os.Exit(1)
	}
	// inspect each spec and add labels without filtering
	specs = specs.Walk(func(spec *et.ExtensionTestSpec) {
		// add spec labels
		if et.NameContains("smoke")(spec) {
			spec.Labels.Insert("smoke")
		}
	})

	// Add the suite prefix label to all specs
	specs = specs.AddLabel(fmt.Sprintf("%s/all", extensionName))

	extension.AddSuite(e.Suite{
		Name: fmt.Sprintf("%s/all", extensionName),
	})
	extension.AddSuite(e.Suite{
		Name: fmt.Sprintf("%s/smoke", extensionName),
		Qualifiers: []string{
			`labels.exists(l, l=="smoke")`,
		},
	})
	extension.AddSpecs(specs)
	registry.Register(extension)
	root := &cobra.Command{
		Use:   "hypershift-extend-test",
		Short: "HyperShift extend test runner",
		Long: `A test runner for HyperShift extend tests using the openshift-tests-extension framework.

This tool allows to discover, list, and run HyperShift extend tests in a standardized way.

Examples:
  # List all available test suites
  hypershift-extend-test list suites

  # List all tests
  hypershift-extend-test list tests

  # Run suite
  hypershift-extend-test run-suite hypershift/smoke

  # Run test
  hypershift-extend-test run-test "run-test "[sig-hypershift] Hypershift openshift-test-extension smoke test"`,
	}

	root.AddCommand(cmd.DefaultExtensionCommands(registry)...)

	// Execute the command
	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
