//go:build e2ev2

package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/openshift-eng/openshift-tests-extension/pkg/cmd"
	e "github.com/openshift-eng/openshift-tests-extension/pkg/extension"
	g "github.com/openshift-eng/openshift-tests-extension/pkg/ginkgo"

	_ "github.com/openshift/hypershift/test/e2e/v2/tests"
)

func main() {
	registry := e.NewRegistry()
	ext := e.NewExtension("openshift", "payload", "hypershift-tests")

	registerPlatformSuites(ext)

	specs, err := g.BuildExtensionTestSpecsFromOpenShiftGinkgoSuite()
	if err != nil {
		panic(fmt.Sprintf("couldn't build extension test specs from ginkgo: %+v", err))
	}

	platform := os.Getenv("HYPERSHIFT_PLATFORM")
	assignPoolsFromLabels(specs, platform)

	ext.AddSpecs(specs)
	registry.Register(ext)

	root := &cobra.Command{Long: "HyperShift v2 E2E Tests (OTE)"}
	root.AddCommand(cmd.DefaultExtensionCommands(registry)...)
	root.AddCommand(newRunCommand())

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
