//go:build e2ev2

// run-tests dispatches the v2 e2e test suites in parallel, one per
// pre-created hosted cluster. Test groups and label filters are
// determined by the platform (HYPERSHIFT_PLATFORM env var).
package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/openshift/hypershift/test/e2e/v2/lifecycle"
)

const (
	defaultVerbose       = "false"
	defaultGinkgoTimeout = "3h"
)

// testResult captures the outcome of a single test group execution.
type testResult struct {
	name string
	err  error
}

func main() {
	log.SetFlags(log.LstdFlags)

	sharedDir := requireEnv("SHARED_DIR")
	artifactDir := requireEnv("ARTIFACT_DIR")
	releaseImage := os.Getenv("RELEASE_IMAGE_LATEST")

	testBinary := "bin/test-e2e-v2"
	if binDir := os.Getenv("E2EV2_BIN_DIR"); binDir != "" {
		testBinary = filepath.Join(binDir, "test-e2e-v2")
	}

	eventuallyVerbose := os.Getenv("EVENTUALLY_VERBOSE")
	if eventuallyVerbose == "" {
		eventuallyVerbose = defaultVerbose
	}
	os.Setenv("EVENTUALLY_VERBOSE", eventuallyVerbose)

	platform, err := lifecycle.NewPlatformConfig(os.Getenv("HYPERSHIFT_PLATFORM"), sharedDir)
	if err != nil {
		log.Fatalf("Failed to initialize platform config: %v", err)
	}

	// Let the platform set up any env vars it needs for tests.
	platform.SetupTestEnv(sharedDir)

	variants := os.Getenv("HYPERSHIFT_VARIANTS")
	specs := lifecycle.FilterClusterSpecs(platform.ClusterSpecs(releaseImage, os.Getenv("OCP_IMAGE_N1")), variants)
	matrix := lifecycle.FilterTestMatrix(platform.TestMatrix(releaseImage), specs)

	// Allow overriding the label filter for all test groups.
	if override := os.Getenv("GINKGO_LABEL_FILTER"); override != "" {
		log.Printf("Overriding label filters with GINKGO_LABEL_FILTER=%s", override)
		for i := range matrix.Parallel {
			matrix.Parallel[i].LabelFilter = override
		}
		for i := range matrix.Sequential {
			for j := range matrix.Sequential[i].Steps {
				matrix.Sequential[i].Steps[j].LabelFilter = override
			}
		}
	}

	var (
		mu      sync.Mutex
		results []testResult
		wg      sync.WaitGroup
	)

	// Launch parallel test groups.
	for _, g := range matrix.Parallel {
		g := g
		wg.Add(1)
		go func() {
			defer wg.Done()
			clusterName := readClusterName(sharedDir, g.ClusterFile)
			log.Printf("Running %s tests against %s...", g.Name, clusterName)
			err := runTestBinary(testBinary, clusterName, g.LabelFilter, g.Skip,
				filepath.Join(artifactDir, g.JUnitFile), g.ExtraEnv)
			mu.Lock()
			results = append(results, testResult{name: g.Name, err: err})
			mu.Unlock()
			if err != nil {
				log.Printf("%s tests FAILED: %v", g.Name, err)
			} else {
				log.Printf("%s tests PASSED", g.Name)
			}
		}()
	}

	// Launch sequential groups (each group runs in its own goroutine,
	// but steps within a group run one after another).
	for _, sg := range matrix.Sequential {
		sg := sg
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i, step := range sg.Steps {
				clusterName := readClusterName(sharedDir, step.ClusterFile)
				log.Printf("Running %s tests against %s...", step.Name, clusterName)
				err := runTestBinary(testBinary, clusterName, step.LabelFilter, step.Skip,
					filepath.Join(artifactDir, step.JUnitFile), step.ExtraEnv)
				mu.Lock()
				results = append(results, testResult{name: step.Name, err: err})
				mu.Unlock()
				if err != nil {
					log.Printf("%s tests FAILED: %v — skipping remaining steps in %s", step.Name, err, sg.Name)
					return
				}
				log.Printf("%s tests PASSED", step.Name)
				if i < len(sg.Steps)-1 {
					log.Printf("Continuing to next step in %s...", sg.Name)
				}
			}
		}()
	}

	log.Println("Waiting for all test suites to complete...")
	wg.Wait()

	// Summarize and exit.
	failed := 0
	for _, r := range results {
		if r.err != nil {
			log.Printf("FAIL: %s — %v", r.name, r.err)
			failed++
		} else {
			log.Printf("PASS: %s", r.name)
		}
	}
	if failed > 0 {
		log.Fatalf("%d test group(s) failed", failed)
	}
	log.Println("All test groups passed")
}

func runTestBinary(testBinary, clusterName, labelFilter, skip, junitPath string, extraEnv []string) error {
	ginkgoTimeout := os.Getenv("GINKGO_TIMEOUT")
	if ginkgoTimeout == "" {
		ginkgoTimeout = defaultGinkgoTimeout
	}

	args := []string{
		fmt.Sprintf("--ginkgo.label-filter=%s", labelFilter),
		fmt.Sprintf("--ginkgo.junit-report=%s", junitPath),
		fmt.Sprintf("--ginkgo.timeout=%s", ginkgoTimeout),
		"--ginkgo.v",
	}
	if skip != "" {
		args = append(args, fmt.Sprintf("--ginkgo.skip=%s", skip))
	}

	cmd := exec.Command(testBinary, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	clusterNS := os.Getenv("HYPERSHIFT_NAMESPACE")
	if clusterNS == "" {
		clusterNS = lifecycle.DefaultNamespace
	}
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("E2E_HOSTED_CLUSTER_NAME=%s", clusterName),
		fmt.Sprintf("E2E_HOSTED_CLUSTER_NAMESPACE=%s", clusterNS),
	)
	cmd.Env = append(cmd.Env, extraEnv...)

	return cmd.Run()
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

func requireEnv(key string) string {
	val := os.Getenv(key)
	if val == "" {
		log.Fatalf("Required environment variable %s is not set", key)
	}
	return val
}
