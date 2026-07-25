//go:build e2ev2

package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/openshift/hypershift/test/e2e/v2/lifecycle"
	"github.com/spf13/cobra"
)

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
		Long: `Discovers hosted clusters from SHARED_DIR/clusters.json and runs
all test suites sequentially against each cluster. Clusters are tested in
parallel. Tests self-select against compatible clusters using skip guards.`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()
			return runTests(ctx, sharedDir, artifactDir)
		},
	}

	cmd.Flags().StringVar(&sharedDir, "shared-dir", os.Getenv("SHARED_DIR"), "Directory containing cluster name files")
	cmd.Flags().StringVar(&artifactDir, "artifact-dir", os.Getenv("ARTIFACT_DIR"), "Directory for JUnit output")

	return cmd
}

func runTests(ctx context.Context, sharedDir, artifactDir string) error {
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

	manifestPath := filepath.Join(sharedDir, "clusters.json")
	clusters, err := lifecycle.ReadClusterManifest(manifestPath)
	if err != nil {
		return fmt.Errorf("reading cluster manifest: %w", err)
	}
	if len(clusters) == 0 {
		return fmt.Errorf("no clusters in %s", manifestPath)
	}
	for _, c := range clusters {
		log.Printf("Discovered cluster: %s → %s/%s", c.Variant, c.Namespace, c.Name)
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
				if ctx.Err() != nil {
					mu.Lock()
					results = append(results, runResult{cluster: c.Variant, suite: suite, err: ctx.Err()})
					mu.Unlock()
					return
				}
				suiteSuffix := suite[strings.LastIndex(suite, "/")+1:]
				junitPath := filepath.Join(artifactDir, fmt.Sprintf("junit_%s_%s.xml", c.Variant, suiteSuffix))
				log.Printf("Running %s against %s cluster: %s", suite, c.Variant, c.Name)
				err := runSuite(ctx, suite, c, junitPath)
				mu.Lock()
				results = append(results, runResult{cluster: c.Variant, suite: suite, err: err})
				mu.Unlock()
				if err != nil {
					log.Printf("%s FAILED for %s: %v", suite, c.Variant, err)
				} else {
					log.Printf("%s PASSED for %s", suite, c.Variant)
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

func runSuite(ctx context.Context, suite string, cluster lifecycle.ClusterManifest, junitPath string) error {
	args := []string{"run-suite", suite, "--junit-path=" + junitPath}
	cmd := exec.CommandContext(ctx, os.Args[0], args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Cancel = func() error {
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("E2E_HOSTED_CLUSTER_NAME=%s", cluster.Name),
		fmt.Sprintf("E2E_HOSTED_CLUSTER_NAMESPACE=%s", cluster.Namespace),
	)
	return cmd.Run()
}
