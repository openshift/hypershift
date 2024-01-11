package framework

import (
	"context"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/go-logr/logr"
)

type Builder func(ctx context.Context, logger logr.Logger, opts *Options) (Cleanup, error)

//go:embed assets
var assets embed.FS

// InstallAssets applies static assets to the cluster for setup.
//
// A closure is returned that knows how to clean this emulated process up.
func InstallAssets(ctx context.Context, logger logr.Logger, opts *Options) (Cleanup, error) {
	return func() error {
			if SkippedCleanupSteps().HasAny("all", "assets") {
				return nil
			}
			logger.Info("cleaning up assets")
			return fs.WalkDir(assets, "assets", processAsset(logger, func(path string, content io.Reader) error {
				logPath := filepath.Join("install", path+".delete.log")
				cmd := exec.CommandContext(ctx, opts.OCPath,
					"delete", "--ignore-not-found", "-f", "-", "--kubeconfig", opts.Kubeconfig,
				)
				cmd.Stdin = content
				if err := RunCommand(logger, opts, logPath, cmd); err != nil {
					return fmt.Errorf("failed to delete %s: %w", path, err)
				}
				return nil
			}))
		}, fs.WalkDir(assets, "assets", processAsset(logger, func(path string, content io.Reader) error {
			logPath := filepath.Join("install", path+".apply.log")
			cmd := exec.CommandContext(ctx, opts.OCPath,
				"apply", "--server-side", "-f", "-", "--kubeconfig", opts.Kubeconfig,
			)
			cmd.Stdin = content
			if err := RunCommand(logger, opts, logPath, cmd); err != nil {
				return fmt.Errorf("failed to apply %s: %w", path, err)
			}
			return nil
		}))
}

func processAsset(logger logr.Logger, process func(path string, content io.Reader) error) fs.WalkDirFunc {
	return func(path string, d fs.DirEntry, err error) error {
		if filepath.Ext(path) != ".yaml" {
			return nil
		}
		content, err := assets.Open(path)
		if err != nil {
			return err
		}
		defer func() {
			if err := content.Close(); err != nil {
				logger.Error(err, "couldn't close asset file")
			}
		}()
		return process(path, content)
	}
}

// InstallHyperShiftOperator generates and applies assets to the cluster for setup of the HyperShift Operator.
//
// A closure is returned that knows how to clean this emulated process up.
func InstallHyperShiftOperator(ctx context.Context, logger logr.Logger, opts *Options) (Cleanup, error) {
	installLogPath := filepath.Join("install", "hypershift-install.log")
	renderCmd := exec.CommandContext(ctx, opts.HyperShiftCLIPath,
		"install", "render",
		"--format=yaml",
		"--development", // scales the deployment to 0, so we can run it locally
		"--enable-ci-debug-output",
		// Since we're not running the HyperShift operator in the cluster, these webhooks would not resolve.
		// These are not strictly required, but we should keep in mind that we're drifting from a production
		// deployment by turning them off.
		"--enable-conversion-webhook=false",
		"--enable-defaulting-webhook=false",
		"--enable-validating-webhook=false",
	)
	yamlPath := filepath.Join("install", "hypershift-install.yaml")
	yamlFile, err := Artifact(opts, yamlPath)
	if err != nil {
		return CleanupSentinel, err
	}
	renderCmd.Stdout = yamlFile

	if err := RunCommand(logger, opts, installLogPath, renderCmd); err != nil {
		return CleanupSentinel, fmt.Errorf("failed to run hypershift install render: %w", err)
	}

	applyLogPath := filepath.Join("install", "hypershift-install.apply.log")
	applyCmd := exec.CommandContext(ctx, opts.OCPath,
		"apply", "--server-side", "-f", filepath.Join(opts.ArtifactDir, yamlPath), "--kubeconfig", opts.Kubeconfig,
	)
	if err := RunCommand(logger, opts, applyLogPath, applyCmd); err != nil {
		return CleanupSentinel, fmt.Errorf("failed to apply rendered install artifacts: %w", err)
	}

	return func() error {
		if SkippedCleanupSteps().HasAny("all", "hypershift-operator") {
			return nil
		}
		logger.Info("cleaning up hosted hypershift operator assets")
		deleteLogPath := filepath.Join("install", "hypershift-install.delete.log")
		deleteCmd := exec.CommandContext(ctx, opts.OCPath,
			"delete", "--ignore-not-found", "-f", filepath.Join(opts.ArtifactDir, yamlPath), "--kubeconfig", opts.Kubeconfig,
		)
		return RunCommand(logger, opts, deleteLogPath, deleteCmd)
	}, nil
}

// EmulateHyperShiftOperator runs a local process for the HyperShift Operator.
//
// A closure is returned that knows how to clean this emulated process up.
func EmulateHyperShiftOperator(ctx context.Context, logger logr.Logger, opts *Options) (Cleanup, error) {
	cleanup, err := EmulateDeployment(
		ctx, logger, opts, 10*time.Second, InjectKubeconfigEnv,
		"hypershift", "operator", "operator", opts.HyperShiftOperatorPath,
		"--control-plane-operator-image", "fake",
	)
	if err != nil {
		return cleanup, err
	}
	return func() error {
		if SkippedCleanupSteps().HasAny("all", "hypershift-operator") {
			return nil
		}
		logger.Info("cleaning up hypershift-operator")
		return cleanup()
	}, nil
}
