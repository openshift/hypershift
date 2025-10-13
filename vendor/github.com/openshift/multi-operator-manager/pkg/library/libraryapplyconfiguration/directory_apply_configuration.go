package libraryapplyconfiguration

import (
	"errors"
	"fmt"
	"io/fs"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/yaml"
	"os"
	"path/filepath"
)

type ApplyConfigurationResult interface {
	Error() error
	OutputDirectory() (string, error)
	Stdout() string
	Stderr() string
	ControllerResults() *ApplyConfigurationRunResult

	AllDesiredMutationsGetter
}

type simpleApplyConfigurationResult struct {
	err               error
	outputDirectory   string
	stdout            string
	stderr            string
	controllerResults *ApplyConfigurationRunResult

	applyConfiguration *applyConfiguration
}

var (
	_ AllDesiredMutationsGetter = &simpleApplyConfigurationResult{}
	_ ApplyConfigurationResult  = &simpleApplyConfigurationResult{}
)

func NewApplyConfigurationResultFromDirectory(inFS fs.FS, outputDirectory string, execError error) (ApplyConfigurationResult, error) {
	errs := []error{}
	var err error

	stdoutContent := []byte{}
	stdoutLocation := filepath.Join(outputDirectory, "stdout.log")
	stdoutContent, err = fs.ReadFile(inFS, "stdout.log")
	if err != nil && !os.IsNotExist(err) {
		errs = append(errs, fmt.Errorf("failed reading %q: %w", stdoutLocation, err))
	}
	// TODO stream through and preserve first and last to avoid memory explosion
	if len(stdoutContent) > 512*1024 {
		indexToStart := len(stdoutContent) - (512 * 1024)
		stdoutContent = stdoutContent[indexToStart:]
	}

	stderrContent := []byte{}
	stderrLocation := filepath.Join(outputDirectory, "stderr.log")
	stderrContent, err = fs.ReadFile(inFS, "stderr.log")
	if err != nil && !os.IsNotExist(err) {
		errs = append(errs, fmt.Errorf("failed reading %q: %w", stderrLocation, err))
	}
	// TODO stream through and preserve first and last to avoid memory explosion
	if len(stderrContent) > 512*1024 {
		indexToStart := len(stderrContent) - (512 * 1024)
		stderrContent = stderrContent[indexToStart:]
	}

	var controllerResults *ApplyConfigurationRunResult
	controllerResultsContent := []byte{}
	controllerResultsLocation := filepath.Join(outputDirectory, "controller-results.yaml")
	controllerResultsContent, err = fs.ReadFile(inFS, "controller-results.yaml")
	if err != nil && !os.IsNotExist(err) {
		errs = append(errs, fmt.Errorf("failed reading %q: %w", controllerResultsLocation, err))
	}
	if len(controllerResultsContent) > 0 {
		if asJSON, err := yaml.ToJSON(controllerResultsContent); err != nil {
			errs = append(errs, fmt.Errorf("unable to convert controller-results.yaml to json: %w", err))
		} else {
			localControllerResults := &ApplyConfigurationRunResult{}
			if err := json.Unmarshal(asJSON, localControllerResults); err != nil {
				errs = append(errs, fmt.Errorf("unable to parse controller-results.yaml: %w", err))
			} else {
				controllerResults = localControllerResults
			}
		}
	}

	outputContent, err := fs.ReadDir(inFS, ".")
	switch {
	case errors.Is(err, fs.ErrNotExist) && execError != nil:
		return &simpleApplyConfigurationResult{
			stdout:          string(stdoutContent),
			stderr:          string(stderrContent),
			outputDirectory: outputDirectory,

			applyConfiguration: &applyConfiguration{},
		}, execError

	case errors.Is(err, fs.ErrNotExist) && execError == nil:
		return nil, fmt.Errorf("unable to read output-dir content %q: %w", outputDirectory, err)

	case err != nil:
		return nil, fmt.Errorf("unable to read output-dir content %q: %w", outputDirectory, err)
	}

	// at this point we either
	// 1. had an execError and we were able to read the directory
	// 2. did not have an execError we were able to read the directory

	ret := &simpleApplyConfigurationResult{
		stdout:             string(stdoutContent),
		stderr:             string(stderrContent),
		controllerResults:  controllerResults,
		outputDirectory:    outputDirectory,
		applyConfiguration: &applyConfiguration{},
	}
	ret.applyConfiguration, err = newApplyConfigurationFromDirectory(inFS, outputDirectory)
	if err != nil {
		errs = append(errs, fmt.Errorf("failure building applyConfiguration result: %w", err))
	}

	// check to be sure we don't have any extra content
	for _, currContent := range outputContent {
		if currContent.Name() == "stdout.log" {
			continue
		}
		if currContent.Name() == "stderr.log" {
			continue
		}
		if currContent.Name() == "controller-results.yaml" {
			continue
		}

		if !currContent.IsDir() {
			errs = append(errs, fmt.Errorf("unexpected file %q, only target cluster directories are: %v", filepath.Join(outputDirectory, currContent.Name()), sets.List(AllClusterTypes)))
			continue
		}
		if !AllClusterTypes.Has(ClusterType(currContent.Name())) {
			errs = append(errs, fmt.Errorf("unexpected file %q, only target cluster directories are: %v", filepath.Join(outputDirectory, currContent.Name()), sets.List(AllClusterTypes)))
			continue
		}
	}

	// if we had an exec error, be sure we add it to the list of failures.
	if len(errs) == 0 && execError != nil {
		return ret, execError
	}
	if len(errs) > 0 && execError != nil {
		errs = append(errs, execError)
	}

	ret.err = errors.Join(errs...)
	if ret.err != nil {
		// TODO may decide to disallow returning any info later
		return ret, ret.err
	}
	return ret, nil
}

func (s *simpleApplyConfigurationResult) Stdout() string {
	return s.stdout
}

func (s *simpleApplyConfigurationResult) Stderr() string {
	return s.stderr
}

func (s *simpleApplyConfigurationResult) Error() error {
	return s.err
}

func (s *simpleApplyConfigurationResult) ControllerResults() *ApplyConfigurationRunResult {
	return s.controllerResults
}

func (s *simpleApplyConfigurationResult) OutputDirectory() (string, error) {
	return s.outputDirectory, nil
}

func (s *simpleApplyConfigurationResult) MutationsForClusterType(clusterType ClusterType) SingleClusterDesiredMutationGetter {
	return s.applyConfiguration.MutationsForClusterType(clusterType)
}
