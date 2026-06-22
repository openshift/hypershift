package libraryoutputresources

import (
	"context"
	"fmt"

	"k8s.io/cli-runtime/pkg/genericiooptions"
	"sigs.k8s.io/yaml"
)

type outputResourcesOptions struct {
	outputResourcesFn OutputResourcesFunc

	streams genericiooptions.IOStreams
}

func newOutputResourcesOptions(outputResourcesFn OutputResourcesFunc, streams genericiooptions.IOStreams) *outputResourcesOptions {
	return &outputResourcesOptions{
		outputResourcesFn: outputResourcesFn,
		streams:           streams,
	}
}

func (o *outputResourcesOptions) Run(ctx context.Context) error {
	result, err := o.outputResourcesFn(ctx)
	if err != nil {
		return err
	}

	outputResourcesYAML, err := yaml.Marshal(result)
	if err != nil {
		return err
	}

	if _, err := fmt.Fprint(o.streams.Out, string(outputResourcesYAML)); err != nil {
		return err
	}

	return nil
}
