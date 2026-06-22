package libraryinputresources

import (
	"context"
	"github.com/openshift/multi-operator-manager/pkg/library/libraryoutputresources"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"k8s.io/cli-runtime/pkg/genericiooptions"
)

type InputResourcesFunc func(ctx context.Context) (*InputResources, error)

func NewInputResourcesCommand(inputResourcesFn InputResourcesFunc, outputResourcesFn libraryoutputresources.OutputResourcesFunc, streams genericiooptions.IOStreams) *cobra.Command {
	return newInputResourcesCommand(inputResourcesFn, outputResourcesFn, streams)
}

type inputResourcesFlags struct {
	inputResourcesFn  InputResourcesFunc
	outputResourcesFn libraryoutputresources.OutputResourcesFunc

	streams genericiooptions.IOStreams
}

func newInputResourcesFlags(streams genericiooptions.IOStreams) *inputResourcesFlags {
	return &inputResourcesFlags{
		streams: streams,
	}
}

func newInputResourcesCommand(inputResourcesFn InputResourcesFunc, outputResourcesFn libraryoutputresources.OutputResourcesFunc, streams genericiooptions.IOStreams) *cobra.Command {
	f := newInputResourcesFlags(streams)
	f.inputResourcesFn = inputResourcesFn
	f.outputResourcesFn = outputResourcesFn

	cmd := &cobra.Command{
		Use:   "input-resources",
		Short: "List of resources that this operator expects as inputs and the type of cluster those modifications should be applied to.",

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

func (f *inputResourcesFlags) BindFlags(flags *pflag.FlagSet) {
}

func (f *inputResourcesFlags) Validate() error {
	return nil
}

func (f *inputResourcesFlags) ToOptions(ctx context.Context) (*inputResourcesOptions, error) {
	return newInputResourcesOptions(
			f.inputResourcesFn,
			f.outputResourcesFn,
			f.streams,
		),
		nil
}
