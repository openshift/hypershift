package libraryinputresources

import (
	"context"
	"errors"
	"fmt"
	"github.com/openshift/multi-operator-manager/pkg/library/libraryoutputresources"

	"k8s.io/cli-runtime/pkg/genericiooptions"
	"sigs.k8s.io/yaml"
)

type inputResourcesOptions struct {
	inputResourcesFn  InputResourcesFunc
	outputResourcesFn libraryoutputresources.OutputResourcesFunc

	streams genericiooptions.IOStreams
}

func newInputResourcesOptions(inputResourcesFn InputResourcesFunc, outputResourcesFn libraryoutputresources.OutputResourcesFunc, streams genericiooptions.IOStreams) *inputResourcesOptions {
	return &inputResourcesOptions{
		inputResourcesFn:  inputResourcesFn,
		outputResourcesFn: outputResourcesFn,
		streams:           streams,
	}
}

func (o *inputResourcesOptions) Run(ctx context.Context) error {
	errs := []error{}
	inputResources, err := o.inputResourcesFn(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("failed generating input resources: %w", err))
	}
	outputResources, err := o.outputResourcesFn(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("failed generating input resources: %w", err))
	}
	convertedResources := convertOutputToInput(outputResources)
	inputResources.ApplyConfigurationResources.ExactResources = append(inputResources.ApplyConfigurationResources.ExactResources, convertedResources.ApplyConfigurationResources.ExactResources...)
	inputResources.ApplyConfigurationResources.GeneratedNameResources = append(inputResources.ApplyConfigurationResources.GeneratedNameResources, convertedResources.ApplyConfigurationResources.GeneratedNameResources...)

	errs = append(errs, validateInputResources(inputResources)...)

	inputResourcesYAML, err := yaml.Marshal(inputResources)
	if err != nil {
		errs = append(errs, fmt.Errorf("failed marshalling input resources: %w", err))
	}

	if _, err := fmt.Fprint(o.streams.Out, string(inputResourcesYAML)); err != nil {
		errs = append(errs, fmt.Errorf("failed outputing input resources: %w", err))
	}

	return errors.Join(errs...)
}

func convertOutputToInput(outputResources *libraryoutputresources.OutputResources) *InputResources {
	inputResources := &InputResources{}

	resourceList := []libraryoutputresources.ResourceList{outputResources.ConfigurationResources}
	for _, currResourceList := range resourceList {
		for _, curr := range currResourceList.ExactResources {
			inputResources.ApplyConfigurationResources.ExactResources = append(inputResources.ApplyConfigurationResources.ExactResources, ExactResourceID{
				InputResourceTypeIdentifier: InputResourceTypeIdentifier{
					Group:    curr.Group,
					Version:  curr.Version,
					Resource: curr.Resource,
				},
				Namespace: curr.Namespace,
				Name:      curr.Name,
			})
		}
		for _, curr := range currResourceList.GeneratedNameResources {
			inputResources.ApplyConfigurationResources.GeneratedNameResources = append(inputResources.ApplyConfigurationResources.GeneratedNameResources, GeneratedResourceID{
				InputResourceTypeIdentifier: InputResourceTypeIdentifier{
					Group:    curr.Group,
					Version:  curr.Version,
					Resource: curr.Resource,
				},
				Namespace:     curr.Namespace,
				GeneratedName: curr.GeneratedName,
			})
		}
	}

	return inputResources
}
