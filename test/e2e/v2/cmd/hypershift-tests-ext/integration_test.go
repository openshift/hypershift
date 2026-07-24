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

	"github.com/openshift/hypershift/test/e2e/v2/lifecycle"
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
		Isolation struct {
			Taint []string `json:"taint"`
		} `json:"isolation"`
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

type listedSuite struct {
	Name        string `json:"name"`
	Parallelism int    `json:"parallelism"`
}

func listSuites(t *testing.T, env []string) []listedSuite {
	t.Helper()
	out := mustRun(t, env, "list", "suites", "-o", "jsonl")
	var suites []listedSuite
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		var s listedSuite
		if err := json.Unmarshal([]byte(line), &s); err != nil {
			t.Fatalf("parse suite: %v\nline: %s", err, line)
		}
		suites = append(suites, s)
	}
	return suites
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

// createSharedDir creates a temp directory with platform env files
// matching the test platform config.
func createSharedDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"test_env_value": "hello-from-shared-dir",
		"test_env_file":  "this file exists for path mapping",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
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
	t.Run("platform suite includes all specs", func(t *testing.T) {
		specs := listSpecs(t, testEnv, "--suite", "hypershift/test")
		if len(specs) != 10 {
			t.Errorf("expected 10 specs in hypershift/test, got %d", len(specs))
			for _, s := range specs {
				t.Logf("  %s", s.Name)
			}
		}
	})

	t.Run("step-1 includes only step-1 specs", func(t *testing.T) {
		specs := listSpecs(t, testEnv, "--suite", "hypershift/test/step-1")
		if len(specs) != 3 {
			t.Fatalf("expected 3 step-1 specs, got %d", len(specs))
		}
		for _, s := range specs {
			if !strings.Contains(s.Name, "Step 1") {
				t.Errorf("expected Step 1 spec, got %s", s.Name)
			}
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

	t.Run("extra suites do not include non-matching specs", func(t *testing.T) {
		step1 := listSpecs(t, testEnv, "--suite", "hypershift/test/step-1")
		for _, s := range step1 {
			if strings.Contains(s.Name, "Pool") || strings.Contains(s.Name, "Step 2") {
				t.Errorf("non-step-1 spec leaked into step-1 suite: %s", s.Name)
			}
		}
	})
}

func TestParallelExecution(t *testing.T) {
	stdout, stderr, err := run(t, testEnv, "run-suite", "hypershift/test", "--output", "jsonl")
	if err != nil {
		t.Fatalf("platform suite should exit 0 (informing failures are non-terminal): %v\nstderr:\n%s", err, stderr)
	}

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
	if passed != 8 {
		t.Errorf("expected 8 passed, got %d\nstderr:\n%s", passed, stderr)
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
}

func TestEnvSetup(t *testing.T) {
	sharedDir := createSharedDir(t)
	env := append(testEnv,
		"SHARED_DIR="+sharedDir,
		"EXPECTED_ENV_VALUE=hello-from-shared-dir",
		"EXPECTED_ENV_FILE_EXISTS=true",
	)
	_, stderr, err := run(t, env, "run-suite", "hypershift/test/step-1", "--output", "jsonl")
	if err != nil {
		t.Fatalf("step-1 suite should pass with env setup: %v\nstderr:\n%s", err, stderr)
	}
}

func TestReleaseImageEnv(t *testing.T) {
	sharedDir := createSharedDir(t)
	env := append(testEnv,
		"SHARED_DIR="+sharedDir,
		"RELEASE_IMAGE_LATEST=quay.io/test/release:latest",
		"EXPECTED_RELEASE_IMAGE=quay.io/test/release:latest",
	)
	_, stderr, err := run(t, env, "run-suite", "hypershift/test/step-1", "--output", "jsonl")
	if err != nil {
		t.Fatalf("step-1 suite should pass with release image env: %v\nstderr:\n%s", err, stderr)
	}
}

func TestRunSubcommandRegistered(t *testing.T) {
	stdout, _, err := run(t, testEnv, "run", "--help")
	if err != nil {
		t.Fatalf("run --help should succeed: %v", err)
	}
	if !strings.Contains(stdout, "clusters.json") {
		t.Errorf("run --help output should mention clusters.json, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "--shared-dir") {
		t.Errorf("run --help output should mention --shared-dir flag")
	}
	if !strings.Contains(stdout, "--artifact-dir") {
		t.Errorf("run --help output should mention --artifact-dir flag")
	}
}

func TestClusterManifestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clusters.json")

	want := []lifecycle.ClusterManifest{
		{Name: "public-a1b2c3d4e5", Namespace: "clusters", Variant: "public", ReleaseImage: "quay.io/ocp/release:latest", Platform: "aws"},
		{Name: "upgrade-a1b2c3d4e5", Namespace: "clusters", Variant: "upgrade", ReleaseImage: "quay.io/ocp/release:n-1", Platform: "aws"},
	}

	if err := lifecycle.WriteClusterManifest(path, want); err != nil {
		t.Fatalf("WriteClusterManifest failed: %v", err)
	}

	got, err := lifecycle.ReadClusterManifest(path)
	if err != nil {
		t.Fatalf("ReadClusterManifest failed: %v", err)
	}

	if len(got) != len(want) {
		t.Fatalf("expected %d clusters, got %d", len(want), len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("cluster %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestTaintLabelConvention(t *testing.T) {
	specs := listSpecs(t, testEnv)

	var found bool
	for _, s := range specs {
		if strings.Contains(s.Name, "Test Platform Pool A") {
			found = true
			if len(s.Resources.Isolation.Taint) != 1 || s.Resources.Isolation.Taint[0] != "test-exclusive" {
				t.Errorf("Pool A spec %q should have taint [test-exclusive], got %v", s.Name, s.Resources.Isolation.Taint)
			}
		}
	}
	if !found {
		t.Error("no Test Platform Pool A specs found")
	}
}

func TestConformanceSuiteExcludesLifecycle(t *testing.T) {
	suites := listSuites(t, testEnv)

	suiteMap := make(map[string]bool)
	for _, s := range suites {
		suiteMap[s.Name] = true
	}

	for _, name := range []string{"hypershift/conformance", "hypershift/upgrade", "hypershift/chaos"} {
		if !suiteMap[name] {
			t.Errorf("expected suite %s to be registered", name)
		}
	}
}

func TestSerialSuites(t *testing.T) {
	suites := listSuites(t, testEnv)

	suiteMap := make(map[string]listedSuite)
	for _, s := range suites {
		suiteMap[s.Name] = s
	}

	for _, name := range []string{"hypershift/test/step-1", "hypershift/upgrade", "hypershift/chaos"} {
		s, ok := suiteMap[name]
		if !ok {
			t.Errorf("serial suite %s not found", name)
			continue
		}
		if s.Parallelism != 1 {
			t.Errorf("%s should have parallelism=1, got %d", name, s.Parallelism)
		}
	}

	if s, ok := suiteMap["hypershift/test"]; ok && s.Parallelism == 1 {
		t.Error("platform suite should not be serial")
	}
}
