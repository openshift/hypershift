//go:build e2ev2

// run-tests dispatches the v2 e2e test suites in parallel, one per
// pre-created hosted cluster. Test groups and label filters are
// determined by the platform (HYPERSHIFT_PLATFORM env var).
package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	controlplaneoperatoroverrides "github.com/openshift/hypershift/hypershift-operator/controlplaneoperator-overrides"
	"github.com/openshift/hypershift/test/e2e/v2/lifecycle"
	v2util "github.com/openshift/hypershift/test/e2e/v2/util"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/onsi/ginkgo/v2/reporters"
	"github.com/onsi/ginkgo/v2/types"
)

const (
	defaultTestBinary     = "bin/test-e2e-v2"
	defaultVerbose        = "false"
	defaultGinkgoTimeout  = "3h"
	defaultInterruptGrace = 35 * time.Minute
	defaultInterruptWait  = 3 * time.Hour
)

// testResult captures the outcome of a single test group execution.
type testResult struct {
	name string
	err  error
}

func resolveTestBinary() string {
	if binDir := os.Getenv("E2EV2_BIN_DIR"); binDir != "" {
		return filepath.Join(binDir, "test-e2e-v2")
	}
	return defaultTestBinary
}

func main() {
	log.SetFlags(log.LstdFlags)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	testBinary := resolveTestBinary()

	sharedDir := requireEnv("SHARED_DIR")
	artifactDir := requireEnv("ARTIFACT_DIR")

	eventuallyVerbose := os.Getenv("EVENTUALLY_VERBOSE")
	if eventuallyVerbose == "" {
		eventuallyVerbose = defaultVerbose
	}
	os.Setenv("EVENTUALLY_VERBOSE", eventuallyVerbose)

	setupTestEnvironment()

	manifest, err := lifecycle.ReadManifest(sharedDir)
	if err != nil {
		log.Fatalf("Failed to read cluster manifest: %v", err)
	}
	if err := setupManifestEnvironment(manifest); err != nil {
		log.Fatalf("Failed to restore release image inputs from manifest: %v", err)
	}

	platform, err := lifecycle.NewPlatformConfig(os.Getenv("HYPERSHIFT_PLATFORM"), sharedDir)
	if err != nil {
		log.Fatalf("Failed to initialize platform config: %v", err)
	}

	// Let the platform set up any env vars it needs for tests.
	platform.SetupTestEnv(sharedDir)

	testPlanPath := os.Getenv("TEST_PLAN")
	plan, err := lifecycle.ResolveTestPlan(testPlanPath, platform)
	if err != nil {
		log.Fatalf("Failed to resolve test plan: %v", err)
	}
	log.Printf("Using test plan %q", plan.Name)

	if err := plan.TestMatrix.Validate(); err != nil {
		log.Fatalf("Invalid test plan: %v", err)
	}

	clustersByVariant, err := plan.TestMatrix.ResolveVariants(manifest)
	if err != nil {
		log.Fatalf("Manifest/test matrix mismatch: %v", err)
	}
	var stopObserver func() error
	if client, err := v2util.GetClient(); err != nil {
		log.Printf("WARNING: failed to construct pod timing client: %v", err)
	} else {
		namespaces := make([]string, 0, len(clustersByVariant))
		for _, cluster := range clustersByVariant {
			namespaces = append(namespaces, cluster.Namespace+"-"+cluster.Name)
		}
		stopObserver, err = v2util.StartPodTimingObserver(ctx, client, namespaces, filepath.Join(artifactDir, "e2e-metrics-raw.prometheus"))
		if err != nil {
			log.Printf("WARNING: failed to start suite pod timing diagnostics: %v", err)
		}
	}

	var (
		mu      sync.Mutex
		results []testResult
		wg      sync.WaitGroup
	)

	// Launch parallel test groups.
	for _, g := range plan.TestMatrix.Parallel {
		g := g
		entry := clustersByVariant[g.Variant]
		wg.Add(1)
		go func() {
			defer wg.Done()
			log.Printf("Running %s tests against %s...", g.Name, entry.Name)
			err := runTestBinary(ctx, testBinary, entry.Name, entry.Namespace, g.LabelFilter, g.Skip,
				filepath.Join(artifactDir, g.JUnitFile()), entry.ExpectedNodePools)
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
	for _, sg := range plan.TestMatrix.Sequential {
		sg := sg
		wg.Add(1)
		go func() {
			defer wg.Done()
			phase := diagnosticPhase{}
			for i, step := range sg.Steps {
				entry := clustersByVariant[step.Variant]
				log.Printf("Running %s tests against %s...", step.Name, entry.Name)
				var err error
				if platform.Name() == "aws" {
					err = phase.run(ctx, testBinary, entry, step, filepath.Join(artifactDir, step.JUnitFile()))
				} else {
					err = runTestBinary(ctx, testBinary, entry.Name, entry.Namespace, step.LabelFilter, step.Skip, filepath.Join(artifactDir, step.JUnitFile()), entry.ExpectedNodePools)
				}
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
	if stopObserver != nil {
		if err := stopObserver(); err != nil {
			log.Printf("WARNING: failed to write test metrics: %v", err)
		}
	}
	if platform.Name() == "aws" {
		diagnosticContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		alerts, err := v2util.ManagementSLOAlerts(diagnosticContext)
		cancel()
		if err != nil {
			log.Printf("WARNING: management SLO diagnostics unavailable: %v", err)
		}
		for _, alert := range alerts {
			log.Printf("SLO alert %s fired for %s seconds", alert.Metric["alertname"], alert.Value)
		}
		if err := cleanupRunnerSharedOIDC(ctx, sharedDir, v2util.GetClient, lifecycle.CleanupSharedOIDCIfUnused); err != nil {
			log.Printf("WARNING: shared OIDC cleanup incomplete; destroy-guests can retry: %v", err)
		}
	}

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

func cleanupRunnerSharedOIDC(ctx context.Context, sharedDir string, getClient func() (crclient.Client, error), cleanup func(context.Context, string, crclient.Client) error) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
	defer cancel()
	client, err := getClient()
	if err != nil {
		return err
	}
	return cleanup(cleanupCtx, sharedDir, client)
}

func runTestBinary(ctx context.Context, testBinary, clusterName, namespace, labelFilter, skip, junitPath string, expectedNodePools ...map[string]int32) error {
	return runTestBinaryWithGrace(ctx, testBinary, clusterName, namespace, labelFilter, skip, junitPath, defaultInterruptGrace, defaultInterruptWait, expectedNodePools...)
}

func runTestBinaryWithGrace(ctx context.Context, testBinary, clusterName, namespace, labelFilter, skip, junitPath string, grace, waitDelay time.Duration, expectedNodePools ...map[string]int32) error {
	return runTestBinaryWithDiagnosticRelease(ctx, testBinary, clusterName, namespace, labelFilter, skip, junitPath, "", "", nil, grace, waitDelay, expectedNodePools...)
}

func runTestBinaryWithDiagnosticRelease(ctx context.Context, testBinary, clusterName, namespace, labelFilter, skip, junitPath, diagnosticRelease, diagnosticTarget string, releaseImages map[string]string, grace, waitDelay time.Duration, expectedNodePools ...map[string]int32) error {
	poolEnvironment, err := expectedNodePoolsEnvironment(expectedNodePools...)
	if err != nil {
		return err
	}
	ginkgoTimeout := os.Getenv("GINKGO_TIMEOUT")
	if ginkgoTimeout == "" {
		ginkgoTimeout = defaultGinkgoTimeout
	}

	args := []string{
		fmt.Sprintf("--ginkgo.label-filter=%s", labelFilter),
		fmt.Sprintf("--e2e.junit-report=%s", junitPath),
		fmt.Sprintf("--ginkgo.timeout=%s", ginkgoTimeout),
		fmt.Sprintf("--ginkgo.grace-period=%s", grace),
		"--ginkgo.v",
	}
	if skip != "" {
		args = append(args, fmt.Sprintf("--ginkgo.skip=%s", skip))
	}

	cmd := exec.CommandContext(ctx, testBinary, args...)
	cmd.Cancel = func() error {
		return cmd.Process.Signal(os.Interrupt)
	}
	cmd.WaitDelay = waitDelay
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	cmd.Env = append(os.Environ(),
		fmt.Sprintf("E2E_HOSTED_CLUSTER_NAME=%s", clusterName),
		fmt.Sprintf("E2E_HOSTED_CLUSTER_NAMESPACE=%s", namespace),
		"E2E_CONDITION_DIAGNOSTIC_RELEASE_IMAGE="+diagnosticRelease,
		"E2E_CONDITION_DIAGNOSTIC_UPGRADE_TARGET="+diagnosticTarget,
		"E2E_HEALTH_WORKER_PHASE=",
	)
	if poolEnvironment != "" {
		cmd.Env = append(cmd.Env, poolEnvironment)
	}
	for name, image := range releaseImages {
		cmd.Env = append(cmd.Env, name+"="+image)
	}

	return cmd.Run()
}

type diagnosticFixture struct {
	variant, name, namespace string
}

type diagnosticPhase map[diagnosticFixture]bool

func (phase diagnosticPhase) run(ctx context.Context, testBinary string, entry lifecycle.ClusterEntry, step lifecycle.TestGroup, junitPath string) error {
	filter, err := types.ParseLabelFilter(step.LabelFilter)
	if err != nil {
		return fmt.Errorf("diagnostic phase label filter: %w", err)
	}
	fixture := diagnosticFixture{variant: entry.Variant, name: entry.Name, namespace: entry.Namespace}
	ordinary := entry.Variant == "upgrade" || entry.Variant == "upgrade-pki-disabled"
	diagnosticTarget := ""
	if ordinary || entry.Variant == "karpenter-upgrade" || entry.Variant == "karpenter-upgrade-pki-disabled" {
		diagnosticTarget = os.Getenv("E2E_LATEST_RELEASE_IMAGE")
	}
	diagnosticRelease := ""
	if ordinary && phase[fixture] {
		diagnosticRelease = os.Getenv("E2E_LATEST_RELEASE_IMAGE")
	}
	selected := ordinary && filter([]string{"lifecycle", "control-plane-upgrade"})
	releaseImages := map[string]string{"E2E_HEALTH_WORKER_PHASE": healthWorkerPhase(entry.Variant, step.Name)}
	if entry.Variant == "cpo-override-latest" || entry.Variant == "cpo-override-previous" {
		latest, previous := controlplaneoperatoroverrides.LatestOverrideTestReleases("aws")
		if latest == "" || previous == "" || latest == previous {
			return fmt.Errorf("CPO override child requires distinct production-mapped AWS latest and previous releases")
		}
		releaseImages["E2E_LATEST_RELEASE_IMAGE"] = latest
		releaseImages["E2E_PREVIOUS_RELEASE_IMAGE"] = previous
	}
	previous, previousErr := os.Stat(junitPath)
	if err := runTestBinaryWithDiagnosticRelease(ctx, testBinary, entry.Name, entry.Namespace, step.LabelFilter, step.Skip, junitPath, diagnosticRelease, diagnosticTarget, releaseImages, defaultInterruptGrace, defaultInterruptWait, entry.ExpectedNodePools); err != nil {
		return err
	}
	if !selected || phase[fixture] {
		return nil
	}
	if previousErr != nil && !os.IsNotExist(previousErr) {
		log.Printf("WARNING: CP diagnostic phase provenance unavailable for %s/%s: %v", entry.Namespace, entry.Name, previousErr)
		return nil
	}
	passed, err := controlPlaneDiagnosticPassed(junitPath, previous)
	if err != nil {
		log.Printf("WARNING: CP diagnostic phase not advanced for %s/%s: %v", entry.Namespace, entry.Name, err)
		return nil
	}
	phase[fixture] = passed
	return nil
}

func controlPlaneDiagnosticPassed(path string, previous os.FileInfo) (bool, error) {
	current, err := os.Stat(path)
	if err != nil {
		return false, fmt.Errorf("read CP child JUnit provenance: %w", err)
	}
	if previous != nil && os.SameFile(previous, current) {
		return false, fmt.Errorf("CP child did not replace its prior JUnit report")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	var report reporters.JUnitTestSuites
	if err := xml.Unmarshal(data, &report); err != nil {
		return false, fmt.Errorf("decode CP child JUnit provenance: %w", err)
	}
	const specName = "[sig-hypershift][Jira:Hypershift][Feature:ControlPlaneUpgrade] Control Plane Upgrade should upgrade the control plane from N-1 to latest"
	matches, passed := 0, false
	for _, suite := range report.TestSuites {
		if suite.Name != "hypershift-e2e" {
			continue
		}
		for _, testcase := range suite.TestCases {
			if testcase.Name == specName && testcase.Classname == "hypershift-e2e" {
				matches++
				passed = testcase.Status == types.SpecStatePassed.String() && testcase.Skipped == nil && testcase.Failure == nil && testcase.Error == nil
			}
		}
	}
	if matches != 1 {
		return false, fmt.Errorf("CP child JUnit has %d exact ordinary CP specs, expected one", matches)
	}
	return passed, nil
}

func expectedNodePoolsEnvironment(configured ...map[string]int32) (string, error) {
	if len(configured) == 0 {
		return "", nil
	}
	if err := lifecycle.ValidateExpectedNodePools(configured[0]); err != nil {
		return "", err
	}
	value := ""
	if configured[0] != nil {
		encoded, err := json.Marshal(configured[0])
		if err != nil {
			return "", err
		}
		value = string(encoded)
	}
	return lifecycle.ExpectedNodePoolsEnv + "=" + value, nil
}

func requireEnv(key string) string {
	val := os.Getenv(key)
	if val == "" {
		log.Fatalf("Required environment variable %s is not set", key)
	}
	return val
}

func setupTestEnvironment() {
	for _, mapping := range [][2]string{
		{"E2E_LATEST_RELEASE_IMAGE", "RELEASE_IMAGE_LATEST"},
		{"E2E_PREVIOUS_RELEASE_IMAGE", "OCP_IMAGE_N1"},
		{"E2E_N1_RELEASE_IMAGE", "OCP_IMAGE_N1"},
		{"E2E_N2_RELEASE_IMAGE", "OCP_IMAGE_N2"},
		{"E2E_N3_RELEASE_IMAGE", "OCP_IMAGE_N3"},
		{"E2E_N4_RELEASE_IMAGE", "OCP_IMAGE_N4"},
		{"E2E_SERVICE_DOMAIN", "HYPERSHIFT_EXTERNAL_DNS_DOMAIN"},
		{"PULL_SECRET_FILE", "PULL_SECRET"},
	} {
		if os.Getenv(mapping[0]) == "" && os.Getenv(mapping[1]) != "" {
			os.Setenv(mapping[0], os.Getenv(mapping[1]))
		}
	}
}

func setupManifestEnvironment(manifest *lifecycle.ClusterManifest) error {
	for name, image := range manifest.ReleaseImages {
		if os.Getenv(name) == "" {
			if err := os.Setenv(name, image); err != nil {
				return err
			}
		}
	}
	return nil
}
