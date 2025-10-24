This package is something that is useful for building testing methodology will move to library-go eventually.

Start by calling `func NewSampleOperatorApplyConfigurationCommand(applyConfigurationFn ApplyConfigurationFunc, streams genericiooptions.IOStreams) *cobra.Command {`
and adding that command as your `apply-configuration` command.