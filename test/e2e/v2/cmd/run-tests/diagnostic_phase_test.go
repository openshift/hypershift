//go:build e2ev2

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	controlplaneoperatoroverrides "github.com/openshift/hypershift/hypershift-operator/controlplaneoperator-overrides"
	"github.com/openshift/hypershift/test/e2e/v2/internal"
	"github.com/openshift/hypershift/test/e2e/v2/lifecycle"

	"github.com/onsi/ginkgo/v2/types"
)

func diagnosticPhaseFixture(t *testing.T, state types.SpecState, informing bool) (string, string) {
	t.Helper()
	dir := t.TempDir()
	provider := filepath.Join(dir, "report-template.xml")
	spec := types.SpecReport{
		ContainerHierarchyTexts:  []string{"[sig-hypershift][Jira:Hypershift][Feature:ControlPlaneUpgrade] Control Plane Upgrade"},
		ContainerHierarchyLabels: [][]string{{"lifecycle", "control-plane-upgrade"}},
		LeafNodeText:             "should upgrade the control plane from N-1 to latest", LeafNodeType: types.NodeTypeIt, State: state,
	}
	if informing {
		spec.LeafNodeLabels = []string{internal.InformingLabel}
		spec.Failure.Message = "informing test failure: CP assertion failed"
	}
	if err := internal.GenerateJUnitReport(types.Report{SuiteDescription: "hypershift-e2e", SpecReports: types.SpecReports{spec}}, provider); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "child")
	script := `#!/bin/sh
printf '%s|%s|%s|%s\n' "$E2E_CONDITION_DIAGNOSTIC_RELEASE_IMAGE" "$E2E_HOSTED_CLUSTER_NAME" "$E2E_HOSTED_CLUSTER_NAMESPACE" "$E2E_EXPECTED_NODE_POOLS" >> "$DIAGNOSTIC_ENV_RESULT"
printf '%s\n' "$E2E_CONDITION_DIAGNOSTIC_UPGRADE_TARGET" >> "$DIAGNOSTIC_TARGET_RESULT"
printf '%s|%s\n' "$E2E_LATEST_RELEASE_IMAGE" "$E2E_PREVIOUS_RELEASE_IMAGE" >> "$DIAGNOSTIC_RELEASE_PAIR_RESULT"
printf '%s\n' "$E2E_HEALTH_WORKER_PHASE" >> "$DIAGNOSTIC_WORKER_RESULT"
for argument in "$@"; do
  case "$argument" in
    --e2e.junit-report=*) report="${argument#*=}" ;;
  esac
done
if [ "$DIAGNOSTIC_WRITE_REPORT" != "false" ]; then
  cp "$DIAGNOSTIC_REPORT_TEMPLATE" "$report.next" || exit 2
  mv "$report.next" "$report" || exit 2
fi
exit "${DIAGNOSTIC_CHILD_EXIT:-0}"
`
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	result := filepath.Join(dir, "environment")
	t.Setenv("DIAGNOSTIC_ENV_RESULT", result)
	t.Setenv("DIAGNOSTIC_TARGET_RESULT", filepath.Join(dir, "targets"))
	t.Setenv("DIAGNOSTIC_RELEASE_PAIR_RESULT", filepath.Join(dir, "release-pairs"))
	t.Setenv("DIAGNOSTIC_WORKER_RESULT", filepath.Join(dir, "worker-phases"))
	t.Setenv("E2E_HEALTH_WORKER_PHASE", "after")
	t.Setenv("DIAGNOSTIC_REPORT_TEMPLATE", provider)
	t.Setenv("DIAGNOSTIC_WRITE_REPORT", "true")
	t.Setenv("DIAGNOSTIC_CHILD_EXIT", "0")
	t.Setenv("E2E_LATEST_RELEASE_IMAGE", "quay.io/openshift-release-dev/ocp-release:4.23.0-x86_64")
	t.Setenv("E2E_CONDITION_DIAGNOSTIC_RELEASE_IMAGE", "stale-parent-phase")
	t.Setenv("E2E_CONDITION_DIAGNOSTIC_UPGRADE_TARGET", "stale-parent-target")
	return binary, result
}

func TestDiagnosticPhaseRun(t *testing.T) {
	for _, testCase := range []struct {
		name, variant, filter string
		state                 types.SpecState
		informing, advanced   bool
	}{
		{name: "When ordinary upgrade passes, it should advance only its later children", variant: "upgrade", filter: "lifecycle && control-plane-upgrade", state: types.SpecStatePassed, advanced: true},
		{name: "When PKI-disabled ordinary upgrade passes, it should advance its later children", variant: "upgrade-pki-disabled", filter: "control-plane-upgrade", state: types.SpecStatePassed, advanced: true},
		{name: "When the selected CP spec skips, it should not infer CPMain from exit zero", variant: "upgrade", filter: "control-plane-upgrade", state: types.SpecStateSkipped},
		{name: "When an informing CP assertion fails, it should not advance on exit zero", variant: "upgrade", filter: "control-plane-upgrade", state: types.SpecStateSkipped, informing: true},
		{name: "When a CP spec is pending, it should not advance", variant: "upgrade", filter: "control-plane-upgrade", state: types.SpecStatePending},
		{name: "When inherited lifecycle is excluded, it should not advance even with a CP-shaped report", variant: "upgrade", filter: "control-plane-upgrade && !lifecycle", state: types.SpecStatePassed},
		{name: "When AutoNode upgrades, it should retain initial gates", variant: "karpenter-upgrade", filter: "karpenter-upgrade", state: types.SpecStatePassed},
		{name: "When PKI-disabled AutoNode upgrades, it should retain initial gates", variant: "karpenter-upgrade-pki-disabled", filter: "karpenter-upgrade", state: types.SpecStatePassed},
		{name: "When a public NodePool crosses major versions, it should not advance CP gates", variant: "public", filter: "nodepool-major-version-upgrade", state: types.SpecStatePassed},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			binary, result := diagnosticPhaseFixture(t, testCase.state, testCase.informing)
			phase := diagnosticPhase{}
			entry := lifecycle.ClusterEntry{Variant: testCase.variant, Name: "fixture", Namespace: "clusters", ExpectedNodePools: map[string]int32{"zone-a": 1}}
			step := lifecycle.TestGroup{Name: "arbitrary-feature-identity", Variant: entry.Variant, LabelFilter: testCase.filter}
			if err := phase.run(t.Context(), binary, entry, step, filepath.Join(t.TempDir(), "feature.xml")); err != nil {
				t.Fatal(err)
			}
			key := diagnosticFixture{variant: entry.Variant, name: entry.Name, namespace: entry.Namespace}
			if phase[key] != testCase.advanced {
				t.Fatalf("phase advanced=%v, expected %v", phase[key], testCase.advanced)
			}
			step.LabelFilter = "hosted-cluster-health || control-plane-workloads"
			if err := phase.run(t.Context(), binary, entry, step, filepath.Join(t.TempDir(), "after.xml")); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(result)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			if len(lines) != 2 || lines[0] != `|fixture|clusters|{"zone-a":1}` {
				t.Fatalf("initial child inherited stale input or lost configured pools: %s", data)
			}
			want := `|fixture|clusters|{"zone-a":1}`
			if testCase.advanced {
				want = os.Getenv("E2E_LATEST_RELEASE_IMAGE") + want
			}
			if lines[1] != want || os.Getenv("E2E_CONDITION_DIAGNOSTIC_RELEASE_IMAGE") != "stale-parent-phase" {
				t.Fatalf("wrong command-local phase or mutated parent: %s", data)
			}
			targets, err := os.ReadFile(os.Getenv("DIAGNOSTIC_TARGET_RESULT"))
			if err != nil {
				t.Fatal(err)
			}
			wantTarget := os.Getenv("E2E_LATEST_RELEASE_IMAGE")
			if testCase.variant == "public" {
				wantTarget = ""
			}
			if string(targets) != strings.Repeat(wantTarget+"\n", 2) || os.Getenv("E2E_CONDITION_DIAGNOSTIC_UPGRADE_TARGET") != "stale-parent-target" {
				t.Fatalf("target must be independent of CP selection/pass and parent env: %q", targets)
			}
		})
	}
}

func TestDiagnosticPhaseRunCPOOverrides(t *testing.T) {
	binary, result := diagnosticPhaseFixture(t, types.SpecStatePassed, false)
	const inheritedLatest = "quay.io/openshift-release-dev/ocp-release:4.23.0-x86_64"
	const inheritedPrevious = "quay.io/openshift-release-dev/ocp-release:4.22.0-x86_64"
	t.Setenv("E2E_LATEST_RELEASE_IMAGE", inheritedLatest)
	t.Setenv("E2E_PREVIOUS_RELEASE_IMAGE", inheritedPrevious)
	plan, err := lifecycle.ReadTestPlan(filepath.Join("..", "..", "..", "..", "..", "contrib", "aws-e2e-v2-cpo-overrides.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Validate((&lifecycle.AWSPlatformConfig{}).ClusterSpecs(inheritedLatest, inheritedPrevious)); err != nil {
		t.Fatal(err)
	}
	manifest := &lifecycle.ClusterManifest{}
	for _, variant := range []string{"cpo-override-latest", "cpo-override-previous"} {
		manifest.Clusters = append(manifest.Clusters, lifecycle.ClusterEntry{Variant: variant, Name: variant, Namespace: "clusters", ExpectedNodePools: map[string]int32{"pool": 1}})
	}
	resolved, err := plan.TestMatrix.ResolveVariants(manifest)
	if err != nil {
		t.Fatal(err)
	}
	for _, lane := range plan.TestMatrix.Sequential {
		phase := diagnosticPhase{}
		for _, step := range lane.Steps {
			if err := phase.run(t.Context(), binary, resolved[step.Variant], step, filepath.Join(t.TempDir(), step.JUnitFile())); err != nil {
				t.Fatal(err)
			}
		}
		if len(phase) != 0 {
			t.Fatal("CPO override fixture manufactured ordinary upgrade release provenance")
		}
	}
	data, err := os.ReadFile(result)
	if err != nil {
		t.Fatal(err)
	}
	wantChildren := ""
	for _, variant := range []string{"cpo-override-latest", "cpo-override-previous"} {
		wantChildren += strings.Repeat("|"+variant+"|clusters|{\"pool\":1}\n", 4)
	}
	if string(data) != wantChildren {
		t.Fatalf("CPO children changed identities/pools or inherited a release override: %q", data)
	}
	targets, err := os.ReadFile(os.Getenv("DIAGNOSTIC_TARGET_RESULT"))
	if err != nil || string(targets) != strings.Repeat("\n", 8) {
		t.Fatalf("CPO create fixtures inherited an upgrade target: %q error=%v", targets, err)
	}
	latest, previous := controlplaneoperatoroverrides.LatestOverrideTestReleases("aws")
	entry := lifecycle.ClusterEntry{Variant: "public", Name: "public", Namespace: "clusters"}
	if err := (diagnosticPhase{}).run(t.Context(), binary, entry, lifecycle.TestGroup{Variant: "public", LabelFilter: "hosted-cluster-health"}, filepath.Join(t.TempDir(), "public.xml")); err != nil {
		t.Fatal(err)
	}
	if err := runTestBinary(t.Context(), binary, "cpo-override-latest", "azure", "hosted-cluster-cpo-required", "", filepath.Join(t.TempDir(), "azure.xml")); err != nil {
		t.Fatal(err)
	}
	pairs, err := os.ReadFile(os.Getenv("DIAGNOSTIC_RELEASE_PAIR_RESULT"))
	wantPairs := strings.Repeat(latest+"|"+previous+"\n", 8) + strings.Repeat(inheritedLatest+"|"+inheritedPrevious+"\n", 2)
	if err != nil || string(pairs) != wantPairs {
		t.Fatalf("command-local mapped pair or unrelated fixture isolation failed: %q error=%v", pairs, err)
	}
	if os.Getenv("E2E_LATEST_RELEASE_IMAGE") != inheritedLatest || os.Getenv("E2E_PREVIOUS_RELEASE_IMAGE") != inheritedPrevious || os.Getenv("E2E_CONDITION_DIAGNOSTIC_RELEASE_IMAGE") != "stale-parent-phase" || os.Getenv("E2E_CONDITION_DIAGNOSTIC_UPGRADE_TARGET") != "stale-parent-target" {
		t.Fatal("CPO child transport mutated parent process inputs")
	}
}

func TestDiagnosticPhaseRunInitialTarget(t *testing.T) {
	for _, variant := range []string{"upgrade", "upgrade-pki-disabled", "karpenter-upgrade", "karpenter-upgrade-pki-disabled", "public"} {
		t.Run("When the resolved fixture is "+variant+", it should transport independent target mode from the first child", func(t *testing.T) {
			binary, result := diagnosticPhaseFixture(t, types.SpecStateSkipped, false)
			phase := diagnosticPhase{}
			entry := lifecycle.ClusterEntry{Variant: variant, Name: "fixture", Namespace: "clusters", ExpectedNodePools: map[string]int32{}}
			for _, filter := range []string{"hosted-cluster-initial", "hosted-cluster-health", "karpenter-upgrade", "hosted-cluster-health"} {
				if err := phase.run(t.Context(), binary, entry, lifecycle.TestGroup{Variant: variant, LabelFilter: filter}, filepath.Join(t.TempDir(), "child.xml")); err != nil {
					t.Fatal(err)
				}
			}
			data, err := os.ReadFile(result)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != strings.Repeat("|fixture|clusters|{}\n", 4) {
				t.Fatalf("unproven CP/AutoNode must retain initial release: %s", data)
			}
			targets, err := os.ReadFile(os.Getenv("DIAGNOSTIC_TARGET_RESULT"))
			if err != nil {
				t.Fatal(err)
			}
			want := os.Getenv("E2E_LATEST_RELEASE_IMAGE")
			if variant == "public" {
				want = ""
			}
			if string(targets) != strings.Repeat(want+"\n", 4) {
				t.Fatalf("initial target mode mismatch: %q", targets)
			}
		})
	}
}

func TestRunTestBinaryDiagnosticIsolation(t *testing.T) {
	binary, result := diagnosticPhaseFixture(t, types.SpecStatePassed, false)
	if err := runTestBinary(t.Context(), binary, "azure-upgrade", "clusters", "control-plane-upgrade", "", filepath.Join(t.TempDir(), "child.xml")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(result)
	if err != nil {
		t.Fatal(err)
	}
	targets, err := os.ReadFile(os.Getenv("DIAGNOSTIC_TARGET_RESULT"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "|azure-upgrade|clusters|\n" || string(targets) != "\n" {
		t.Fatalf("non-AWS/parallel direct runner inherited diagnostic input: %q targets=%q", data, targets)
	}
}

func TestDiagnosticPhaseRunOrderedChildren(t *testing.T) {
	binary, result := diagnosticPhaseFixture(t, types.SpecStatePassed, false)
	plan := (&lifecycle.AWSPlatformConfig{}).DefaultTestPlan()
	entry := lifecycle.ClusterEntry{Variant: "upgrade", Name: "fixture", Namespace: "clusters", ExpectedNodePools: map[string]int32{}}
	phase := diagnosticPhase{}
	found := false
	for _, lane := range plan.TestMatrix.Sequential {
		if lane.Name != "upgrade-and-chaos" {
			continue
		}
		found = true
		matrix := lifecycle.TestMatrix{Sequential: []lifecycle.SequentialGroup{lane}}
		if err := matrix.Validate(); err != nil {
			t.Fatal(err)
		}
		resolved, err := matrix.ResolveVariants(&lifecycle.ClusterManifest{Clusters: []lifecycle.ClusterEntry{entry}})
		if err != nil {
			t.Fatal(err)
		}
		for _, step := range lane.Steps {
			if err := phase.run(t.Context(), binary, resolved[step.Variant], step, filepath.Join(t.TempDir(), step.JUnitFile())); err != nil {
				t.Fatal(err)
			}
		}
	}
	if !found {
		t.Fatal("actual default ordinary upgrade lane missing")
	}
	data, err := os.ReadFile(result)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 7 {
		t.Fatalf("actual upgrade lane child count changed: %s", data)
	}
	for index, line := range lines {
		want := "|fixture|clusters|{}"
		if index > 2 {
			want = os.Getenv("E2E_LATEST_RELEASE_IMAGE") + want
		}
		if line != want {
			t.Fatalf("actual ordered child %d got %q, expected %q", index, line, want)
		}
	}
	for _, other := range []lifecycle.ClusterEntry{
		{Variant: "upgrade", Name: "different", Namespace: entry.Namespace},
		{Variant: "upgrade", Name: entry.Name, Namespace: "other"},
		{Variant: "upgrade-pki-disabled", Name: entry.Name, Namespace: entry.Namespace},
	} {
		step := lifecycle.TestGroup{Variant: other.Variant, LabelFilter: "hosted-cluster-health"}
		if err := phase.run(t.Context(), binary, other, step, filepath.Join(t.TempDir(), "other.xml")); err != nil {
			t.Fatal(err)
		}
	}
	otherPhase := diagnosticPhase{}
	if err := otherPhase.run(t.Context(), binary, entry, lifecycle.TestGroup{Variant: entry.Variant, LabelFilter: "hosted-cluster-health"}, filepath.Join(t.TempDir(), "separate-lane.xml")); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n")[7:] {
		if !strings.HasPrefix(line, "|") {
			t.Fatalf("unrelated fixture or lane inherited CP phase: %s", line)
		}
	}
}

func TestDiagnosticPhaseRunOptInChildren(t *testing.T) {
	binary, result := diagnosticPhaseFixture(t, types.SpecStatePassed, false)
	plan, err := lifecycle.ReadTestPlan(filepath.Join("..", "..", "..", "..", "..", "contrib", "aws-e2e-v2-opt-in.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Validate((&lifecycle.AWSPlatformConfig{}).ClusterSpecs("latest", "previous")); err != nil {
		t.Fatal(err)
	}
	entry := lifecycle.ClusterEntry{Variant: "upgrade-pki-disabled", Name: "fixture", Namespace: "clusters", ExpectedNodePools: map[string]int32{}}
	phase := diagnosticPhase{}
	found := false
	for _, lane := range plan.TestMatrix.Sequential {
		if lane.Name != "aws-upgrade-without-pki-reconciliation" {
			continue
		}
		found = true
		matrix := lifecycle.TestMatrix{Sequential: []lifecycle.SequentialGroup{lane}}
		resolved, err := matrix.ResolveVariants(&lifecycle.ClusterManifest{Clusters: []lifecycle.ClusterEntry{entry}})
		if err != nil {
			t.Fatal(err)
		}
		for _, step := range lane.Steps {
			if err := phase.run(t.Context(), binary, resolved[step.Variant], step, filepath.Join(t.TempDir(), step.JUnitFile())); err != nil {
				t.Fatal(err)
			}
		}
	}
	data, err := os.ReadFile(result)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if !found || len(lines) != 5 {
		t.Fatalf("actual PKI-disabled upgrade lane changed: %s", data)
	}
	for index, line := range lines {
		want := "|fixture|clusters|{}"
		if index > 2 {
			want = os.Getenv("E2E_LATEST_RELEASE_IMAGE") + want
		}
		if line != want {
			t.Fatalf("actual PKI-disabled child %d got %q, expected %q", index, line, want)
		}
	}
}

func TestDiagnosticPhaseRunUnavailableProvenance(t *testing.T) {
	for _, testCase := range []struct {
		name, exit, filter string
		stale              bool
		wantError          bool
	}{
		{name: "When a selected child fails, it should never advance", exit: "1", filter: "control-plane-upgrade", wantError: true},
		{name: "When the label expression is malformed, it should reject it without starting a child", filter: "&&&", wantError: true},
		{name: "When a successful child does not replace stale JUnit, it should preserve its result without advancing", exit: "0", filter: "control-plane-upgrade", stale: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			binary, result := diagnosticPhaseFixture(t, types.SpecStatePassed, false)
			t.Setenv("DIAGNOSTIC_CHILD_EXIT", testCase.exit)
			path := filepath.Join(t.TempDir(), "report.xml")
			if testCase.stale {
				data, err := os.ReadFile(os.Getenv("DIAGNOSTIC_REPORT_TEMPLATE"))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
				t.Setenv("DIAGNOSTIC_WRITE_REPORT", "false")
			}
			phase := diagnosticPhase{}
			entry := lifecycle.ClusterEntry{Variant: "upgrade", Name: "fixture", Namespace: "clusters"}
			err := phase.run(t.Context(), binary, entry, lifecycle.TestGroup{Variant: entry.Variant, LabelFilter: testCase.filter}, path)
			if (err != nil) != testCase.wantError || phase[diagnosticFixture{variant: entry.Variant, name: entry.Name, namespace: entry.Namespace}] {
				t.Fatalf("error=%v, phase=%v", err, phase)
			}
			if testCase.filter == "&&&" {
				if _, err := os.Stat(result); !os.IsNotExist(err) {
					t.Fatal("malformed selection launched a child")
				}
			}
		})
	}
}

func TestControlPlaneDiagnosticPassed(t *testing.T) {
	for _, testCase := range []struct {
		name, data string
	}{
		{name: "When the report is malformed, it should qualify missing provenance", data: "not XML"},
		{name: "When no CP leaf was reported, it should not infer execution", data: "<testsuites><testsuite name=\"hypershift-e2e\"/></testsuites>"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "report.xml")
			if err := os.WriteFile(path, []byte(testCase.data), 0600); err != nil {
				t.Fatal(err)
			}
			if passed, err := controlPlaneDiagnosticPassed(path, nil); passed || err == nil {
				t.Fatalf("passed=%v, error=%v", passed, err)
			}
		})
	}
}

func TestDiagnosticPhaseRunWorkerApplicability(t *testing.T) {
	binary, result := diagnosticPhaseFixture(t, types.SpecStatePassed, false)
	entry := lifecycle.ClusterEntry{Variant: "karpenter-upgrade", Name: "fixture", Namespace: "clusters", ExpectedNodePools: map[string]int32{}}
	phase := diagnosticPhase{}
	steps := (&lifecycle.AWSPlatformConfig{}).TestMatrix().Sequential
	var lane *lifecycle.SequentialGroup
	for index := range steps {
		if steps[index].Name == "karpenter-upgrade" {
			lane = &steps[index]
		}
	}
	if lane == nil || len(lane.Steps) != 4 {
		t.Fatal("actual AutoNode upgrade lane changed")
	}
	for _, step := range append(append([]lifecycle.TestGroup{}, lane.Steps...), lane.Steps[1], lane.Steps[2]) {
		if err := phase.run(t.Context(), binary, entry, step, filepath.Join(t.TempDir(), step.JUnitFile())); err != nil {
			t.Fatal(err)
		}
	}
	if err := runTestBinary(t.Context(), binary, entry.Name, entry.Namespace, "hosted-cluster-health", "", filepath.Join(t.TempDir(), "standalone.xml")); err != nil {
		t.Fatal(err)
	}
	if err := runTestBinaryWithGrace(t.Context(), binary, entry.Name, entry.Namespace, "hosted-cluster-health", "", filepath.Join(t.TempDir(), "wrapper.xml"), defaultInterruptGrace, defaultInterruptWait); err != nil {
		t.Fatal(err)
	}
	if err := runTestBinaryWithDiagnosticRelease(t.Context(), binary, entry.Name, entry.Namespace, "hosted-cluster-health", "", filepath.Join(t.TempDir(), "diagnostic.xml"), "", "", nil, defaultInterruptGrace, defaultInterruptWait); err != nil {
		t.Fatal(err)
	}
	workers, err := os.ReadFile(os.Getenv("DIAGNOSTIC_WORKER_RESULT"))
	if err != nil || string(workers) != "\n\n\nafter\n\n\n\n\n\n" {
		t.Fatalf("after transport or subsequent before/Main/wrapper clearing failed: %q error=%v", workers, err)
	}
	targets, err := os.ReadFile(os.Getenv("DIAGNOSTIC_TARGET_RESULT"))
	if err != nil || string(targets) != strings.Repeat(os.Getenv("E2E_LATEST_RELEASE_IMAGE")+"\n", 6)+"\n\n\n" {
		t.Fatalf("worker phase changed independent AutoNode target transport: %q error=%v", targets, err)
	}
	children, err := os.ReadFile(result)
	if err != nil || string(children) != strings.Repeat("|fixture|clusters|{}\n", 6)+strings.Repeat("|fixture|clusters|\n", 3) {
		t.Fatalf("worker phase changed AutoNode release lineage or initial intent: %q error=%v", children, err)
	}
	if os.Getenv("E2E_HEALTH_WORKER_PHASE") != "after" || os.Getenv("E2E_CONDITION_DIAGNOSTIC_RELEASE_IMAGE") != "stale-parent-phase" || os.Getenv("E2E_CONDITION_DIAGNOSTIC_UPGRADE_TARGET") != "stale-parent-target" {
		t.Fatal("command-local inputs mutated the parent process")
	}
}
