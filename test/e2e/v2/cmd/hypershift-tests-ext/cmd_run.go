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

type suiteResult struct {
	suite string
	err   error
}

func newRunCommand() *cobra.Command {
	var platform, artifactDir, sharedDir, clusterNamespace string
	var maxConcurrency int

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run the full test matrix for a platform",
		Long:  "Executes parallel and sequential suites as defined by the platform's test plan.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if platform == "" {
				return fmt.Errorf("--platform or HYPERSHIFT_PLATFORM is required")
			}
			if artifactDir == "" {
				return fmt.Errorf("--artifact-dir or ARTIFACT_DIR is required")
			}
			return runPlatform(platform, artifactDir, sharedDir, clusterNamespace, maxConcurrency)
		},
	}

	cmd.Flags().StringVar(&platform, "platform", os.Getenv("HYPERSHIFT_PLATFORM"), "Platform to run (e.g. azure, test). Defaults to HYPERSHIFT_PLATFORM env.")
	cmd.Flags().StringVar(&artifactDir, "artifact-dir", os.Getenv("ARTIFACT_DIR"), "Directory for JUnit output. Defaults to ARTIFACT_DIR env.")
	cmd.Flags().StringVar(&sharedDir, "shared-dir", os.Getenv("SHARED_DIR"), "Directory containing cluster name files. Defaults to SHARED_DIR env.")
	cmd.Flags().StringVar(&clusterNamespace, "cluster-namespace", envOrDefault("E2E_HOSTED_CLUSTER_NAMESPACE", "clusters"), "Namespace for hosted clusters. Defaults to E2E_HOSTED_CLUSTER_NAMESPACE or 'clusters'.")
	cmd.Flags().IntVar(&maxConcurrency, "max-concurrency", 10, "Max concurrent tests per suite")
	return cmd
}

func runPlatform(platform, artifactDir, sharedDir, clusterNamespace string, maxConcurrency int) error {
	cfg, ok := platformConfigs[platform]
	if !ok {
		return fmt.Errorf("unknown platform %q (known: %s)", platform, knownPlatforms())
	}

	if len(cfg.ClusterFiles) > 0 && sharedDir == "" {
		return fmt.Errorf("--shared-dir or SHARED_DIR is required for platform %q", platform)
	}

	os.MkdirAll(artifactDir, 0o755)

	var (
		mu      sync.Mutex
		results []suiteResult
		wg      sync.WaitGroup
	)

	for _, suite := range cfg.TestPlan.Parallel {
		suite := suite
		extraEnv := clusterEnvForSuite(cfg, suite, sharedDir, clusterNamespace)
		wg.Add(1)
		go func() {
			defer wg.Done()
			log.Printf("Running parallel suite %s...", suite)
			err := runSuite(suite, junitPath(artifactDir, suite), maxConcurrency, extraEnv)
			mu.Lock()
			results = append(results, suiteResult{suite: suite, err: err})
			mu.Unlock()
			if err != nil {
				log.Printf("Suite %s FAILED: %v", suite, err)
			} else {
				log.Printf("Suite %s PASSED", suite)
			}
		}()
	}

	for _, chain := range cfg.TestPlan.Sequential {
		chain := chain
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i, suite := range chain {
				extraEnv := clusterEnvForSuite(cfg, suite, sharedDir, clusterNamespace)
				log.Printf("Running sequential suite %s...", suite)
				err := runSuite(suite, junitPath(artifactDir, suite), maxConcurrency, extraEnv)
				mu.Lock()
				results = append(results, suiteResult{suite: suite, err: err})
				mu.Unlock()
				if err != nil {
					log.Printf("Suite %s FAILED: %v — skipping remaining suites in chain", suite, err)
					return
				}
				log.Printf("Suite %s PASSED", suite)
				if i < len(chain)-1 {
					log.Printf("Continuing to next suite in chain...")
				}
			}
		}()
	}

	log.Println("Waiting for all suites to complete...")
	wg.Wait()

	failed := 0
	for _, r := range results {
		if r.err != nil {
			log.Printf("FAIL: %s — %v", r.suite, r.err)
			failed++
		} else {
			log.Printf("PASS: %s", r.suite)
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d suite(s) failed", failed)
	}
	log.Println("All suites passed")
	return nil
}

func clusterEnvForSuite(cfg platformConfig, suite, sharedDir, clusterNamespace string) []string {
	clusterFile, ok := cfg.ClusterFiles[suite]
	if !ok {
		return nil
	}
	clusterName := readClusterName(sharedDir, clusterFile)
	return []string{
		"E2E_HOSTED_CLUSTER_NAME=" + clusterName,
		"E2E_HOSTED_CLUSTER_NAMESPACE=" + clusterNamespace,
	}
}

func readClusterName(sharedDir, filename string) string {
	path := filepath.Join(sharedDir, filename)
	data, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("Failed to read cluster name from %s: %v", path, err)
	}
	name := strings.TrimSpace(string(data))
	if name == "" {
		log.Fatalf("Cluster name file %s is empty", path)
	}
	return name
}

func runSuite(suite, junitPath string, maxConcurrency int, extraEnv []string) error {
	args := []string{
		"run-suite", suite,
		"--output", "jsonl",
		fmt.Sprintf("--max-concurrency=%d", maxConcurrency),
	}
	if junitPath != "" {
		args = append(args, "--junit-path", junitPath)
	}

	cmd := exec.Command(os.Args[0], args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), extraEnv...)
	return cmd.Run()
}

func junitPath(artifactDir, suite string) string {
	if artifactDir == "" {
		return ""
	}
	name := strings.ReplaceAll(suite, "/", "_")
	return filepath.Join(artifactDir, fmt.Sprintf("junit_%s.xml", name))
}

func knownPlatforms() string {
	names := make([]string, 0, len(platformConfigs))
	for name := range platformConfigs {
		names = append(names, name)
	}
	return strings.Join(names, ", ")
}

func envOrDefault(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}
