package framework

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/go-logr/logr"
	"github.com/openshift/hypershift/support/supportedversion"
	"k8s.io/apimachinery/pkg/util/sets"
)

func InterruptableContext(parent context.Context) context.Context {
	// Set up a root context for all tests and set up signal handling
	ctx, cancel := context.WithCancel(parent)
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		fmt.Println("Received an interrupt, cancelling test context...")
		cancel()
	}()
	return ctx
}

type Cleanup func(ctx context.Context) error

// CleanupSentinel is a helper for returning a no-op cleanup function.
func CleanupSentinel(_ context.Context) error {
	return nil
}

// SkippedCleanupSteps parses $SKIP_CLEANUP as a comma-delimited list of cleanup steps to skip.
func SkippedCleanupSteps() sets.Set[string] {
	skip := os.Getenv("SKIP_CLEANUP")
	if skip == "" {
		return sets.New[string]()
	}
	parts := strings.Split(skip, ",")
	return sets.New[string](parts...)
}

type InjectKubeconfigMode string

const (
	InjectKubeconfigFlag InjectKubeconfigMode = "flag"
	InjectKubeconfigEnv  InjectKubeconfigMode = "env"
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

type HostedClusterOptions struct {
	// Note: this is not well tested. If you ask for a CPO debug deployment, for instance,
	// we won't be able to give a guest cluster kubeconfig. Use with care.
	// Options: control-plane-operator,ignition-server,hosted-cluster-config-operator,control-plane-pki-operator
	DebugDeployments []string
}

const HostedClusterNamespace = "hosted-clusters"

// InstallHostedCluster generates and applies assets to the cluster for setup of a HostedCluster.
//
// A closure is returned that knows how to clean this emulated process up.
func InstallHostedCluster(ctx context.Context, logger logr.Logger, opts *Options, hostedClusterOpts HostedClusterOptions, t *testing.T, args ...string) (Cleanup, error) {
	hostedClusterName := HostedClusterFor(t)
	t.Logf("installing HostedCluster %s", hostedClusterName)
	logger.Info("rendering hosted cluster assets", "hostedCluster", hostedClusterName)

	pullSpec := opts.ReleaseImage
	if pullSpec == "" {
		resp, err := http.Get(fmt.Sprintf(`https://multi.ocp.releases.ci.openshift.org/api/v1/releasestream/%s-0.nightly-multi/latest`, supportedversion.LatestSupportedVersion.String()))
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
		pullSpec = info.PullSpec
	}

	cmdArgs := []string{
		"create", "cluster", "none",
		"--name", hostedClusterName,
		"--namespace", HostedClusterNamespace,
		"--annotations", "hypershift.openshift.io/control-plane-operator-image=" + opts.ControlPlaneOperatorImage,
		"--annotations", "hypershift.openshift.io/control-plane-operator-image-labels=" + opts.ControlPlaneOperatorImageLabels,
		"--annotations", "hypershift.openshift.io/pod-security-admission-label-override=baseline",
		"--release-image", pullSpec,
		"--pull-secret", opts.PullSecret,
		"--render",
	}
	if len(hostedClusterOpts.DebugDeployments) > 0 {
		cmdArgs = append(cmdArgs, "--annotations", "hypershift.openshift.io/debug-deployments="+strings.Join(hostedClusterOpts.DebugDeployments, ","))
	}

	installLogPath := "render.log"
	renderCmd := exec.CommandContext(ctx, opts.HyperShiftCLIPath, append(args, cmdArgs...)...)
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

	cleanup := func(ctx context.Context) error {
		if SkippedCleanupSteps().HasAny("all", "hosted-clusters") {
			return nil
		}
		logger.Info("dumping hosted hosted cluster assets")
		dumpLogPath := filepath.Join("install", "assets.dump.yaml")
		dumpCmd := exec.CommandContext(ctx, opts.OCPath,
			"get", "--ignore-not-found", "--show-managed-fields", "-f", filepath.Join(opts.ArtifactDir, yamlPath), "--kubeconfig", opts.Kubeconfig,
		)
		if err := RunCommand(logger, opts, dumpLogPath, dumpCmd); err != nil {
			logger.Error(err, "failed to dump hosted cluster assets")
		}

		logger.Info("cleaning up hosted cluster assets")
		deleteLogPath := "assets.delete.log"
		deleteCmd := exec.CommandContext(ctx, opts.OCPath,
			"delete", "--ignore-not-found", "-f", filepath.Join(opts.ArtifactDir, yamlPath), "--kubeconfig", opts.Kubeconfig,
		)
		return RunCommand(logger, opts, deleteLogPath, deleteCmd)
	}

	// TODO: logs from the HostedCluster namespace - can we reuse e2e?
	return cleanup, nil
}

func WaitForHostedClusterAvailable(ctx context.Context, logger logr.Logger, opts *Options, t *testing.T) (Cleanup, error) {
	hostedClusterName := HostedClusterFor(t)
	t.Logf("waiting for HostedCluster %s to be ready", hostedClusterName)

	waitLogPath := filepath.Join("hosted-cluster.wait.log")
	waitCmd := exec.CommandContext(ctx, opts.OCPath,
		"wait", "--for", "condition=Available",
		"hostedcluster/"+hostedClusterName, "--namespace", HostedClusterNamespace,
		"--timeout", "-1s", "--kubeconfig", opts.Kubeconfig,
	)

	// TODO: logs from the HostedCluster namespace - can we reuse e2e?
	return CleanupSentinel, RunCommand(logger, opts, waitLogPath, waitCmd)
}
