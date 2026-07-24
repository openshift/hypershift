//go:build e2ev2

package main

import (
	"encoding/json"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var binaryPath string

func TestMain(m *testing.M) {
	root := findRepoRoot()
	binaryPath = filepath.Join(root, "bin", "hypershift-tests-ext")
	if _, err := os.Stat(binaryPath); err != nil {
		log.Fatalf("binary not found at %s — run 'make e2ev2-hypershift-tests-ext' first", binaryPath)
	}
	os.Exit(m.Run())
}

func findRepoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			log.Fatal("no go.mod found")
		}
		dir = parent
	}
}

type listedSpec struct {
	Name      string                     `json:"name"`
	Labels    map[string]json.RawMessage `json:"labels"`
	Lifecycle string                     `json:"lifecycle"`
	Resources struct {
		ResourcePools map[string]int `json:"resourcePools"`
	} `json:"resources"`
}

func listSpecs(t *testing.T, env []string, args ...string) []listedSpec {
	t.Helper()
	fullArgs := append([]string{"list", "tests", "-o", "jsonl"}, args...)
	out := mustRun(t, env, fullArgs...)
	var specs []listedSpec
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		var s listedSpec
		if err := json.Unmarshal([]byte(line), &s); err != nil {
			t.Fatalf("parse spec: %v\nline: %s", err, line)
		}
		specs = append(specs, s)
	}
	return specs
}

func run(t *testing.T, env []string, args ...string) (string, string, error) {
	t.Helper()
	cmd := exec.Command(binaryPath, args...)
	cmd.Env = append(os.Environ(), env...)
	for _, e := range env {
		cmd.Env = append(cmd.Env, e)
	}
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func mustRun(t *testing.T, env []string, args ...string) string {
	t.Helper()
	stdout, stderr, err := run(t, env, args...)
	if err != nil {
		t.Fatalf("%v failed: %v\nstdout:\n%s\nstderr:\n%s", args, err, stdout, stderr)
	}
	return stdout
}

var testEnv = []string{"HYPERSHIFT_PLATFORM=test"}

// createSharedDir creates a temp directory with cluster name files matching
// the test platform's ClusterFiles config. Returns the path.
func createSharedDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"cluster-name-a":   "test-cluster-a",
		"cluster-name-b":   "test-cluster-b",
		"cluster-name-seq": "test-cluster-seq",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// clusterWiringEnv returns the env vars needed for cluster wiring validation.
func clusterWiringEnv(sharedDir, artifactDir string) []string {
	return append(testEnv,
		"SHARED_DIR="+sharedDir,
		"ARTIFACT_DIR="+artifactDir,
		"EXPECTED_CLUSTER_NAME_POOL_A=test-cluster-a",
		"EXPECTED_CLUSTER_NAME_POOL_B=test-cluster-b",
		"EXPECTED_CLUSTER_NAME_SEQ=test-cluster-seq",
	)
}

func TestDiscovery(t *testing.T) {
	specs := listSpecs(t, testEnv)

	expected := []string{
		"Pool A",
		"Pool B",
		"Sequential Step 1",
		"Sequential Step 2",
	}
	for _, want := range expected {
		found := false
		for _, s := range specs {
			if strings.Contains(s.Name, want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no spec containing %q in %d listed specs", want, len(specs))
		}
	}
}

func TestSuiteFiltering(t *testing.T) {
	t.Run("multi-pool parallel includes pool-a and pool-b only", func(t *testing.T) {
		specs := listSpecs(t, testEnv, "--suite", "hypershift/test/parallel")
		for _, s := range specs {
			_, hasA := s.Labels["test-pool-a"]
			_, hasB := s.Labels["test-pool-b"]
			if !hasA && !hasB {
				t.Errorf("spec without pool-a or pool-b label in parallel suite: %s", s.Name)
			}
		}
		if len(specs) != 6 {
			t.Errorf("expected 6 parallel specs (2 pool-a + 4 pool-b), got %d", len(specs))
		}
	})

	t.Run("pool-a suite includes only pool-a specs", func(t *testing.T) {
		specs := listSpecs(t, testEnv, "--suite", "hypershift/test/pool-a")
		for _, s := range specs {
			if _, has := s.Labels["test-pool-a"]; !has {
				t.Errorf("spec without test-pool-a label in pool-a suite: %s", s.Name)
			}
		}
		if len(specs) != 2 {
			t.Errorf("expected 2 pool-a specs, got %d", len(specs))
		}
	})

	t.Run("pool-b suite includes only pool-b specs", func(t *testing.T) {
		specs := listSpecs(t, testEnv, "--suite", "hypershift/test/pool-b")
		for _, s := range specs {
			if _, has := s.Labels["test-pool-b"]; !has {
				t.Errorf("spec without test-pool-b label in pool-b suite: %s", s.Name)
			}
		}
		if len(specs) != 4 {
			t.Errorf("expected 4 pool-b specs, got %d", len(specs))
		}
	})

	t.Run("step-1 includes only step-1 spec", func(t *testing.T) {
		specs := listSpecs(t, testEnv, "--suite", "hypershift/test/step-1")
		if len(specs) != 1 {
			t.Fatalf("expected 1 step-1 spec, got %d", len(specs))
		}
		if !strings.Contains(specs[0].Name, "Step 1") {
			t.Errorf("expected Step 1 spec, got %s", specs[0].Name)
		}
	})

	t.Run("step-2 includes only step-2 spec", func(t *testing.T) {
		specs := listSpecs(t, testEnv, "--suite", "hypershift/test/step-2")
		if len(specs) != 1 {
			t.Fatalf("expected 1 step-2 spec, got %d", len(specs))
		}
		if !strings.Contains(specs[0].Name, "Step 2") {
			t.Errorf("expected Step 2 spec, got %s", specs[0].Name)
		}
	})

	t.Run("suites do not cross-contaminate", func(t *testing.T) {
		parallel := listSpecs(t, testEnv, "--suite", "hypershift/test/parallel")
		for _, s := range parallel {
			if strings.Contains(s.Name, "Sequential") {
				t.Errorf("sequential spec leaked into parallel suite: %s", s.Name)
			}
		}
	})
}

func TestPoolAssignment(t *testing.T) {
	specs := listSpecs(t, testEnv, "--suite", "hypershift/test/parallel")

	poolCounts := make(map[string]int)
	for _, s := range specs {
		for pool := range s.Resources.ResourcePools {
			poolCounts[pool]++
		}
	}

	if poolCounts["pool-a"] != 2 {
		t.Errorf("expected 2 specs in pool-a, got %d", poolCounts["pool-a"])
	}
	if poolCounts["pool-b"] != 4 {
		t.Errorf("expected 4 specs in pool-b, got %d", poolCounts["pool-b"])
	}

	t.Run("informing spec retains pool assignment", func(t *testing.T) {
		for _, s := range specs {
			if s.Lifecycle == "informing" {
				if s.Resources.ResourcePools["pool-b"] != 1 {
					t.Errorf("informing spec should be in pool-b: %s", s.Name)
				}
				return
			}
		}
		t.Error("no informing spec found in parallel suite")
	})

	t.Run("sequential specs get pool-seq", func(t *testing.T) {
		for _, suite := range []string{"hypershift/test/step-1", "hypershift/test/step-2"} {
			specs := listSpecs(t, testEnv, "--suite", suite)
			for _, s := range specs {
				if s.Resources.ResourcePools["pool-seq"] != 1 {
					t.Errorf("spec in %s should have pool-seq: %s", suite, s.Name)
				}
			}
		}
	})
}

func TestParallelExecution(t *testing.T) {
	stdout, stderr, err := run(t, testEnv, "run-suite", "hypershift/test/parallel", "--output", "jsonl")
	if err != nil {
		t.Fatalf("parallel suite should exit 0 (informing failures are non-terminal): %v\nstderr:\n%s", err, stderr)
	}

	t.Run("pool capacity enforcement", func(t *testing.T) {
		for _, pool := range []string{"pool-a", "pool-b"} {
			if !strings.Contains(stderr, pool) {
				t.Errorf("scheduler output should reference %s", pool)
			}
		}
		if !strings.Contains(stderr, "available)") {
			t.Error("expected pool capacity logs from scheduler")
		}
	})

	t.Run("result lifecycle counts", func(t *testing.T) {
		type specResult struct {
			Result    string `json:"result"`
			Lifecycle string `json:"lifecycle"`
		}
		var passed, failed, skipped int
		var hasInforming bool
		for _, line := range strings.Split(stdout, "\n") {
			if !strings.HasPrefix(line, "{") {
				continue
			}
			var r specResult
			if json.Unmarshal([]byte(line), &r) != nil || r.Result == "" {
				continue
			}
			switch r.Result {
			case "passed":
				passed++
			case "failed":
				failed++
				if r.Lifecycle == "informing" {
					hasInforming = true
				}
			case "skipped":
				skipped++
			}
		}
		if passed != 4 {
			t.Errorf("expected 4 passed, got %d", passed)
		}
		if skipped != 1 {
			t.Errorf("expected 1 skipped, got %d", skipped)
		}
		if failed != 1 {
			t.Errorf("expected 1 failed (informing), got %d", failed)
		}
		if !hasInforming {
			t.Error("expected the failure to have informing lifecycle")
		}
	})
}

func TestRunPlatformSuccess(t *testing.T) {
	sharedDir := createSharedDir(t)
	artifactDir := t.TempDir()
	env := clusterWiringEnv(sharedDir, artifactDir)
	_, stderr, err := run(t, env, "run")
	if err != nil {
		t.Fatalf("run should pass: %v\nstderr:\n%s", err, stderr)
	}

	for _, suite := range []string{"hypershift/test/pool-a", "hypershift/test/pool-b", "hypershift/test/step-1", "hypershift/test/step-2"} {
		if !strings.Contains(stderr, "PASS: "+suite) {
			t.Errorf("expected PASS for %s in:\n%s", suite, stderr)
		}
	}

	for _, name := range []string{"hypershift_test_pool-a", "hypershift_test_pool-b", "hypershift_test_step-1", "hypershift_test_step-2"} {
		path := filepath.Join(artifactDir, "junit_"+name+".xml")
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("missing JUnit file: %s", filepath.Base(path))
		}
	}
}

func TestSequentialFailurePropagation(t *testing.T) {
	sharedDir := createSharedDir(t)
	artifactDir := t.TempDir()
	env := append(clusterWiringEnv(sharedDir, artifactDir), "TEST_PLATFORM_STEP1_FAIL=true")
	_, stderr, err := run(t, env, "run")

	if err == nil {
		t.Fatal("expected non-zero exit when step-1 fails")
	}

	if !strings.Contains(stderr, "skipping remaining suites in chain") {
		t.Error("expected 'skipping remaining suites in chain' when step-1 fails")
	}

	if !strings.Contains(stderr, "FAIL: hypershift/test/step-1") {
		t.Error("step-1 failure should appear in summary")
	}

	step2Junit := filepath.Join(artifactDir, "junit_hypershift_test_step-2.xml")
	if _, err := os.Stat(step2Junit); err == nil {
		t.Error("step-2 JUnit should not exist — step-2 should have been skipped")
	}

	if !strings.Contains(stderr, "PASS: hypershift/test/pool-a") {
		t.Error("pool-a suite should still pass independently")
	}
}

func TestClusterEnvWiring(t *testing.T) {
	t.Run("pool-a receives correct cluster name", func(t *testing.T) {
		env := append(testEnv,
			"E2E_HOSTED_CLUSTER_NAME=test-cluster-a",
			"EXPECTED_CLUSTER_NAME_POOL_A=test-cluster-a",
		)
		_, stderr, err := run(t, env, "run-suite", "hypershift/test/pool-a", "--output", "jsonl")
		if err != nil {
			t.Fatalf("pool-a suite should pass: %v\nstderr:\n%s", err, stderr)
		}
	})

	t.Run("pool-b receives correct cluster name", func(t *testing.T) {
		env := append(testEnv,
			"E2E_HOSTED_CLUSTER_NAME=test-cluster-b",
			"EXPECTED_CLUSTER_NAME_POOL_B=test-cluster-b",
		)
		_, stderr, err := run(t, env, "run-suite", "hypershift/test/pool-b", "--output", "jsonl")
		if err != nil {
			t.Fatalf("pool-b suite should pass (informing failure is non-terminal): %v\nstderr:\n%s", err, stderr)
		}
	})

	t.Run("mismatched cluster name causes failure", func(t *testing.T) {
		env := append(testEnv,
			"E2E_HOSTED_CLUSTER_NAME=wrong-cluster",
			"EXPECTED_CLUSTER_NAME_POOL_A=test-cluster-a",
		)
		_, _, err := run(t, env, "run-suite", "hypershift/test/pool-a", "--output", "jsonl")
		if err == nil {
			t.Fatal("expected failure when cluster name doesn't match expected")
		}
	})
}

func TestClusterWiringEndToEnd(t *testing.T) {
	sharedDir := createSharedDir(t)
	artifactDir := t.TempDir()
	env := clusterWiringEnv(sharedDir, artifactDir)
	_, stderr, err := run(t, env, "run")
	if err != nil {
		t.Fatalf("run should pass: %v\nstderr:\n%s", err, stderr)
	}

	for _, suite := range []string{"hypershift/test/pool-a", "hypershift/test/pool-b"} {
		if !strings.Contains(stderr, "PASS: "+suite) {
			t.Errorf("expected PASS for %s", suite)
		}
	}
	for _, suite := range []string{"hypershift/test/step-1", "hypershift/test/step-2"} {
		if !strings.Contains(stderr, "PASS: "+suite) {
			t.Errorf("expected PASS for %s", suite)
		}
	}
}
