package main

import (
	"context"
	"os"

	"github.com/openshift/hypershift/control-plane-pki-operator/topology"
	hypershiftversion "github.com/openshift/hypershift/pkg/version"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/version"

	"k8s.io/component-base/cli"
	"k8s.io/utils/clock"

	"github.com/openshift/library-go/pkg/controller/controllercmd"
)

func main() {
	command := NewOperatorCommand(context.Background())
	code := cli.Run(command)
	os.Exit(code)
}

func NewOperatorCommand(ctx context.Context) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "control-plane-pki-operator",
		Short: "HyperShift control plane PKI Operator.",
		Run: func(cmd *cobra.Command, args []string) {
			cmd.Help()
			os.Exit(1)
		},
	}

	cmd.Version = hypershiftversion.GetRevision()
	cmd.AddCommand(NewOperator(ctx))

	return cmd
}

func NewOperator(ctx context.Context) *cobra.Command {
	cmd := controllercmd.
		NewControllerCommandConfig("control-plane-pki-operator", version.Info{
			GitCommit: hypershiftversion.GetRevision(),
		}, RunOperator, &clock.RealClock{}).
		WithTopologyDetector(topology.Detector{}).
		NewCommandWithContext(ctx)
	cmd.Use = "operator"
	cmd.Short = "Start the HyperShift control plane PKI Operator"
	return cmd
}
