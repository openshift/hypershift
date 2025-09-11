package libraryapplyconfiguration

import (
	"context"
	"errors"
	"fmt"
	"github.com/openshift/library-go/pkg/manifestclient"
	"os"
	"path/filepath"
	"sigs.k8s.io/yaml"

	"github.com/openshift/multi-operator-manager/pkg/library/libraryoutputresources"
)

type applyConfigurationOptions struct {
	applyConfigurationFn ApplyConfigurationFunc
	outputResourcesFn    libraryoutputresources.OutputResourcesFunc

	input ApplyConfigurationInput

	outputDirectory string
}

func newApplyConfigurationOptions(
	applyConfigurationFn ApplyConfigurationFunc,
	outputResourcesFn libraryoutputresources.OutputResourcesFunc,
	input ApplyConfigurationInput,
	outputDirectory string) *applyConfigurationOptions {
	return &applyConfigurationOptions{
		applyConfigurationFn: applyConfigurationFn,
		outputResourcesFn:    outputResourcesFn,
		input:                input,
		outputDirectory:      outputDirectory,
	}
}

func (o *applyConfigurationOptions) Run(ctx context.Context) error {
	if err := os.MkdirAll(o.outputDirectory, 0755); err != nil && !os.IsExist(err) {
		return fmt.Errorf("unable to create output directory %q:%v", o.outputDirectory, err)
	}

	errs := []error{}
	allAllowedOutputResources, err := o.outputResourcesFn(ctx)
	if err != nil {
		errs = append(errs, err)
	}

	controllerResults, mutations, err := o.applyConfigurationFn(ctx, o.input)
	if err != nil {
		errs = append(errs, err)
	}

	// also validate the raw results because filtering may have eliminated "bad" output.
	unspecifiedOutputResources := UnspecifiedOutputResources(mutations, allAllowedOutputResources)
	if err := ValidateAllDesiredMutationsGetter(mutations, allAllowedOutputResources); err != nil {
		errs = append(errs, err)
	}

	// now filter the results and check them
	filteredResult := FilterAllDesiredMutationsGetter(mutations, allAllowedOutputResources)
	if err := ValidateAllDesiredMutationsGetter(filteredResult, allAllowedOutputResources); err != nil {
		errs = append(errs, err)
	}

	if err := WriteApplyConfiguration(filteredResult, o.outputDirectory); err != nil {
		errs = append(errs, err)
	}
	if len(unspecifiedOutputResources) > 0 {
		if err := manifestclient.WriteMutationDirectory(filepath.Join(o.outputDirectory, "Unspecified"), unspecifiedOutputResources...); err != nil {
			errs = append(errs, err)
		}
	}

	if controllerResultBytes, err := yaml.Marshal(controllerResults); err != nil {
		errs = append(errs, fmt.Errorf("failed marshalling controller results: %w", err))
	} else {
		if err := os.WriteFile(filepath.Join(o.outputDirectory, "controller-results.yaml"), controllerResultBytes, 0644); err != nil {
			errs = append(errs, fmt.Errorf("failed writing controller results: %w", err))
		}
	}

	return errors.Join(errs...)
}
