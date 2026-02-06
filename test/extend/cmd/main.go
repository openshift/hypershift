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
	registry := e.NewRegistry()
	extension := e.NewExtension("openshift", "payload", extensionName)
	specs, err := g.BuildExtensionTestSpecsFromOpenShiftGinkgoSuite()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error building test specs: %v\n", err)
		os.Exit(1)
	}
	specs = specs.Walk(func(spec *et.ExtensionTestSpec) {
		// add spec labels
		if et.NameContains("smoke")(spec) {
			spec.Labels.Insert("smoke")
		}

		if et.NameContains("HyperShiftMGMT")(spec) {
			spec.Labels.Insert("HyperShiftMGMT")
		}

		if et.NameContains("Critical")(spec) {
			spec.Labels.Insert("Critical")
		}
	})
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
	extension.AddSuite(e.Suite{
		Name: fmt.Sprintf("%s/HyperShiftMGMT", extensionName),
		Qualifiers: []string{
			`labels.exists(l, l=="HyperShiftMGMT")`,
		},
	})
	extension.AddSuite(e.Suite{
		Name: fmt.Sprintf("%s/Critical", extensionName),
		Qualifiers: []string{
			`labels.exists(l, l=="Critical")`,
		},
	})
	extension.AddSpecs(specs)
	registry.Register(extension)
	root := &cobra.Command{
		Use:   "hypershift-test-extend",
		Short: "HyperShift extend test runner",
		Long: `A test runner for HyperShift extend tests using the openshift-tests-extension framework.

This tool allows to discover, list, and run HyperShift extend tests in a standardized way.

Examples:
  # List all available test suites
  hypershift-test-extend list suites

  # List all tests
  hypershift-test-extend list tests

  # Run suite
  hypershift-test-extend run-suite hypershift/smoke

  # Run test
  hypershift-test-extend run-test "[sig-hypershift] Hypershift openshift-test-extension smoke test"`,
	}

	root.AddCommand(cmd.DefaultExtensionCommands(registry)...)

	// Execute the command
	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
