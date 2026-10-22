//go:build e2ev2

package tests

import (
	"context"
	"errors"
	"strings"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	controlplaneoperatoroverrides "github.com/openshift/hypershift/hypershift-operator/controlplaneoperator-overrides"
	hyperapi "github.com/openshift/hypershift/support/api"
	supportawsutil "github.com/openshift/hypershift/support/awsutil"
	"github.com/openshift/hypershift/test/e2e/v2/internal"
	v2util "github.com/openshift/hypershift/test/e2e/v2/util"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/blang/semver"
)

func TestNodePoolCloudVersionApplicable(t *testing.T) {
	for _, minimum := range []string{"4.19.0", "4.22.0"} {
		for _, testCase := range []struct {
			name, previous, latest, override, target string
			previousVersion, latestVersion, observed string
			want, wantError                          bool
		}{
			{name: "When origin is below the boundary and history is newer, it should remain ineligible", previous: "previous", previousVersion: "4.18.0", observed: "5.0.0"},
			{name: "When origin meets the boundary and history is older, it should remain eligible", previous: "previous", previousVersion: minimum, observed: "4.14.0", want: true},
			{name: "When origin meets the boundary and history is absent, it should remain eligible", previous: "previous", previousVersion: minimum, want: true},
			{name: "When latest origin meets the boundary and history is malformed, it should remain eligible", latest: "latest", latestVersion: minimum, observed: "malformed", want: true},
			{name: "When ordinary CP selects latest, it should not gate on previous", previous: "previous", latest: "latest", override: "latest", target: "latest", previousVersion: "4.18.0", latestVersion: minimum, observed: "4.18.0", want: true},
			{name: "When AutoNode upgrades to latest, it should still gate on previous", previous: "previous", latest: "latest", target: "latest", previousVersion: "4.18.0", latestVersion: minimum, observed: minimum},
			{name: "When configured origin is unresolved, it should fail without history fallback", latest: "latest", observed: minimum, wantError: true},
			{name: "When configured origin metadata is invalid, it should retain the resolution error", previous: "previous", observed: minimum, wantError: true},
			{name: "When configured override is invalid, it should fail despite resolved latest", latest: "latest", override: "other", latestVersion: minimum, observed: minimum, wantError: true},
			{name: "When configured target is invalid, it should fail before applying the boundary", previous: "previous", latest: "latest", target: "other", previousVersion: "4.18.0", observed: minimum, wantError: true},
			{name: "When target lacks latest, it should not fall back to standalone history", target: "latest", observed: minimum, wantError: true},
			{name: "When standalone history is below the boundary, it should remain ineligible", observed: "4.18.0"},
			{name: "When standalone history meets the boundary, it should remain eligible", observed: minimum, want: true},
			{name: "When standalone history is malformed, it should return the native error", observed: "malformed", wantError: true},
			{name: "When standalone history is absent, it should return the native error", wantError: true},
		} {
			t.Run(testCase.name+" at "+minimum, func(t *testing.T) {
				t.Setenv("E2E_PREVIOUS_RELEASE_IMAGE", testCase.previous)
				t.Setenv("E2E_LATEST_RELEASE_IMAGE", testCase.latest)
				t.Setenv("E2E_CONDITION_DIAGNOSTIC_RELEASE_IMAGE", testCase.override)
				t.Setenv("E2E_CONDITION_DIAGNOSTIC_UPGRADE_TARGET", testCase.target)
				hc := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Name: "cluster", Namespace: "clusters"}}
				if testCase.observed != "" {
					hc.Status.Version = &hyperv1.ClusterVersionStatus{History: []configv1.UpdateHistory{{Version: testCase.observed}}}
				}
				reads := 0
				client := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(hc).WithInterceptorFuncs(interceptor.Funcs{Get: func(ctx context.Context, client crclient.WithWatch, key crclient.ObjectKey, object crclient.Object, opts ...crclient.GetOption) error {
					reads++
					return client.Get(ctx, key, object, opts...)
				}}).Build()
				metadataErr := errors.New("independent metadata malformed")
				testCtx := &internal.TestContext{Context: t.Context(), MgmtClient: client, ClusterName: hc.Name, ClusterNamespace: hc.Namespace, ConditionDiagnosticReleaseVersions: map[string]semver.Version{}, ConditionDiagnosticReleaseErrors: map[string]error{"previous": metadataErr}}
				if testCase.previousVersion != "" {
					testCtx.ConditionDiagnosticReleaseVersions["previous"] = semver.MustParse(testCase.previousVersion)
				}
				if testCase.latestVersion != "" {
					testCtx.ConditionDiagnosticReleaseVersions["latest"] = semver.MustParse(testCase.latestVersion)
				}
				resetFailureConditionPhase(testCtx)
				actual, err := nodePoolCloudVersionApplicable(testCtx, semver.MustParse(minimum))
				if actual != testCase.want || (err != nil) != testCase.wantError {
					t.Fatalf("applicable=%t error=%v; want %t error=%t", actual, err, testCase.want, testCase.wantError)
				}
				configured := testCase.previous != "" || testCase.latest != "" || testCase.override != "" || testCase.target != ""
				if configured && reads != 0 || !configured && reads != 1 {
					t.Fatalf("origin lookup performed %d management reads; configured=%t", reads, configured)
				}
				if testCase.previous != "" && testCase.previousVersion == "" && !errors.Is(err, metadataErr) {
					t.Fatalf("origin metadata error was lost: %v", err)
				}
			})
		}
	}
	t.Run("When the actual AWS CPO mapped pair is configured, it should consume image-keyed metadata without cluster access", func(t *testing.T) {
		latest, previous := controlplaneoperatoroverrides.LatestOverrideTestReleases("aws")
		if latest == "" || previous == "" || latest == previous {
			t.Fatal("AWS CPO mapped releases must be nonempty and distinct")
		}
		t.Setenv("E2E_PREVIOUS_RELEASE_IMAGE", previous)
		t.Setenv("E2E_LATEST_RELEASE_IMAGE", latest)
		t.Setenv("E2E_CONDITION_DIAGNOSTIC_RELEASE_IMAGE", "")
		t.Setenv("E2E_CONDITION_DIAGNOSTIC_UPGRADE_TARGET", "")
		testCtx := &internal.TestContext{ConditionDiagnosticReleaseVersions: map[string]semver.Version{previous: semver.MustParse("4.19.0"), latest: semver.MustParse("4.22.0")}}
		resetFailureConditionPhase(testCtx)
		for _, minimum := range []string{"4.19.0", "4.22.0"} {
			actual, err := nodePoolCloudVersionApplicable(testCtx, semver.MustParse(minimum))
			if err != nil || actual != (minimum == "4.19.0") {
				t.Fatalf("mapped previous eligibility at %s=%t error=%v", minimum, actual, err)
			}
		}
		t.Setenv("E2E_CONDITION_DIAGNOSTIC_RELEASE_IMAGE", latest)
		resetFailureConditionPhase(testCtx)
		actual, err := nodePoolCloudVersionApplicable(testCtx, semver.MustParse("4.22.0"))
		if err != nil || !actual {
			t.Fatalf("mapped latest eligibility=%t error=%v", actual, err)
		}
	})
}

func TestNodePoolLegacyRolloutRequired(t *testing.T) {
	testNodePoolReleaseGate(t, "4.16.0", "4.17.0", true, nodePoolLegacyRolloutRequired)
}

func TestNodePoolProvisioningConditionsApplicable(t *testing.T) {
	testNodePoolReleaseGate(t, "4.22.0", "4.23.0", false, nodePoolProvisioningConditionsApplicable)
}

func testNodePoolReleaseGate(t *testing.T, below, boundary string, belowWant bool, gate func(*internal.TestContext) (bool, error)) {
	t.Helper()
	for _, testCase := range []struct {
		name, previous, latest, override, target, phase string
		previousVersion, latestVersion, observed        string
		want, wantError, readError, metadataError       bool
	}{
		{name: "When origin is below the boundary and history is newer, it should use the origin", previous: "previous", previousVersion: below, observed: boundary, want: belowWant},
		{name: "When origin meets the boundary and history is older, it should use the origin", previous: "previous", previousVersion: boundary, observed: below, want: !belowWant},
		{name: "When origin meets the boundary and history is absent, it should use the origin", previous: "previous", previousVersion: boundary, want: !belowWant},
		{name: "When latest origin meets the boundary and history is malformed, it should use the origin", latest: "latest", latestVersion: boundary, observed: "malformed", want: !belowWant},
		{name: "When CP starts at latest, it should not use previous", previous: "previous", latest: "latest", override: "latest", target: "latest", previousVersion: below, latestVersion: boundary, observed: below, want: !belowWant},
		{name: "When AutoNode targets latest before transition, it should use previous", previous: "previous", latest: "latest", target: "latest", previousVersion: below, latestVersion: boundary, observed: boundary, want: belowWant},
		{name: "When AutoNode has transitioned its current phase, it should use latest", previous: "previous", latest: "latest", target: "latest", phase: "latest", previousVersion: below, latestVersion: boundary, observed: below, want: !belowWant},
		{name: "When configured origin is unresolved, it should fail without history fallback", latest: "latest", observed: boundary, wantError: true},
		{name: "When previous metadata failed, it should preserve that error", previous: "previous", observed: boundary, wantError: true, metadataError: true},
		{name: "When latest metadata failed, it should preserve that error", latest: "latest", phase: "latest", observed: boundary, wantError: true, metadataError: true},
		{name: "When configured override is invalid, it should fail despite resolved latest", latest: "latest", override: "other", latestVersion: boundary, observed: boundary, wantError: true},
		{name: "When configured target is invalid, it should fail before applying the boundary", previous: "previous", latest: "latest", target: "other", previousVersion: below, observed: boundary, wantError: true},
		{name: "When target lacks latest, it should not fall back to standalone history", target: "latest", observed: boundary, wantError: true},
		{name: "When standalone history is below the boundary, it should use that history", observed: below, want: belowWant},
		{name: "When standalone history meets the boundary, it should use that history", observed: boundary, want: !belowWant},
		{name: "When standalone history is malformed, it should return the native error", observed: "malformed", wantError: true},
		{name: "When standalone history is absent, it should return the native error", wantError: true},
		{name: "When standalone cluster lookup fails, it should preserve the read error", readError: true, wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("E2E_PREVIOUS_RELEASE_IMAGE", testCase.previous)
			t.Setenv("E2E_LATEST_RELEASE_IMAGE", testCase.latest)
			t.Setenv("E2E_CONDITION_DIAGNOSTIC_RELEASE_IMAGE", testCase.override)
			t.Setenv("E2E_CONDITION_DIAGNOSTIC_UPGRADE_TARGET", testCase.target)
			hc := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Name: "cluster", Namespace: "clusters"}, Spec: hyperv1.HostedClusterSpec{Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform}}}
			if testCase.observed != "" {
				hc.Status.Version = &hyperv1.ClusterVersionStatus{History: []configv1.UpdateHistory{{Version: testCase.observed}}}
			}
			reads := 0
			readErr := errors.New("standalone cluster lookup failed")
			client := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(hc).WithInterceptorFuncs(interceptor.Funcs{Get: func(ctx context.Context, client crclient.WithWatch, key crclient.ObjectKey, object crclient.Object, opts ...crclient.GetOption) error {
				reads++
				if testCase.readError {
					return readErr
				}
				return client.Get(ctx, key, object, opts...)
			}}).Build()
			metadataErr := errors.New("independent metadata malformed")
			testCtx := &internal.TestContext{Context: t.Context(), MgmtClient: client, ClusterName: hc.Name, ClusterNamespace: hc.Namespace, ConditionDiagnosticReleaseVersions: map[string]semver.Version{}, ConditionDiagnosticReleaseErrors: map[string]error{"previous": metadataErr, "latest": metadataErr}}
			if testCase.previousVersion != "" {
				testCtx.ConditionDiagnosticReleaseVersions["previous"] = semver.MustParse(testCase.previousVersion)
			}
			if testCase.latestVersion != "" {
				testCtx.ConditionDiagnosticReleaseVersions["latest"] = semver.MustParse(testCase.latestVersion)
			}
			resetFailureConditionPhase(testCtx)
			if testCase.phase != "" {
				testCtx.ConditionDiagnosticReleaseImage = testCase.phase
			}
			actual, err := gate(testCtx)
			if actual != testCase.want || (err != nil) != testCase.wantError {
				t.Fatalf("gate=%t error=%v; want %t error=%t", actual, err, testCase.want, testCase.wantError)
			}
			configured := testCase.previous != "" || testCase.latest != "" || testCase.override != "" || testCase.target != ""
			if configured && reads != 0 || !configured && reads != 1 {
				t.Fatalf("origin lookup performed %d management reads; configured=%t", reads, configured)
			}
			if testCase.metadataError && !errors.Is(err, metadataErr) {
				t.Fatalf("origin metadata error was lost: %v", err)
			}
			if testCase.readError && !errors.Is(err, readErr) {
				t.Fatalf("standalone read error was lost: %v", err)
			}
		})
	}
	t.Run("When the actual AWS CPO mapped pair changes phase, it should use image-keyed metadata without cluster access", func(t *testing.T) {
		testNodePoolReleaseGateMappedPair(t, below, boundary, belowWant, gate)
	})
}

func testNodePoolReleaseGateMappedPair(t *testing.T, below, boundary string, belowWant bool, gate func(*internal.TestContext) (bool, error)) {
	t.Helper()
	latest, previous := controlplaneoperatoroverrides.LatestOverrideTestReleases("aws")
	if latest == "" || previous == "" || latest == previous {
		t.Fatal("AWS CPO mapped releases must be nonempty and distinct")
	}
	t.Setenv("E2E_PREVIOUS_RELEASE_IMAGE", previous)
	t.Setenv("E2E_LATEST_RELEASE_IMAGE", latest)
	t.Setenv("E2E_CONDITION_DIAGNOSTIC_RELEASE_IMAGE", "")
	t.Setenv("E2E_CONDITION_DIAGNOSTIC_UPGRADE_TARGET", latest)
	testCtx := &internal.TestContext{ConditionDiagnosticReleaseVersions: map[string]semver.Version{previous: semver.MustParse(below), latest: semver.MustParse(boundary)}}
	resetFailureConditionPhase(testCtx)
	actual, err := gate(testCtx)
	if err != nil || actual != belowWant {
		t.Fatalf("mapped previous eligibility=%t error=%v", actual, err)
	}
	testCtx.ConditionDiagnosticReleaseImage = latest
	actual, err = gate(testCtx)
	if err != nil || actual != !belowWant {
		t.Fatalf("mapped latest eligibility=%t error=%v", actual, err)
	}
}

func TestLatestNodePoolMachineSet(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		revisions []string
		want      string
	}{
		{name: "When revisions are unordered, it should select the largest numeric revision", revisions: []string{"9", "11", "10"}, want: "11"},
		{name: "When old revisions are invalid, it should ignore them", revisions: []string{"invalid", "", "-1", "0"}, want: "0"},
		{name: "When all revisions are invalid, it should reject them", revisions: []string{"invalid", "", "-1"}},
		{name: "When no MachineSets exist, it should reject the empty list"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var machineSets []capiv1.MachineSet
			for _, revision := range testCase.revisions {
				machineSets = append(machineSets, capiv1.MachineSet{ObjectMeta: metav1.ObjectMeta{Name: revision, Annotations: map[string]string{capiv1.RevisionAnnotation: revision}}})
			}
			actual, err := latestNodePoolMachineSet(machineSets)
			if actual != testCase.want || (err != nil) != (testCase.want == "") {
				t.Fatalf("latestNodePoolMachineSet() = %q, %v; want %q", actual, err, testCase.want)
			}
		})
	}
}

func TestNodePoolMajorUpgradeVersions(t *testing.T) {
	for _, testCase := range []struct {
		previous  string
		latest    string
		wantError bool
	}{
		{previous: "4.23.0", latest: "5.0.0"},
		{previous: "5.0.0", latest: "6.0.0"},
		{previous: "4.22.0", latest: "4.23.0", wantError: true},
		{previous: "5.0.0", latest: "5.0.0", wantError: true},
		{previous: "5.0.0", latest: "4.23.0", wantError: true},
		{previous: "3.11.0", latest: "4.0.0", wantError: true},
	} {
		t.Run("When upgrading "+testCase.previous+" to "+testCase.latest+", it should enforce the major boundary", func(t *testing.T) {
			err := nodePoolMajorUpgradeVersions(semver.MustParse(testCase.previous), semver.MustParse(testCase.latest))
			if (err != nil) != testCase.wantError {
				t.Fatalf("nodePoolMajorUpgradeVersions() = %v; want error %t", err, testCase.wantError)
			}
		})
	}
}

func TestNodePoolUpgradeVersions(t *testing.T) {
	for _, testCase := range []struct {
		previous, latest string
		wantError        bool
	}{
		{previous: "4.22.0", latest: "4.23.0"},
		{previous: "4.23.0", latest: "5.0.0"},
		{previous: "4.23.0-0.nightly-2026-10-01", latest: "4.23.0-0.nightly-2026-10-02"},
		{previous: "4.23.0", latest: "4.23.0", wantError: true},
		{previous: "4.23.0", latest: "4.22.0", wantError: true},
	} {
		t.Run("When upgrading "+testCase.previous+" to "+testCase.latest+", it should require a newer target", func(t *testing.T) {
			err := nodePoolUpgradeVersions(semver.MustParse(testCase.previous), semver.MustParse(testCase.latest))
			if (err != nil) != testCase.wantError {
				t.Fatalf("nodePoolUpgradeVersions() = %v; want error %t", err, testCase.wantError)
			}
		})
	}
}

func TestAwsNodePoolResourceTags(t *testing.T) {
	for _, testCase := range []struct {
		name, jobID string
	}{
		{name: "When running outside Prow, it should omit only the job tag"},
		{name: "When a Prow job is supplied, it should preserve provenance", jobID: "test-job-id"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			tags := awsNodePoolResourceTags("test-infra", "test-cluster", testCase.jobID)
			for key, want := range map[string]string{
				supportawsutil.HypershiftSourceTagKey:      "e2e",
				supportawsutil.HypershiftInfraIDTagKey:     "test-infra",
				supportawsutil.HypershiftClusterNameTagKey: "test-cluster",
			} {
				if tags[key] != want {
					t.Fatalf("resource tag %s = %q; want %q", key, tags[key], want)
				}
			}
			jobID, found := tags[supportawsutil.HypershiftProwJobIDTagKey]
			if found != (testCase.jobID != "") || jobID != testCase.jobID {
				t.Fatalf("job provenance tag = %q, present %t; want %q", jobID, found, testCase.jobID)
			}
		})
	}
}

func TestNodePoolRuntimeStream(t *testing.T) {
	for _, testCase := range []struct {
		name, observed, requested, version, want string
		wantError                                bool
	}{
		{name: "When observed and requested differ, it should prefer observed", observed: hyperv1.OSImageStreamRHEL9, requested: hyperv1.OSImageStreamRHEL10, version: "5.0.0", want: hyperv1.OSImageStreamRHEL9},
		{name: "When observed is absent, it should prefer explicit pin over version", requested: hyperv1.OSImageStreamRHEL9, version: "5.0.0", want: hyperv1.OSImageStreamRHEL9},
		{name: "When stream is absent on OCP5, it should use status version", version: "5.0.0", want: hyperv1.OSImageStreamRHEL10},
		{name: "When stream is absent on OCP4, it should use status version", version: "4.23.0", want: hyperv1.OSImageStreamRHEL9},
		{name: "When status version is malformed, it should not guess", version: "invalid", wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			pool := &hyperv1.NodePool{}
			pool.Status.OSImageStream.Name = testCase.observed
			pool.Spec.OSImageStream.Name = testCase.requested
			pool.Status.Version = testCase.version
			actual, err := nodePoolRuntimeStream(pool)
			if actual != testCase.want || (err != nil) != testCase.wantError {
				t.Fatalf("nodePoolRuntimeStream() = %q, %v; want %q, error %t", actual, err, testCase.want, testCase.wantError)
			}
		})
	}
}

func TestNodePoolRuntimeChecksEnabled(t *testing.T) {
	for _, testCase := range []struct {
		name, suiteVersion, poolVersion, wantStream string
		want                                        bool
	}{
		{name: "When suite is 4.17, it should not require runtime handlers even for a newer pool", suiteVersion: "4.17.0", poolVersion: "5.0.0"},
		{name: "When suite is 4.18, it should require handlers even for an older pool", suiteVersion: "4.18.0", poolVersion: "4.16.0", want: true, wantStream: hyperv1.OSImageStreamRHEL9},
		{name: "When suite is OCP5 with an older pool, it should still require RHEL9 handlers", suiteVersion: "5.0.0", poolVersion: "4.23.0", want: true, wantStream: hyperv1.OSImageStreamRHEL9},
		{name: "When suite and pool are OCP5, it should require RHEL10 handlers", suiteVersion: "5.0.0", poolVersion: "5.0.0", want: true, wantStream: hyperv1.OSImageStreamRHEL10},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			actual := nodePoolRuntimeChecksEnabled(semver.MustParse(testCase.suiteVersion))
			if actual != testCase.want {
				t.Fatalf("runtime checks enabled = %t; want %t", actual, testCase.want)
			}
			if actual {
				pool := &hyperv1.NodePool{}
				pool.Status.Version = testCase.poolVersion
				stream, err := nodePoolRuntimeStream(pool)
				if err != nil || stream != testCase.wantStream {
					t.Fatalf("runtime stream = %q, %v; want %q", stream, err, testCase.wantStream)
				}
			}
		})
	}
}

func TestAwsInstanceID(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		providerID string
		want       string
	}{
		{name: "When providerID is AWS, it should extract the EC2 identity", providerID: "aws:///us-east-1a/i-0123456789abcdef0", want: "i-0123456789abcdef0"},
		{name: "When providerID is empty, it should reject it"},
		{name: "When providerID has no AWS prefix, it should reject it", providerID: "azure:///zone/i-0123456789abcdef0"},
		{name: "When providerID has an empty zone, it should reject it", providerID: "aws:////i-0123456789abcdef0"},
		{name: "When providerID has no instance, it should reject it", providerID: "aws:///us-east-1a/"},
		{name: "When providerID contains an invalid instance, it should reject it", providerID: "aws:///us-east-1a/unknown"},
		{name: "When providerID contains a truncated instance, it should reject it", providerID: "aws:///us-east-1a/i-123"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			actual, err := awsInstanceID(testCase.providerID)
			if (err != nil) != (testCase.want == "") || actual != testCase.want {
				t.Fatalf("awsInstanceID() = %q, %v; want %q", actual, err, testCase.want)
			}
		})
	}
}

func TestAwsPoolNoRolloutConditions(t *testing.T) {
	for _, test := range []struct {
		name           string
		pool           *hyperv1.NodePool
		configStatus   corev1.ConditionStatus
		platformStatus corev1.ConditionStatus
		wantError      bool
	}{
		{name: "When the pool is nil, it should reject missing rollout status", wantError: true},
		{name: "When both rollout conditions are absent, it should reject them", pool: &hyperv1.NodePool{}, wantError: true},
		{name: "When UpdatingConfig is absent, it should reject it", pool: &hyperv1.NodePool{}, platformStatus: corev1.ConditionFalse, wantError: true},
		{name: "When UpdatingPlatformMachineTemplate is absent, it should reject it", pool: &hyperv1.NodePool{}, configStatus: corev1.ConditionFalse, wantError: true},
		{name: "When UpdatingConfig is Unknown, it should reject it", pool: &hyperv1.NodePool{}, configStatus: corev1.ConditionUnknown, platformStatus: corev1.ConditionFalse, wantError: true},
		{name: "When UpdatingConfig is True, it should reject it", pool: &hyperv1.NodePool{}, configStatus: corev1.ConditionTrue, platformStatus: corev1.ConditionFalse, wantError: true},
		{name: "When UpdatingPlatformMachineTemplate is Unknown, it should reject it", pool: &hyperv1.NodePool{}, configStatus: corev1.ConditionFalse, platformStatus: corev1.ConditionUnknown, wantError: true},
		{name: "When UpdatingPlatformMachineTemplate is True, it should reject it", pool: &hyperv1.NodePool{}, configStatus: corev1.ConditionFalse, platformStatus: corev1.ConditionTrue, wantError: true},
		{name: "When both rollout conditions are False, it should accept them", pool: &hyperv1.NodePool{}, configStatus: corev1.ConditionFalse, platformStatus: corev1.ConditionFalse},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.configStatus != "" {
				test.pool.Status.Conditions = append(test.pool.Status.Conditions, hyperv1.NodePoolCondition{Type: hyperv1.NodePoolUpdatingConfigConditionType, Status: test.configStatus})
			}
			if test.platformStatus != "" {
				test.pool.Status.Conditions = append(test.pool.Status.Conditions, hyperv1.NodePoolCondition{Type: hyperv1.NodePoolUpdatingPlatformMachineTemplateConditionType, Status: test.platformStatus})
			}
			err := awsPoolNoRolloutConditions(test.pool)
			if (err != nil) != test.wantError {
				t.Fatalf("unexpected no-rollout validation result: %v", err)
			}
		})
	}
}

func TestAwsPoolNoRolloutObservation(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		statuses  []corev1.ConditionStatus
		wantError bool
	}{
		{name: "When False changes to True then False, it should remember the rollout", statuses: []corev1.ConditionStatus{corev1.ConditionFalse, corev1.ConditionTrue, corev1.ConditionFalse}, wantError: true},
		{name: "When no True is observed, it should accept the sequence", statuses: []corev1.ConditionStatus{corev1.ConditionFalse, corev1.ConditionFalse, corev1.ConditionFalse}},
	} {
		for _, conditionType := range []string{hyperv1.NodePoolUpdatingConfigConditionType, hyperv1.NodePoolUpdatingPlatformMachineTemplateConditionType} {
			t.Run(testCase.name+" for "+conditionType, func(t *testing.T) {
				observation := &awsPoolNoRolloutObservation{}
				for _, status := range testCase.statuses {
					pool := &hyperv1.NodePool{Status: hyperv1.NodePoolStatus{Conditions: []hyperv1.NodePoolCondition{
						{Type: hyperv1.NodePoolUpdatingConfigConditionType, Status: corev1.ConditionFalse},
						{Type: hyperv1.NodePoolUpdatingPlatformMachineTemplateConditionType, Status: corev1.ConditionFalse},
					}}}
					awsPoolCondition(pool, conditionType).Status = status
					_ = observation.observe(pool)
				}
				if (observation.violation != nil) != testCase.wantError {
					t.Fatalf("no-rollout observation = %v; want error %t", observation.violation, testCase.wantError)
				}
			})
		}
	}
}

func TestNodePoolConditionPredicate(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		conditions []hyperv1.NodePoolCondition
		want       bool
	}{
		{name: "When condition has expected status, it should accept it", conditions: []hyperv1.NodePoolCondition{{Type: "Ready", Status: corev1.ConditionTrue}}, want: true},
		{name: "When condition is absent, it should reject it"},
		{name: "When condition has the opposite status, it should reject it", conditions: []hyperv1.NodePoolCondition{{Type: "Ready", Status: corev1.ConditionFalse}}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			predicate := nodePoolConditionPredicate[*hyperv1.NodePool](v2util.Condition{Type: "Ready", Status: metav1.ConditionTrue})
			actual, _, err := predicate(&hyperv1.NodePool{Status: hyperv1.NodePoolStatus{Conditions: testCase.conditions}})
			if err != nil || actual != testCase.want {
				t.Fatalf("condition predicate = %t, %v; want %t", actual, err, testCase.want)
			}
		})
	}
}

func TestNodePoolUpdatingTransitionPredicate(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		statuses []corev1.ConditionStatus
		want     bool
	}{
		{name: "When True precedes False, it should complete", statuses: []corev1.ConditionStatus{corev1.ConditionTrue, corev1.ConditionFalse}, want: true},
		{name: "When False precedes True then False, it should complete only after the transition", statuses: []corev1.ConditionStatus{corev1.ConditionFalse, corev1.ConditionTrue, corev1.ConditionFalse}, want: true},
		{name: "When True is never observed, it should not complete", statuses: []corev1.ConditionStatus{corev1.ConditionFalse, corev1.ConditionFalse}},
		{name: "When updating has not finished, it should not complete", statuses: []corev1.ConditionStatus{corev1.ConditionTrue}},
		{name: "When condition is absent, it should not complete", statuses: []corev1.ConditionStatus{""}},
		{name: "When status becomes Unknown, it should not complete", statuses: []corev1.ConditionStatus{corev1.ConditionTrue, corev1.ConditionUnknown}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			predicate := nodePoolUpdatingTransitionPredicate(hyperv1.NodePoolUpdatingConfigConditionType)
			var complete bool
			for index, status := range testCase.statuses {
				pool := &hyperv1.NodePool{}
				if status != "" {
					pool.Status.Conditions = []hyperv1.NodePoolCondition{{Type: hyperv1.NodePoolUpdatingConfigConditionType, Status: status}}
				}
				var err error
				complete, _, err = predicate(pool)
				if err != nil || (index < len(testCase.statuses)-1 && complete) {
					t.Fatalf("transition completed too early at step %d: %t, %v", index, complete, err)
				}
			}
			if complete != testCase.want {
				t.Fatalf("transition complete = %t; want %t", complete, testCase.want)
			}
		})
	}
}

func TestNodePoolOSImageStreamPredicate(t *testing.T) {
	for _, testCase := range []struct {
		name, actual string
		want         bool
	}{
		{name: "When stream matches, it should accept it", actual: hyperv1.OSImageStreamRHEL10, want: true},
		{name: "When stream is missing, it should reject it"},
		{name: "When stream differs, it should reject it", actual: hyperv1.OSImageStreamRHEL9},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			pool := &hyperv1.NodePool{}
			pool.Status.OSImageStream.Name = testCase.actual
			actual, _, err := nodePoolOSImageStreamPredicate(hyperv1.OSImageStreamRHEL10)(pool)
			if err != nil || actual != testCase.want {
				t.Fatalf("stream predicate = %t, %v; want %t", actual, err, testCase.want)
			}
		})
	}
}

func TestNodePoolUniqueName(t *testing.T) {
	for _, testCase := range []struct{ name, prefix string }{
		{name: "When prefix is short, it should preserve it", prefix: "test-pool-"},
		{name: "When prefix is overlong, it should produce a bounded name", prefix: strings.Repeat("a", 80)},
		{name: "When prefix is empty, it should produce a valid fallback name"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			name := nodePoolUniqueName(testCase.prefix)
			if len(name) > 63 || strings.HasPrefix(name, "-") || name == nodePoolUniqueName(testCase.prefix) {
				t.Fatalf("invalid or non-unique generated NodePool name: %q", name)
			}
		})
	}
}

func TestNodePoolUpdateObject(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		exists bool
	}{
		{name: "When object exists, it should persist the mutation and preserve other fields", exists: true},
		{name: "When object is missing, it should return the API error"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			pool := &hyperv1.NodePool{ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "clusters", Labels: map[string]string{"preserved": "value"}}}
			builder := fake.NewClientBuilder().WithScheme(hyperapi.Scheme)
			if testCase.exists {
				builder = builder.WithObjects(pool.DeepCopy())
			}
			client := builder.Build()
			err := nodePoolUpdateObject(t.Context(), client, pool, func(current *hyperv1.NodePool) { current.Spec.Release.Image = "release:latest" })
			if !testCase.exists {
				if !apierrors.IsNotFound(err) {
					t.Fatalf("expected not-found, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			current := &hyperv1.NodePool{}
			if err := client.Get(t.Context(), crclient.ObjectKeyFromObject(pool), current); err != nil {
				t.Fatal(err)
			}
			if current.Spec.Release.Image != "release:latest" || current.Labels["preserved"] != "value" {
				t.Fatalf("mutation or preservation failed: %+v", current)
			}
		})
	}
}

func TestAwsNodeUIDs(t *testing.T) {
	nodes := []corev1.Node{{ObjectMeta: metav1.ObjectMeta{UID: "first"}}, {ObjectMeta: metav1.ObjectMeta{UID: "second"}}}
	actual := awsNodeUIDs(nodes)
	if len(actual) != 2 || actual[0] != types.UID("first") || actual[1] != types.UID("second") {
		t.Fatalf("unexpected identities: %v", actual)
	}
	if len(awsNodeUIDs(nil)) != 0 {
		t.Fatal("empty input should produce no identities")
	}
}

func TestAwsLicensedAMIFromMessage(t *testing.T) {
	for _, testCase := range []struct{ name, message, want string }{
		{name: "When AMI is modern, it should extract all seventeen hex digits", message: `Bootstrap Windows AMI is "ami-0123456789abcdef0"`, want: "ami-0123456789abcdef0"},
		{name: "When AMI is legacy, it should extract eight hex digits", message: "ami-12345678", want: "ami-12345678"},
		{name: "When message is empty, it should not fabricate an AMI"},
		{name: "When AMI is truncated, it should reject it", message: "ami-123456789"},
		{name: "When AMI is overlong, it should reject it", message: "ami-0123456789abcdef01"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if actual := awsLicensedAMIFromMessage(testCase.message); actual != testCase.want {
				t.Fatalf("awsLicensedAMIFromMessage() = %q, want %q", actual, testCase.want)
			}
		})
	}
}

func TestAwsNodeHealthy(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		mutate    func(*corev1.Node)
		wantError bool
	}{
		{name: "When node is Ready without pressure, it should accept it"},
		{name: "When Ready is absent, it should reject it", mutate: func(node *corev1.Node) { node.Status.Conditions = node.Status.Conditions[1:] }, wantError: true},
		{name: "When pressure is unknown, it should reject it", mutate: func(node *corev1.Node) { node.Status.Conditions[1].Status = corev1.ConditionUnknown }, wantError: true},
		{name: "When node is not Ready, it should reject it", mutate: func(node *corev1.Node) { node.Status.Conditions[0].Status = corev1.ConditionFalse }, wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			node := &corev1.Node{Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
				{Type: corev1.NodeMemoryPressure, Status: corev1.ConditionFalse},
				{Type: corev1.NodeDiskPressure, Status: corev1.ConditionFalse},
				{Type: corev1.NodePIDPressure, Status: corev1.ConditionFalse},
			}}}
			if testCase.mutate != nil {
				testCase.mutate(node)
			}
			if err := awsNodeHealthy(node); (err != nil) != testCase.wantError {
				t.Fatalf("awsNodeHealthy() = %v, want error %t", err, testCase.wantError)
			}
		})
	}
}

func TestAwsNodeMetadataMatches(t *testing.T) {
	pool := &hyperv1.NodePool{Spec: hyperv1.NodePoolSpec{
		NodeLabels: map[string]string{"empty-label": ""},
		Taints:     []hyperv1.Taint{{Key: "test", Value: "value", Effect: corev1.TaintEffectPreferNoSchedule}},
	}}
	for _, testCase := range []struct {
		name      string
		mutate    func(*corev1.Node)
		wantError bool
	}{
		{name: "When configured labels and taints are present, it should accept the node"},
		{name: "When empty-valued label is missing, it should reject the node", mutate: func(node *corev1.Node) { node.Labels = nil }, wantError: true},
		{name: "When taint effect differs, it should reject the node", mutate: func(node *corev1.Node) { node.Spec.Taints[0].Effect = corev1.TaintEffectNoSchedule }, wantError: true},
		{name: "When taint is absent, it should reject the node", mutate: func(node *corev1.Node) { node.Spec.Taints = nil }, wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			node := &corev1.Node{}
			node.Labels = map[string]string{"empty-label": ""}
			node.Spec.Taints = []corev1.Taint{{Key: "test", Value: "value", Effect: corev1.TaintEffectPreferNoSchedule}}
			if testCase.mutate != nil {
				testCase.mutate(node)
			}
			if err := awsNodeMetadataMatches(node, pool); (err != nil) != testCase.wantError {
				t.Fatalf("awsNodeMetadataMatches() = %v, want error %t", err, testCase.wantError)
			}
		})
	}
}

func TestAwsReleaseSkewMatches(t *testing.T) {
	for _, testCase := range []struct {
		name, controlPlane, pool string
		distance                 int
		wantError                bool
	}{
		{name: "When same-major release is N1, it should accept it", controlPlane: "4.23.0", pool: "4.22.0", distance: 1},
		{name: "When same-major release is N2, it should accept it", controlPlane: "4.23.0", pool: "4.21.0", distance: 2},
		{name: "When latest is supplied as N1, it should reject it", controlPlane: "4.23.0", pool: "4.23.0", distance: 1, wantError: true},
		{name: "When latest is supplied as N2, it should reject it", controlPlane: "4.23.0", pool: "4.23.0", distance: 2, wantError: true},
		{name: "When same-major releases are N3, it should accept them", controlPlane: "4.23.0", pool: "4.20.0", distance: 3},
		{name: "When same-major releases are N4, it should identify them", controlPlane: "4.23.0", pool: "4.19.0", distance: 4},
		{name: "When dual-version releases cross major at N3, it should accept them", controlPlane: "5.0.0", pool: "4.20.0", distance: 3},
		{name: "When next-major releases cross major at N3, it should accept them", controlPlane: "5.1.0", pool: "4.21.0", distance: 3},
		{name: "When image is not actually N3, it should reject the input", controlPlane: "5.0.0", pool: "4.21.0", distance: 3, wantError: true},
		{name: "When release is unknown, it should reject the input", controlPlane: "6.0.0", pool: "4.21.0", distance: 3, wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			err := awsReleaseSkewMatches(semver.MustParse(testCase.controlPlane), semver.MustParse(testCase.pool), testCase.distance)
			if (err != nil) != testCase.wantError {
				t.Fatalf("awsReleaseSkewMatches() = %v, want error %t", err, testCase.wantError)
			}
		})
	}
}

func TestNodePoolProvisioningMessageState(t *testing.T) {
	for _, testCase := range []struct {
		name               string
		status             corev1.ConditionStatus
		message            string
		observed, complete bool
	}{
		{name: "When condition is absent, it should continue observation"},
		{name: "When aggregated False is observed, it should record the diagnostic and finish", status: corev1.ConditionFalse, message: "2 machines are not ready", observed: true, complete: true},
		{name: "When readiness is already True, it should finish without claiming False was observed", status: corev1.ConditionTrue, complete: true},
		{name: "When False lacks machine-level detail, it should keep observing", status: corev1.ConditionFalse, message: "No Machines are created"},
		{name: "When another condition state is aggregated, it should not count it as unready", status: corev1.ConditionFalse, message: "machines are not healthy"},
		{name: "When condition is Unknown, it should keep observing", status: corev1.ConditionUnknown, message: "machines are not ready"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			pool := &hyperv1.NodePool{}
			if testCase.status != "" {
				pool.Status.Conditions = []hyperv1.NodePoolCondition{{Type: hyperv1.NodePoolAllMachinesReadyConditionType, Status: testCase.status, Message: testCase.message}}
			}
			observed, complete := nodePoolProvisioningMessageState(pool, hyperv1.NodePoolAllMachinesReadyConditionType, "ready")
			if observed != testCase.observed || complete != testCase.complete {
				t.Fatalf("provisioning message state = (%t, %t); want (%t, %t)", observed, complete, testCase.observed, testCase.complete)
			}
		})
	}
}
