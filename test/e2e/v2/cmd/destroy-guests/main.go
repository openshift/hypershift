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
// lifecycle tests. Cluster names are re-derived from PROW_JOB_ID
// using the same sha256 hash logic as the create step. All clusters
// are destroyed in parallel with best-effort semantics.
// Platform selection is controlled by the HYPERSHIFT_PLATFORM
// environment variable (default: "azure").
package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/openshift/hypershift/test/e2e/v2/lifecycle"
)

const clusterGracePeriod = "40m"

func main() {
	prowJobID := os.Getenv("PROW_JOB_ID")
	if prowJobID == "" {
		log.Fatal("PROW_JOB_ID is required")
	}

	sharedDir := os.Getenv("SHARED_DIR")

	platform, err := lifecycle.NewPlatformConfig(os.Getenv("HYPERSHIFT_PLATFORM"), sharedDir)
	if err != nil {
		log.Fatalf("Failed to initialize platform config: %v", err)
	}

	hypershiftBin := os.Getenv("HYPERSHIFT_BINARY")
	if hypershiftBin == "" {
		hypershiftBin = "hypershift"
	}

	specs := platform.ClusterSpecs("", "")
	if v := strings.TrimSpace(os.Getenv("HYPERSHIFT_CLUSTER_VARIANTS")); v != "" {
		specs = lifecycle.FilterSpecs(specs, lifecycle.VariantEquals(v))
	}

	log.Printf("Destroying %d clusters derived from PROW_JOB_ID=%s", len(specs), prowJobID)

	var (
		mu     sync.Mutex
		failed bool
		wg     sync.WaitGroup
	)

	for _, spec := range specs {
		clusterName := lifecycle.DeriveClusterName(prowJobID, spec.Variant)
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := destroyCluster(hypershiftBin, clusterName, spec.Variant, platform); err != nil {
				log.Printf("WARNING: Failed to destroy cluster %s (%s): %v", clusterName, spec.Variant, err)
				log.Printf("ACTION REQUIRED: cloud resources for cluster %s may be orphaned and need manual cleanup (resource group, DNS records, etc.)", clusterName)
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

func destroyCluster(hypershiftBin, name, variant string, platform lifecycle.PlatformConfig) error {
	log.Printf("Destroying cluster %s (%s)", name, variant)

	args := []string{
		"destroy", "cluster", platform.Name(),
		"--name=" + name,
		"--cluster-grace-period=" + clusterGracePeriod,
	}
	args = append(args, platform.DestroyArgs()...)

	log.Printf("Running: %s %v", hypershiftBin, args)

	cmd := exec.Command(hypershiftBin, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("hypershift destroy cluster %s failed for %s: %w", platform.Name(), name, err)
	}

	log.Printf("Finished destroying cluster: %s", name)
	return nil
}
