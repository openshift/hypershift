package libraryapplyconfiguration

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/openshift/library-go/pkg/manifestclient"
	"github.com/openshift/multi-operator-manager/pkg/flagtypes"
	"github.com/openshift/multi-operator-manager/pkg/library/libraryoutputresources"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"k8s.io/cli-runtime/pkg/genericiooptions"
	"k8s.io/utils/clock"
	clocktesting "k8s.io/utils/clock/testing"
)

// ApplyConfigurationInput is provided to the ApplyConfigurationFunc
type ApplyConfigurationInput struct {
	// MutationTrackingClient is offered as an alternative to the inputDirectory to make it easier to provide mocks to code.
	// This forces all downstream code to rely on the client reading aspects and not grow an odd dependency to disk.
	MutationTrackingClient manifestclient.MutationTrackingClient

	// Now is the declared time that this function was called at.  It doesn't necessarily bear any relationship to
	// the actual time.  This is another aspect that makes unit and integration testing easier.
	Clock clock.Clock

	// Streams is for I/O.  The StdIn will usually be nil'd out.
	Streams genericiooptions.IOStreams

	// Controllers holds a list of controller names to run.
	Controllers []string
}

// ApplyConfigurationFunc is a function called for applying configuration.
type ApplyConfigurationFunc func(ctx context.Context, applyConfigurationInput ApplyConfigurationInput) (*ApplyConfigurationRunResult, AllDesiredMutationsGetter, error)

func NewApplyConfigurationCommand(applyConfigurationFn ApplyConfigurationFunc, outputResourcesFn libraryoutputresources.OutputResourcesFunc, streams genericiooptions.IOStreams) *cobra.Command {
	return newApplyConfigurationCommand(applyConfigurationFn, outputResourcesFn, streams)
}

type applyConfigurationFlags struct {
	applyConfigurationFn ApplyConfigurationFunc
	outputResourcesFn    libraryoutputresources.OutputResourcesFunc

	// InputDirectory is a directory that contains the must-gather formatted inputs
	inputDirectory string

	// OutputDirectory is the directory to where output should be stored
	outputDirectory string

	// controllers hold an optional list of controller names to run.
	// '*' means "all controllers are enabled by default"
	// 'foo' means "enable 'foo'"
	// '-foo' means "disable 'foo'"
	controllers []string

	now time.Time

	streams genericiooptions.IOStreams
}

func newApplyConfigurationFlags(streams genericiooptions.IOStreams, applyConfigurationFn ApplyConfigurationFunc, outputResourcesFn libraryoutputresources.OutputResourcesFunc) *applyConfigurationFlags {
	return &applyConfigurationFlags{
		applyConfigurationFn: applyConfigurationFn,
		outputResourcesFn:    outputResourcesFn,
		now:                  time.Now(),
		streams:              streams,
	}
}

func newApplyConfigurationCommand(applyConfigurationFn ApplyConfigurationFunc, outputResourcesFn libraryoutputresources.OutputResourcesFunc, streams genericiooptions.IOStreams) *cobra.Command {
	f := newApplyConfigurationFlags(streams, applyConfigurationFn, outputResourcesFn)

	cmd := &cobra.Command{
		Use:   "apply-configuration",
		Short: "Operator apply-configuration command.",

		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintf(f.streams.ErrOut, "TODO output version\n")
			fmt.Fprintf(f.streams.Out, "TODO output version\n")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			if err := f.Validate(); err != nil {
				return err
			}
			o, err := f.ToOptions(ctx)
			if err != nil {
				return err
			}
			rand.New(rand.NewSource(o.input.Clock.Now().UTC().UnixNano()))
			if err := o.Run(ctx); err != nil {
				return err
			}
			return nil
		},
	}

	f.BindFlags(cmd.Flags())

	return cmd
}

func (f *applyConfigurationFlags) BindFlags(flags *pflag.FlagSet) {
	flags.StringVar(&f.inputDirectory, "input-dir", f.inputDirectory, "The directory where the resource input is stored.")
	flags.StringVar(&f.outputDirectory, "output-dir", f.outputDirectory, "The directory where the output is stored.")
	flags.StringSliceVar(&f.controllers, "controllers", []string{"*"}, "A list of controllers to enable. '*' enables all controllers, 'foo' enables the controller named 'foo', '-foo' disables the controller named 'foo'. Default: `*`")
	nowFlag := flagtypes.NewTimeValue(f.now, &f.now, []string{time.RFC3339})
	flags.Var(nowFlag, "now", "The time to use time.Now during this execution.")
}

func (f *applyConfigurationFlags) Validate() error {
	if len(f.inputDirectory) == 0 {
		return fmt.Errorf("--input-dir is required")
	}
	if len(f.outputDirectory) == 0 {
		return fmt.Errorf("--output-dir is required")
	}
	if f.now.IsZero() {
		return fmt.Errorf("--now is required")
	}
	return nil
}

func (f *applyConfigurationFlags) ToOptions(ctx context.Context) (*applyConfigurationOptions, error) {
	momClient := manifestclient.NewHTTPClient(f.inputDirectory)
	input := ApplyConfigurationInput{
		MutationTrackingClient: momClient,
		Clock:                  clocktesting.NewFakeClock(f.now),
		Controllers:            f.controllers,
		Streams:                f.streams,
	}

	return newApplyConfigurationOptions(
			f.applyConfigurationFn,
			f.outputResourcesFn,
			input,
			f.outputDirectory,
		),
		nil
}
