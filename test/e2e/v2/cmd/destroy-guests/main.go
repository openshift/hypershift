//go:build e2ev2

/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// destroy-guests destroys all HostedClusters created by the v2 e2e
// lifecycle tests. Cluster identities are read from the cluster
// manifest written by create-guests to SHARED_DIR. Platform-specific
// destroy flags are captured in the manifest from each ClusterSpec.
// All clusters are destroyed in parallel with best-effort semantics.
package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"hash"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	cmdutil "github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/test/e2e/v2/lifecycle"
)

const clusterGracePeriod = "40m"

func main() {
	signalContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	timeout, err := destroyTimeout(os.Getenv("E2E_DESTROY_TIMEOUT"))
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(signalContext, timeout)
	defer cancel()
	sharedDir := os.Getenv("SHARED_DIR")
	if sharedDir == "" {
		log.Fatal("SHARED_DIR is required")
	}

	manifest, err := lifecycle.ReadManifest(sharedDir)
	if err != nil {
		log.Fatalf("Failed to read cluster manifest: %v", err)
	}

	platform, err := lifecycle.NewPlatformConfig(os.Getenv("HYPERSHIFT_PLATFORM"), sharedDir)
	if err != nil {
		log.Fatalf("Failed to initialize platform config: %v", err)
	}

	hypershiftBin := os.Getenv("HYPERSHIFT_BINARY")
	if hypershiftBin == "" {
		hypershiftBin = "hypershift"
	}

	log.Printf("Destroying %d clusters from manifest; total retry budget=%s, per-attempt cluster grace=%s", len(manifest.Clusters), timeout, clusterGracePeriod)

	var (
		mu            sync.Mutex
		failed        bool
		destroyFailed bool
		wg            sync.WaitGroup
	)

	for _, entry := range manifest.Clusters {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result := destroyClusterWithDiagnostics(ctx, hypershiftBin, os.Getenv("ARTIFACT_DIR"), entry, platform)
			if result.destroyError != nil {
				log.Printf("WARNING: Failed to destroy cluster %s (%s): %v", entry.Name, entry.Variant, result.destroyError)
				log.Printf("ACTION REQUIRED: cloud resources for cluster %s (infraID=%s) may be orphaned and need manual cleanup", entry.Name, entry.InfraID)
			}
			if result.diagnosticError != nil {
				log.Printf("WARNING: Failed to collect teardown diagnostics for cluster %s: %v", entry.Name, result.diagnosticError)
			}
			if result.destroyError != nil || result.diagnosticError != nil {
				mu.Lock()
				failed = true
				destroyFailed = destroyFailed || result.destroyError != nil
				mu.Unlock()
			}
		}()
	}

	wg.Wait()
	if platform.Name() == "aws" {
		if err := cleanupSharedOIDCAfterDestroy(sharedDir, destroyFailed, func(ctx context.Context) error {
			client, err := cmdutil.GetClientWithKubeconfig("")
			if err != nil {
				return err
			}
			return lifecycle.CleanupSharedOIDCIfUnused(ctx, sharedDir, client)
		}); err != nil {
			log.Printf("WARNING: Failed to clean up suite-owned shared OIDC resources: %v", err)
			failed = true
		}
	}

	if failed {
		log.Fatal("One or more cluster teardowns or diagnostic collections failed")
	}
	log.Printf("All clusters destroyed successfully")
}

func cleanupSharedOIDCAfterDestroy(sharedDir string, destroyFailed bool, cleanup func(context.Context) error) error {
	if destroyFailed {
		return nil
	}
	if _, err := os.Stat(filepath.Join(sharedDir, lifecycle.SharedOIDCStateFileName)); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	return cleanup(ctx)
}

func destroyTimeout(value string) (time.Duration, error) {
	if value == "" {
		return 2 * time.Hour, nil
	}
	timeout, err := time.ParseDuration(value)
	if err != nil || timeout <= 0 {
		return 0, fmt.Errorf("E2E_DESTROY_TIMEOUT must be a positive duration, got %q", value)
	}
	return timeout, nil
}

func destroyCluster(ctx context.Context, hypershiftBin string, entry lifecycle.ClusterEntry, platform lifecycle.PlatformConfig) error {
	log.Printf("Destroying cluster %s (%s, infraID=%s)", entry.Name, entry.Variant, entry.InfraID)

	args := []string{
		"destroy", "cluster", platform.Name(),
		"--name=" + entry.Name,
		"--namespace=" + entry.Namespace,
		"--infra-id=" + entry.InfraID,
		"--cluster-grace-period=" + clusterGracePeriod,
	}
	args = append(args, entry.DestroyArgs...)

	log.Printf("Running: %s %v", hypershiftBin, args)

	cmd := exec.CommandContext(ctx, hypershiftBin, args...)
	cmd.Stdout = os.Stdout
	stderr := &destroyErrorOutput{digest: sha256.New()}
	cmd.Stderr = io.MultiWriter(os.Stderr, stderr)
	if err := cmd.Run(); err != nil {
		return &destroyCommandError{
			err:      fmt.Errorf("hypershift destroy cluster %s failed for %s (stderr identity %x): %w", platform.Name(), entry.Name, stderr.digest.Sum(nil), err),
			terminal: stderr.terminal,
		}
	}

	log.Printf("Finished destroying cluster: %s", entry.Name)
	return nil
}

type destroyErrorOutput struct {
	digest   hash.Hash
	tail     string
	terminal bool
}

func (output *destroyErrorOutput) Write(data []byte) (int, error) {
	_, _ = output.digest.Write(data)
	text := output.tail + string(data)
	output.terminal = output.terminal || strings.Contains(text, "required inputs are missing") || strings.Contains(text, "NoCredentialProviders")
	const retainedBytes = 64
	if len(text) > retainedBytes {
		text = text[len(text)-retainedBytes:]
	}
	output.tail = text
	return len(data), nil
}
