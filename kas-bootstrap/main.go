package kasbootstrap

// kas-bootstrap is a tool to run the pre-required actions for bootstrapping the kas during cluster creation (or upgrade).
// It will apply some CRDs rendered by the cluster-config-operator and update the featureGate CR status by appending the git FeatureGate status.

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
)

type Options struct {
	ResourcesPath string
}

func NewRunCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "kas-bootstrap",
		Short: "Runs kas-bootstrap process",
		Long:  `Runs kas-bootstrap process. The process will run all pre required actions for the kas to bootstrap. This includes applying some CRDs and updating the featureGate CR.`,
	}

	opts := Options{
		ResourcesPath: "/work",
	}
	cmd.Flags().StringVar(&opts.ResourcesPath, "resources-path", "", "The path to all resources that should be applied and the rendered featureGate CR to reconcile.")
	cmd.Run = func(cmd *cobra.Command, args []string) {
		ctx, cancel := context.WithCancel(context.Background())
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT)
		go func() {
			<-sigs
			cancel()
		}()

		if err := run(ctx, opts); err != nil {
			log.Fatal(err)
			os.Exit(1)
		}
	}

	return cmd
}
