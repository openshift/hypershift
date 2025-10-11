package applyconfiguration

import (
	"context"
	"errors"
	"fmt"
	"github.com/openshift/multi-operator-manager/pkg/library/libraryapplyconfiguration"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type ApplyConfigurationOptions struct {
	InputDirectory  string
	OutputDirectory string
	Now             time.Time
	Controllers     []string

	// Env specifies the environment of the process.
	// Each entry is of the form "key=value".
	Env []string
}

// ExecApplyConfiguration takes a binaryPath, inputDir, and desiredOutputDir and runs the binary
// It then reads the result directory and returns the result.
func ExecApplyConfiguration(ctx context.Context, binaryPath string, options ApplyConfigurationOptions) (libraryapplyconfiguration.ApplyConfigurationResult, error) {
	// the cmd.Wait() closes these output files.
	stdoutFilename := filepath.Join(options.OutputDirectory, "stdout.log")
	stdoutFile, err := os.OpenFile(stdoutFilename, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("unable to open stdout.log: %w", err)
	}
	stderrFilename := filepath.Join(options.OutputDirectory, "stderr.log")
	stderrFile, err := os.OpenFile(stderrFilename, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("unable to open stderr.log: %w", err)
	}

	args := []string{
		"apply-configuration",
		"--input-dir", options.InputDirectory,
		"--output-dir", options.OutputDirectory,
	}
	if !options.Now.IsZero() {
		args = append(args, "--now", options.Now.Format(time.RFC3339))
	}
	if len(options.Controllers) > 0 {
		args = append(args, "--controllers", strings.Join(options.Controllers, ","))
	}

	// TODO prove that the timeout works if the process captures sig-int
	processCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(processCtx, binaryPath, args...)
	cmd.Stdout = stdoutFile
	cmd.Stderr = stderrFile
	cmd.Env = options.Env
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start process: %w", err)
	}

	if err := cmd.Wait(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			if err := stdoutFile.Close(); err != nil {
				utilruntime.HandleError(err)
			}
			if err := stderrFile.Close(); err != nil {
				utilruntime.HandleError(err)
			}
			return libraryapplyconfiguration.NewApplyConfigurationResultFromDirectory(
				os.DirFS(options.OutputDirectory),
				options.OutputDirectory,
				fmt.Errorf("failed to wait for process %v: %w stderr: %v", cmd, err, string(exitErr.Stderr)))
		}
		return libraryapplyconfiguration.NewApplyConfigurationResultFromDirectory(
			os.DirFS(options.OutputDirectory),
			options.OutputDirectory,
			fmt.Errorf("failed to wait for process: %w", err))
	}

	return libraryapplyconfiguration.NewApplyConfigurationResultFromDirectory(
		os.DirFS(options.OutputDirectory),
		options.OutputDirectory,
		nil)
}
