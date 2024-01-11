package framework

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/openshift/hypershift/support/supportedversion/supported"
	"k8s.io/apimachinery/pkg/util/errors"
)

// ArtifactDirectoryFor transforms t.Name() into a string that can be used for a directory name. Adapted from
// the core testutil golden fixture logic.
func ArtifactDirectoryFor(t *testing.T) string {
	result := strings.Builder{}
	for _, r := range t.Name() {
		if (r >= 'a' && r < 'z') || (r >= 'A' && r < 'Z') || r == '_' || r == '.' || (r >= '0' && r <= '9') {
			// The thing is documented as returning a nil error so lets just drop it
			_, _ = result.WriteRune(r)
			continue
		}
		if !strings.HasSuffix(result.String(), "_") {
			result.WriteRune('_')
		}
	}
	return result.String()
}

// HostedClusterFor hashes t.Name() to create a name for the HostedCluster.
// base36(sha224(value)) produces a useful, deterministic value that fits the requirements to be
// a Kubernetes object name (honoring length requirement, is a valid DNS subdomain, etc)
func HostedClusterFor(t *testing.T) string {
	hash := sha256.Sum224([]byte(t.Name()))
	var i big.Int
	i.SetBytes(hash[:])
	return i.Text(36)
}

const HostedClusterNamespace = "hosted-clusters"

// SetupHostedCluster does all the setup necessary to actuate a HostedCluster.
func SetupHostedCluster(ctx context.Context, logger logr.Logger, opts *Options, t *testing.T, args ...string) (Cleanup, error) {
	opt := *opts
	opt.ArtifactDir = filepath.Join(opts.ArtifactDir, ArtifactDirectoryFor(t))

	var cleanups []Cleanup
	cleanup := func() error {
		var errs []error
		for _, cleanup := range cleanups {
			errs = append(errs, cleanup())
		}
		return errors.NewAggregate(errs)
	}
	assetCleanup, err := InstallHostedCluster(ctx, logger, &opt, t, args...)
	if err != nil {
		return assetCleanup, err
	}
	cleanups = append(cleanups, assetCleanup)

	processCleanup, err := EmulateHostedClusterOperators(ctx, logger, &opt, t)
	cleanups = append(cleanups, processCleanup)
	return cleanup, err
}

// InstallHostedCluster generates and applies assets to the cluster for setup of a HostedCluster.
//
// A closure is returned that knows how to clean this emulated process up.
func InstallHostedCluster(ctx context.Context, logger logr.Logger, opts *Options, t *testing.T, args ...string) (Cleanup, error) {
	hostedClusterName := HostedClusterFor(t)
	logger.Info("rendering hosted cluster assets", "hostedCluster", hostedClusterName)

	resp, err := http.Get(fmt.Sprintf(`https://multi.ocp.releases.ci.openshift.org/api/v1/releasestream/%s-0.nightly-multi/latest`, supported.LatestSupportedVersion.String()))
	if err != nil {
		return CleanupSentinel, fmt.Errorf("couldn't fetch latest release image: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			logger.Error(err, "failed to close http body")
		}
	}()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error(err, "failed to read http body")
	}
	if resp.StatusCode != http.StatusOK {
		return CleanupSentinel, fmt.Errorf("couldn't fetch latest release image: HTTP %d: %v", resp.StatusCode, string(body))
	}
	type releaseInfo struct {
		Name     string `json:"name"`
		PullSpec string `json:"pullSpec"`
	}
	var info releaseInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return CleanupSentinel, fmt.Errorf("failed to parse release info: %w", err)
	}
	logger.Info("resolved latest release", "name", info.Name, "pullSpec", info.PullSpec)

	installLogPath := "render.log"
	renderCmd := exec.CommandContext(ctx, opts.HyperShiftCLIPath,
		append(args, "create", "cluster", "none",
			"--name", hostedClusterName,
			"--namespace", HostedClusterNamespace,
			// scales the deployments to 0, so we can run them locally
			"--annotations", "hypershift.openshift.io/debug-deployments=control-plane-operator,hosted-cluster-config-operator,control-plane-pki-operator",
			"--annotations", "hypershift.openshift.io/pod-security-admission-label-override=baseline",
			"--release-image", info.PullSpec,
			"--pull-secret", opts.PullSecret,
			"--render")...,
	)
	renderCmd.Env = append(renderCmd.Env, "KUBECONFIG="+opts.Kubeconfig)
	yamlPath := "assets.yaml"
	yamlFile, err := Artifact(opts, yamlPath)
	if err != nil {
		return CleanupSentinel, err
	}
	renderCmd.Stdout = yamlFile

	if err := RunCommand(logger, opts, installLogPath, renderCmd); err != nil {
		return CleanupSentinel, fmt.Errorf("failed to run hypershift create cluster: %w", err)
	}

	applyLogPath := "assets.apply.log"
	applyCmd := exec.CommandContext(ctx, opts.OCPath,
		"apply", "--server-side", "-f", filepath.Join(opts.ArtifactDir, yamlPath), "--kubeconfig", opts.Kubeconfig,
	)
	if err := RunCommand(logger, opts, applyLogPath, applyCmd); err != nil {
		return CleanupSentinel, fmt.Errorf("failed to apply rendered artifacts: %w", err)
	}

	return func() error {
		if SkippedCleanupSteps().HasAny("all", "hosted-clusters") {
			return nil
		}
		logger.Info("cleaning up hosted cluster assets")
		deleteLogPath := "assets.delete.log"
		deleteCmd := exec.CommandContext(ctx, opts.OCPath,
			"delete", "--ignore-not-found", "-f", filepath.Join(opts.ArtifactDir, yamlPath), "--kubeconfig", opts.Kubeconfig,
		)
		return RunCommand(logger, opts, deleteLogPath, deleteCmd)
	}, nil
}

// EmulateHostedClusterOperators runs local processes for the operators that actuate a HostedCluster.
//
// A closure is returned that knows how to clean these emulated processes up.
func EmulateHostedClusterOperators(ctx context.Context, logger logr.Logger, opts *Options, t *testing.T) (Cleanup, error) {
	var cleanups []Cleanup
	var startupErrs []error
	for _, item := range []struct {
		name, containerName, path string
		args                      []string
		timeout                   time.Duration
		injectionMode             InjectKubeconfigMode
	}{
		{
			name:          "control-plane-operator",
			containerName: "control-plane-operator",
			path:          opts.ControlPlaneOperatorPath,
			args:          []string{"--in-cluster", "false"},
			timeout:       10 * time.Second,
			injectionMode: InjectKubeconfigEnv,
		},
		{
			name:          "control-plane-pki-operator",
			containerName: "control-plane-pki-operator",
			path:          opts.ControlPlanePKIOperatorPath,
			timeout:       10 * time.Second,
			injectionMode: InjectKubeconfigFlag,
		},
		{
			name:          "hosted-cluster-config-operator",
			containerName: "hosted-cluster-config-operator",
			path:          opts.ControlPlaneOperatorPath,
			timeout:       10 * time.Minute,
			injectionMode: InjectKubeconfigEnv,
		},
	} {
		item := item
		cleanup, err := EmulateDeployment(
			ctx, logger, opts,
			item.timeout, item.injectionMode,
			HostedClusterNamespace+"-"+HostedClusterFor(t), item.name, item.containerName, item.path, item.args...,
		)
		cleanups = append(cleanups, func() error {
			logger.Info("cleaning up " + item.name)
			return cleanup()
		})
		startupErrs = append(startupErrs, err)
	}

	return func() error {
		if SkippedCleanupSteps().HasAny("all", "hosted-clusters") {
			return nil
		}
		logger.Info("cleaning up hosted cluster operators")
		var errs []error
		for _, cleanup := range cleanups {
			errs = append(errs, cleanup())
		}
		return errors.NewAggregate(errs)
	}, errors.NewAggregate(startupErrs)
}
