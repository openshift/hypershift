//go:build e2ev2

package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/openshift-eng/openshift-tests-extension/pkg/cmd"
	e "github.com/openshift-eng/openshift-tests-extension/pkg/extension"
	"github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"
	g "github.com/openshift-eng/openshift-tests-extension/pkg/ginkgo"

	_ "github.com/openshift/hypershift/test/e2e/v2/tests"
)

var suites []e.Suite

func init() {
	suites = append(suites,
		e.Suite{Name: "hypershift/conformance", Qualifiers: []string{`!labels.exists(l, l=="control-plane-upgrade") && !labels.exists(l, l=="etcd-chaos")`}},
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

	specs.Walk(func(spec *extensiontests.ExtensionTestSpec) {
		for _, label := range spec.Labels.UnsortedList() {
			if taint, ok := strings.CutPrefix(label, "taint:"); ok {
				spec.Resources.Isolation.Taint = append(spec.Resources.Isolation.Taint, taint)
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
