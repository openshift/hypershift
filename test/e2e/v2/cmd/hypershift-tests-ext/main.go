//go:build e2ev2

package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/openshift-eng/openshift-tests-extension/pkg/cmd"
	e "github.com/openshift-eng/openshift-tests-extension/pkg/extension"
	et "github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"
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
	cfg, ok := platformConfigs[platform]
	if !ok {
		panic(fmt.Sprintf("unknown platform %q", platform))
	}
	// Assign each test to its resource pool based on label so the OTE
	// scheduler can co-schedule tests that share a hosted cluster.
	specs.Walk(func(spec *et.ExtensionTestSpec) {
		for label := range spec.Labels {
			if pool, found := cfg.LabelToPool[label]; found {
				if spec.Resources.ResourcePools == nil {
					spec.Resources.ResourcePools = make(map[string]int)
				}
				spec.Resources.ResourcePools[pool] = 1
				return
			}
		}
	})

	ext.AddSpecs(specs)
	registry.Register(ext)

	root := &cobra.Command{Long: "HyperShift v2 E2E Tests (OTE)"}
	root.AddCommand(cmd.DefaultExtensionCommands(registry)...)
	root.AddCommand(newRunCommand())

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
