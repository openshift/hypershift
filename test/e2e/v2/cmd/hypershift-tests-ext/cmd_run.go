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

// runConfig holds validated flags for the "run" subcommand, which executes
// a platform's full test plan by spawning OTE run-suite subprocesses.
type runConfig struct {
	Platform         string
	ArtifactDir      string
	SharedDir        string
	ClusterNamespace string
	ReleaseImage     string
	MaxConcurrency   int
}

func (rc *runConfig) validate() error {
	if rc.Platform == "" {
		return fmt.Errorf("--platform or HYPERSHIFT_PLATFORM is required")
	}
	if _, ok := platformConfigs[rc.Platform]; !ok {
		return fmt.Errorf("unknown platform %q (known: %s)", rc.Platform, knownPlatforms())
	}
	if rc.ArtifactDir == "" {
		return fmt.Errorf("--artifact-dir or ARTIFACT_DIR is required")
	}
	cfg := platformConfigs[rc.Platform]
	if len(cfg.ClusterFiles) > 0 && rc.SharedDir == "" {
		return fmt.Errorf("--shared-dir or SHARED_DIR is required for platform %q", rc.Platform)
	}
	return nil
}

type suiteResult struct {
	suite string
	err   error
}

func newRunCommand() *cobra.Command {
	rc := &runConfig{}

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run the full test matrix for a platform",
		Long:  "Executes parallel and sequential suites as defined by the platform's test plan.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := rc.validate(); err != nil {
				return err
			}
			return runPlatform(rc)
		},
	}

	cmd.Flags().StringVar(&rc.Platform, "platform", os.Getenv("HYPERSHIFT_PLATFORM"), "Platform to run (e.g. azure, test). Defaults to HYPERSHIFT_PLATFORM env.")
	cmd.Flags().StringVar(&rc.ArtifactDir, "artifact-dir", os.Getenv("ARTIFACT_DIR"), "Directory for JUnit output. Defaults to ARTIFACT_DIR env.")
	cmd.Flags().StringVar(&rc.SharedDir, "shared-dir", os.Getenv("SHARED_DIR"), "Directory containing cluster name files. Defaults to SHARED_DIR env.")
	cmd.Flags().StringVar(&rc.ClusterNamespace, "cluster-namespace", envOrDefault("E2E_HOSTED_CLUSTER_NAMESPACE", "clusters"), "Namespace for hosted clusters. Defaults to E2E_HOSTED_CLUSTER_NAMESPACE or 'clusters'.")
	cmd.Flags().StringVar(&rc.ReleaseImage, "release-image", os.Getenv("RELEASE_IMAGE_LATEST"), "Release image for upgrade tests. Defaults to RELEASE_IMAGE_LATEST env.")
	cmd.Flags().IntVar(&rc.MaxConcurrency, "max-concurrency", 10, "Max concurrent tests per suite")
	return cmd
}

// runPlatform executes the platform's test plan: parallel suites run
// concurrently, sequential chains run in order (aborting the chain on
// first failure). Each suite is a subprocess invocation of "run-suite"
// with platform and per-suite environment variables injected.
func runPlatform(rc *runConfig) error {
	cfg := platformConfigs[rc.Platform]

	if err := os.MkdirAll(rc.ArtifactDir, 0o755); err != nil {
		return fmt.Errorf("creating artifact directory %s: %w", rc.ArtifactDir, err)
	}

	var platformEnv []string
	var perSuiteEnv map[string][]string
	if cfg.EnvFunc != nil {
		platformEnv, perSuiteEnv = cfg.EnvFunc(envInput{
			SharedDir:    rc.SharedDir,
			ReleaseImage: rc.ReleaseImage,
		})
	}
	envForSuite := func(suite string) []string {
		env := append([]string{}, platformEnv...)
		if clusterFile, ok := cfg.ClusterFiles[suite]; ok {
			clusterName := readClusterName(rc.SharedDir, clusterFile)
			env = append(env,
				"E2E_HOSTED_CLUSTER_NAME="+clusterName,
				"E2E_HOSTED_CLUSTER_NAMESPACE="+rc.ClusterNamespace,
			)
		}
		env = append(env, perSuiteEnv[suite]...)
		return env
	}

	var (
		mu      sync.Mutex
		results []suiteResult
		wg      sync.WaitGroup
	)

	for _, suite := range cfg.TestPlan.Parallel {
		suite := suite
		extraEnv := envForSuite(suite)
		wg.Add(1)
		go func() {
			defer wg.Done()
			log.Printf("Running parallel suite %s...", suite)
			err := runSuite(suite, junitPath(rc.ArtifactDir, suite), rc.MaxConcurrency, extraEnv)
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
				extraEnv := envForSuite(suite)
				log.Printf("Running sequential suite %s...", suite)
				err := runSuite(suite, junitPath(rc.ArtifactDir, suite), rc.MaxConcurrency, extraEnv)
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
