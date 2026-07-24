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

var suites []e.Suite

func init() {
	suites = append(suites,
		e.Suite{Name: "hypershift/conformance", Qualifiers: []string{"true"}},
		e.Suite{Name: "hypershift/upgrade", Qualifiers: []string{`labels.exists(l, l=="control-plane-upgrade")`}, Parallelism: 1},
		e.Suite{Name: "hypershift/chaos", Qualifiers: []string{`labels.exists(l, l=="etcd-chaos")`}, Parallelism: 1},
	)
}

func main() {
	registry := e.NewRegistry()
	ext := e.NewExtension("openshift", "payload", "hypershift-tests")

	for _, s := range suites {
		ext.AddSuite(s)
	}

	specs, err := g.BuildExtensionTestSpecsFromOpenShiftGinkgoSuite()
	if err != nil {
		panic(fmt.Sprintf("couldn't build extension test specs from ginkgo: %+v", err))
	}

	ext.AddSpecs(specs)
	registry.Register(ext)

	root := &cobra.Command{Long: "HyperShift v2 E2E Tests (OTE)"}
	root.AddCommand(cmd.DefaultExtensionCommands(registry)...)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
