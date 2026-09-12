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
// HostedClusters in parallel. Cluster identities are read from
// the cluster manifest written by create-guests to SHARED_DIR.
//
// After manifest-based dumps complete, the binary also discovers
// any HostedClusters in the management cluster that were NOT in
// the manifest (e.g. clusters created dynamically by the upgrade
// test) and dumps those as well.
//
// It always exits 0 so that dump failures never block teardown.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/test/e2e/v2/lifecycle"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func main() {
	hypershiftBinary := flag.String("hypershift-binary", "hypershift", "Path to the hypershift CLI binary")
	flag.Parse()

	sharedDir := os.Getenv("SHARED_DIR")
	if sharedDir == "" {
		log.Fatal("SHARED_DIR environment variable is required")
	}
	artifactDir := os.Getenv("ARTIFACT_DIR")
	if artifactDir == "" {
		log.Fatal("ARTIFACT_DIR environment variable is required")
	}

	manifest, err := lifecycle.ReadManifest(sharedDir)
	if err != nil {
		log.Fatalf("Failed to read cluster manifest: %v", err)
	}

	log.Printf("Dumping %d clusters from manifest", len(manifest.Clusters))

	// Build a set of cluster names from the manifest so we can skip
	// them during the discovery phase below.
	manifestClusterNames := make(map[string]struct{}, len(manifest.Clusters))

	var wg sync.WaitGroup
	for _, entry := range manifest.Clusters {
		manifestClusterNames[entry.Name] = struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			dumpCluster(*hypershiftBinary, artifactDir, entry.Name, entry.Namespace)
		}()
	}
	wg.Wait()

	log.Println("Manifest cluster dumps complete")

	// Discover and dump any HostedClusters that aren't in the manifest.
	// Some tests (e.g. the upgrade test) create their own HostedClusters
	// dynamically, and those won't appear in the manifest written by
	// create-guests. We don't want to lose their HCP namespace artifacts.
	//
	// Errors here are non-fatal: we log a warning and move on so that
	// dump failures never block teardown.
	discoverAndDumpUnmanifestedClusters(*hypershiftBinary, artifactDir, manifestClusterNames)

	log.Println("All cluster dumps complete")
}

// discoverAndDumpUnmanifestedClusters lists all HostedClusters in the
// management cluster and dumps any that were not already covered by the
// manifest. It uses the management-cluster kubeconfig (KUBECONFIG env
// var) to build a controller-runtime client.
func discoverAndDumpUnmanifestedClusters(hypershiftBinary, artifactDir string, alreadyDumped map[string]struct{}) {
	log.Println("Discovering HostedClusters not in the manifest...")

	c, err := util.GetClient()
	if err != nil {
		log.Printf("WARNING: Failed to create management-cluster client for HostedCluster discovery: %v", err)
		return
	}

	hcList := &hyperv1.HostedClusterList{}
	if err := c.List(context.TODO(), hcList, &crclient.ListOptions{}); err != nil {
		log.Printf("WARNING: Failed to list HostedClusters for discovery: %v", err)
		return
	}

	// Identify clusters that were not in the manifest.
	var extra []hyperv1.HostedCluster
	for i := range hcList.Items {
		hc := &hcList.Items[i]
		if _, found := alreadyDumped[hc.Name]; !found {
			extra = append(extra, *hc)
		}
	}

	if len(extra) == 0 {
		log.Println("No additional HostedClusters discovered outside the manifest")
		return
	}

	log.Printf("Discovered %d HostedCluster(s) not in the manifest — dumping them now", len(extra))

	var wg sync.WaitGroup
	for _, hc := range extra {
		log.Printf("  -> %s/%s (not in manifest)", hc.Namespace, hc.Name)
		wg.Add(1)
		go func() {
			defer wg.Done()
			dumpCluster(hypershiftBinary, artifactDir, hc.Name, hc.Namespace)
		}()
	}
	wg.Wait()

	log.Printf("Finished dumping %d discovered cluster(s)", len(extra))
}

func dumpCluster(hypershiftBinary, artifactDir, clusterName, namespace string) {
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
		"--namespace=" + namespace,
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
