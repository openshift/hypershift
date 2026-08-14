package libraryoutputresources

import (
	"context"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"k8s.io/cli-runtime/pkg/genericiooptions"
)

type OutputResourcesFunc func(ctx context.Context) (*OutputResources, error)

func NewOutputResourcesCommand(outputResourcesFn OutputResourcesFunc, streams genericiooptions.IOStreams) *cobra.Command {
	return newOutputResourcesCommand(outputResourcesFn, streams)
}

type outputResourcesFlags struct {
	outputResources OutputResourcesFunc

	streams genericiooptions.IOStreams
}

func newOutputResourcesFlags(streams genericiooptions.IOStreams) *outputResourcesFlags {
	return &outputResourcesFlags{
		streams: streams,
	}
}

func newOutputResourcesCommand(outputResources OutputResourcesFunc, streams genericiooptions.IOStreams) *cobra.Command {
	f := newOutputResourcesFlags(streams)
	f.outputResources = outputResources

	cmd := &cobra.Command{
		Use:   "output-resources",
		Short: "List of resources that this operator outputs and the type of cluster those modifications should be applied to.",

		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			if err := f.Validate(); err != nil {
				return err
			}
			o, err := f.ToOptions(ctx)
			if err != nil {
				return err
			}
			if err := o.Run(ctx); err != nil {
				return err
			}
			return nil
		},
	}

	f.BindFlags(cmd.Flags())

	return cmd
}

func (f *outputResourcesFlags) BindFlags(flags *pflag.FlagSet) {
}

func (f *outputResourcesFlags) Validate() error {
	return nil
}

func (f *outputResourcesFlags) ToOptions(ctx context.Context) (*outputResourcesOptions, error) {
	return newOutputResourcesOptions(
			f.outputResources,
			f.streams,
		),
		nil
}
