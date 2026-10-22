//go:build e2ev2

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	clusterdump "github.com/openshift/hypershift/cmd/cluster/dump"
	cmdutil "github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/test/e2e/v2/lifecycle"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type teardownResult struct {
	destroyError    error
	diagnosticError error
}

const teardownDiagnosticTimeout = 5 * time.Minute

func destroyClusterWithDiagnostics(ctx context.Context, binary, artifactDir string, entry lifecycle.ClusterEntry, platform lifecycle.PlatformConfig) (result teardownResult) {
	return destroyClusterWithDiagnosticCollectors(ctx, binary, artifactDir, entry, platform, func(ctx context.Context, directory string, guest bool) error {
		return collectTeardownDiagnostics(ctx, platform.Name() == "aws",
			func(ctx context.Context) error { return dumpAWSConsole(ctx, entry, directory) },
			func(ctx context.Context) error {
				return dumpHostedClusterDiagnostics(ctx, directory, entry, platform, guest, clusterdump.DumpCluster)
			},
			func(ctx context.Context) error { return dumpAWSJournals(ctx, entry, directory) },
		)
	})
}

func collectTeardownDiagnostics(ctx context.Context, aws bool, console, dump, journals func(context.Context) error) error {
	if aws {
		if err := console(ctx); err != nil {
			log.Printf("Failed saving machine console logs; this is nonfatal: %v", err)
		}
	}
	err := dump(ctx)
	if aws {
		if err := journals(ctx); err != nil {
			log.Printf("Failed to dump machine journals; this is nonfatal: %v", err)
		}
	}
	return err
}

func destroyClusterWithDiagnosticCollectors(ctx context.Context, binary, artifactDir string, entry lifecycle.ClusterEntry, platform lifecycle.PlatformConfig, collect func(context.Context, string, bool) error) (result teardownResult) {
	directory, err := teardownArtifactDirectory(artifactDir, entry.Name)
	if err != nil {
		result.diagnosticError = err
	}
	if directory != "" {
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), teardownDiagnosticTimeout)
			defer cancel()
			err := clusterdump.CreateArchive(ctx, &clusterdump.DumpOptions{ArtifactDir: directory, Log: logr.Discard()})
			if err != nil {
				result.diagnosticError = errors.Join(result.diagnosticError, fmt.Errorf("archive teardown diagnostics: %w", err))
			}
		}()
		if err := collect(ctx, directory, true); err != nil {
			result.diagnosticError = errors.Join(result.diagnosticError, fmt.Errorf("dump before destroy: %w", err))
		}
	}
	result.destroyError = retryDestroy(ctx, 5*time.Second, func(ctx context.Context) error {
		return destroyCluster(ctx, binary, entry, platform)
	}, func(ctx context.Context) {
		if directory == "" {
			return
		}
		if err := collect(ctx, directory, false); err != nil {
			log.Printf("Failed to dump cluster during destroy; this is nonfatal: %v", err)
		}
	})
	return result
}

func dumpHostedClusterDiagnostics(parent context.Context, directory string, entry lifecycle.ClusterEntry, platform lifecycle.PlatformConfig, guest bool, dump func(context.Context, *clusterdump.DumpOptions) error) (result error) {
	ctx, cancel := context.WithTimeout(parent, teardownDiagnosticTimeout)
	defer cancel()
	file, err := os.Create(filepath.Join(directory, "dump.log"))
	if err != nil {
		return err
	}
	logger := zap.New(zapcore.NewCore(zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()), zapcore.Lock(file), zap.DebugLevel))
	defer func() {
		result = errors.Join(result, closeDiagnosticLog(file, logger))
	}()
	var diagnosticErrors []error
	opts := &clusterdump.DumpOptions{
		Namespace: entry.Namespace, Name: entry.Name, ArtifactDir: directory,
		IsDumpingGuestCluster: guest, Log: zapr.NewLogger(logger),
		DumpGuestClusterPolicies: map[clusterdump.DumpGuestClusterPolicy]struct{}{},
		ClientProvider:           &cmdutil.ClientProvider{ControllerRuntimeClient: cmdutil.GetClientWithKubeconfig, Config: cmdutil.GetConfigWithKubeconfig},
		LogCheckers: []clusterdump.LogChecker{func(filename string, content []byte) {
			if bytes.Contains(content, []byte(upsert.LoopDetectorWarningMessage)) {
				diagnosticErrors = append(diagnosticErrors, fmt.Errorf("found %s messages in file %s", upsert.LoopDetectorWarningMessage, filename))
			}
		}},
	}
	for _, spec := range platform.ClusterSpecs("", "") {
		if spec.Variant != entry.Variant {
			continue
		}
		for _, argument := range spec.DumpArgs {
			const azurePrefix = "--azure-creds="
			if strings.HasPrefix(argument, azurePrefix) {
				opts.Azure = &clusterdump.AzureDumpOptions{CredentialsFile: strings.TrimPrefix(argument, azurePrefix)}
			}
		}
	}
	if err := dump(ctx, opts); err != nil {
		diagnosticErrors = append(diagnosticErrors, err)
	}
	return errors.Join(diagnosticErrors...)
}

func closeDiagnosticLog(file *os.File, logger *zap.Logger) error {
	var result error
	if err := logger.Sync(); err != nil {
		result = fmt.Errorf("flush diagnostic log %s: %w", file.Name(), err)
	}
	if err := file.Close(); err != nil {
		result = errors.Join(result, fmt.Errorf("close diagnostic log %s: %w", file.Name(), err))
	}
	return result
}

type destroyCommandError struct {
	err      error
	terminal bool
}

func (err *destroyCommandError) Error() string { return err.err.Error() }
func (err *destroyCommandError) Unwrap() error { return err.err }

func retryDestroy(ctx context.Context, interval time.Duration, destroy func(context.Context) error, dump func(context.Context)) error {
	var previousError string
	var lastError error
	for {
		if err := ctx.Err(); err != nil {
			return errors.Join(lastError, err)
		}
		err := destroy(ctx)
		if err == nil {
			return nil
		}
		lastError = err
		var commandError *destroyCommandError
		var exitError *exec.ExitError
		if errors.As(err, &commandError) && (commandError.terminal || !errors.As(err, &exitError)) {
			return err
		}
		if previousError != err.Error() {
			log.Printf("Failed to destroy cluster, will retry: %v", err)
			previousError = err.Error()
		}
		dump(ctx)
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return errors.Join(err, ctx.Err())
		case <-timer.C:
		}
	}
}

func teardownArtifactDirectory(artifactDir, name string) (string, error) {
	if artifactDir == "" {
		return "", nil
	}
	if name == "" || filepath.Base(name) != name || name == "." || name == ".." {
		return "", fmt.Errorf("invalid cluster artifact directory name")
	}
	parent := filepath.Join(artifactDir, name)
	if err := os.MkdirAll(parent, 0755); err != nil {
		return "", fmt.Errorf("create teardown artifact parent: %w", err)
	}
	directory, err := os.MkdirTemp(parent, "teardown-")
	if err != nil {
		return "", fmt.Errorf("create isolated teardown artifact directory: %w", err)
	}
	return directory, nil
}
