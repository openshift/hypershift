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

// dump-guests collects diagnostic artifacts from all v2 e2e
// HostedClusters in parallel. It shells out to the hypershift CLI
// for each cluster and always exits 0 so that dump failures never
// block teardown.
// Platform selection is controlled by the HYPERSHIFT_PLATFORM
// environment variable (default: "azure").
package main

import (
	"flag"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/openshift/hypershift/test/e2e/v2/lifecycle"
)

func main() {
	hypershiftBinary := flag.String("hypershift-binary", "hypershift", "Path to the hypershift CLI binary")
	flag.Parse()

	prowJobID := os.Getenv("PROW_JOB_ID")
	if prowJobID == "" {
		log.Fatal("PROW_JOB_ID environment variable is required")
	}
	artifactDir := os.Getenv("ARTIFACT_DIR")
	if artifactDir == "" {
		log.Fatal("ARTIFACT_DIR environment variable is required")
	}

	sharedDir := os.Getenv("SHARED_DIR")
	platform, err := lifecycle.NewPlatformConfig(os.Getenv("HYPERSHIFT_PLATFORM"), sharedDir)
	if err != nil {
		log.Fatalf("Failed to initialize platform config: %v", err)
	}

	specs := platform.ClusterSpecs("", "", "")
	log.Printf("Dumping %d clusters derived from PROW_JOB_ID=%s", len(specs), prowJobID)

	var wg sync.WaitGroup
	for _, spec := range specs {
		clusterName := lifecycle.DeriveClusterName(prowJobID, spec.Variant)
		wg.Add(1)
		go func() {
			defer wg.Done()
			dumpCluster(*hypershiftBinary, artifactDir, clusterName)
		}()
	}
	wg.Wait()

	log.Println("All cluster dumps complete")
}

func dumpCluster(hypershiftBinary, artifactDir, clusterName string) {
	dumpDir := filepath.Join(artifactDir, clusterName)
	if err := os.MkdirAll(dumpDir, 0755); err != nil {
		log.Printf("WARNING: Failed to create artifact directory %s: %v", dumpDir, err)
		return
	}

	args := []string{
		"dump", "cluster",
		"--artifact-dir=" + dumpDir,
		"--dump-guest-cluster=true",
		"--name=" + clusterName,
	}

	log.Printf("Dumping cluster %s -> %s", clusterName, dumpDir)
	log.Printf("Running: %s %v", hypershiftBinary, args)

	cmd := exec.Command(hypershiftBinary, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Printf("WARNING: Failed to dump cluster %s: %v", clusterName, err)
		return
	}

	log.Printf("Successfully dumped cluster %s", clusterName)
}
