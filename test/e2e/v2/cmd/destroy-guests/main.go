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
// destroy flags come from PlatformConfig.DestroyArgs().
// Clusters are destroyed with the developer `hypershift` CLI by default, or
// the product `hcp` CLI when HCP_BINARY is set.
// All clusters are destroyed in parallel with best-effort semantics.
package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"slices"
	"sync"

	"github.com/openshift/hypershift/test/e2e/v2/lifecycle"
)

const clusterGracePeriod = "40m"

func main() {
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

	cli := lifecycle.LifecycleCLI()

	// Destroy args are identical for every cluster, so build them once and
	// fail before any destroy is attempted if the CLI cannot drive the
	// platform.
	args, err := destroyArgs(cli, platform)
	if err != nil {
		log.Fatalf("Failed to build destroy args: %v", err)
	}

	log.Printf("Destroying %d clusters from manifest using %s", len(manifest.Clusters), cli)

	var (
		mu     sync.Mutex
		failed bool
		wg     sync.WaitGroup
	)

	for _, entry := range manifest.Clusters {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := destroyCluster(cli, entry, platform, args); err != nil {
				log.Printf("WARNING: Failed to destroy cluster %s (%s): %v", entry.Name, entry.Variant, err)
				log.Printf("ACTION REQUIRED: cloud resources for cluster %s (infraID=%s) may be orphaned and need manual cleanup", entry.Name, entry.InfraID)
				mu.Lock()
				failed = true
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	if failed {
		log.Fatal("One or more clusters failed to destroy")
	}
	log.Printf("All clusters destroyed successfully")
}

// destroyArgs returns the platform-specific args shared by every destroy
// invocation, spelled for cli. The per-cluster identity flags are appended by
// destroyCluster. It returns an error when the platform cannot be driven by
// cli or when any resulting flag is one the CLI does not accept.
func destroyArgs(cli lifecycle.CLI, platform lifecycle.PlatformConfig) ([]string, error) {
	platformArgs, err := platform.DestroyArgs(cli)
	if err != nil {
		return nil, err
	}

	args := append([]string{
		"destroy", "cluster", platform.Name(),
		"--cluster-grace-period=" + clusterGracePeriod,
	}, platformArgs...)

	if err := lifecycle.ValidateArgs(cli, args); err != nil {
		return nil, err
	}
	return args, nil
}

func destroyCluster(cli lifecycle.CLI, entry lifecycle.ClusterEntry, platform lifecycle.PlatformConfig, sharedArgs []string) error {
	log.Printf("Destroying cluster %s (%s, infraID=%s)", entry.Name, entry.Variant, entry.InfraID)

	args := append(slices.Clone(sharedArgs),
		"--name="+entry.Name,
		"--namespace="+entry.Namespace,
		"--infra-id="+entry.InfraID,
	)

	log.Printf("Running: %s %v", cli.Binary, args)

	cmd := exec.Command(cli.Binary, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s destroy cluster %s failed for %s: %w", cli.Kind, platform.Name(), entry.Name, err)
	}

	log.Printf("Finished destroying cluster: %s", entry.Name)
	return nil
}
