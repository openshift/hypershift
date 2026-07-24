//go:build e2ev2

package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/spf13/cobra"
)

const defaultClusterNamespace = "clusters"

var orderedSuites = []string{
	"hypershift/conformance",
	"hypershift/upgrade",
	"hypershift/chaos",
}

type runResult struct {
	cluster string
	suite   string
	err     error
}

func newRunCommand() *cobra.Command {
	var sharedDir, artifactDir string

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Discover clusters and run OTE suites against them",
		Long: `Discovers hosted clusters from SHARED_DIR cluster name files and runs
all test suites sequentially against each cluster. Clusters are tested in
parallel. Tests self-select against compatible clusters using skip guards.`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTests(sharedDir, artifactDir)
		},
	}

	cmd.Flags().StringVar(&sharedDir, "shared-dir", os.Getenv("SHARED_DIR"), "Directory containing cluster name files")
	cmd.Flags().StringVar(&artifactDir, "artifact-dir", os.Getenv("ARTIFACT_DIR"), "Directory for JUnit output")

	return cmd
}

func runTests(sharedDir, artifactDir string) error {
	if sharedDir == "" {
		return fmt.Errorf("--shared-dir is required")
	}
	if artifactDir == "" {
		return fmt.Errorf("--artifact-dir is required")
	}

	kubeconfig := filepath.Join(sharedDir, "management_cluster_kubeconfig")
	if _, err := os.Stat(kubeconfig); err == nil {
		os.Setenv("KUBECONFIG", kubeconfig)
	}

	clusters, err := discoverClusters(sharedDir)
	if err != nil {
		return fmt.Errorf("discovering clusters: %w", err)
	}
	if len(clusters) == 0 {
		return fmt.Errorf("no cluster-name-* files found in %s", sharedDir)
	}

	var (
		mu      sync.Mutex
		results []runResult
		wg      sync.WaitGroup
	)

	for _, c := range clusters {
		c := c
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, suite := range orderedSuites {
				suiteSuffix := suite[strings.LastIndex(suite, "/")+1:]
				junitPath := filepath.Join(artifactDir, fmt.Sprintf("junit_%s_%s.xml", c.variant, suiteSuffix))
				log.Printf("Running %s against %s cluster: %s", suite, c.variant, c.name)
				err := runSuite(suite, c.name, junitPath)
				mu.Lock()
				results = append(results, runResult{cluster: c.variant, suite: suite, err: err})
				mu.Unlock()
				if err != nil {
					log.Printf("%s FAILED for %s: %v", suite, c.variant, err)
				} else {
					log.Printf("%s PASSED for %s", suite, c.variant)
				}
			}
		}()
	}

	log.Println("Waiting for all test suites to complete...")
	wg.Wait()

	failed := 0
	for _, r := range results {
		if r.err != nil {
			log.Printf("FAIL: %s @ %s — %v", r.suite, r.cluster, r.err)
			failed++
		} else {
			log.Printf("PASS: %s @ %s", r.suite, r.cluster)
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d suite run(s) failed", failed)
	}
	log.Println("All suite runs passed")
	return nil
}

type clusterInfo struct {
	variant string
	name    string
}

func discoverClusters(sharedDir string) ([]clusterInfo, error) {
	matches, err := filepath.Glob(filepath.Join(sharedDir, "cluster-name-*"))
	if err != nil {
		return nil, err
	}
	var clusters []clusterInfo
	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}
		name := strings.TrimSpace(string(data))
		if name == "" {
			continue
		}
		variant := strings.TrimPrefix(filepath.Base(path), "cluster-name-")
		clusters = append(clusters, clusterInfo{variant: variant, name: name})
		log.Printf("Discovered cluster: %s → %s", variant, name)
	}
	return clusters, nil
}

func runSuite(suite, clusterName, junitPath string) error {
	args := []string{"run-suite", suite, "--junit-path=" + junitPath}
	cmd := exec.Command(os.Args[0], args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("E2E_HOSTED_CLUSTER_NAME=%s", clusterName),
		fmt.Sprintf("E2E_HOSTED_CLUSTER_NAMESPACE=%s", defaultClusterNamespace),
	)
	return cmd.Run()
}
