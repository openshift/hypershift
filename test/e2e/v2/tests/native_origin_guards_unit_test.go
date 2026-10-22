//go:build e2ev2

package tests

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	configv1 "github.com/openshift/api/config/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/blang/semver"
	"github.com/onsi/ginkgo/v2/types"
	"github.com/onsi/gomega"
)

func TestNativeOriginAtLeast(t *testing.T) {
	for _, minimum := range []string{"4.15.0", "4.16.0", "4.17.0", "4.18.0", "4.19.0", "4.20.0", "4.22.0", "4.23.0"} {
		t.Run(fmt.Sprintf("When the feature requires %s, it should use the originating release", minimum), func(t *testing.T) {
			nativeOriginGuardCases(t, semver.MustParse(minimum), func(tc *internal.TestContext) (bool, error) {
				return nativeOriginAtLeast(tc, semver.MustParse(minimum))
			})
		})
	}
}

func TestNodePoolAggregationApplicable(t *testing.T) {
	nativeOriginGuardCases(t, semver.MustParse("4.23.0"), nodePoolAggregationApplicable)
}

func TestNodePoolRuntimeApplicable(t *testing.T) {
	nativeOriginGuardCases(t, semver.MustParse("4.18.0"), nodePoolRuntimeApplicable)
}

func TestAutoscalerScaleDownConfigurationApplicable(t *testing.T) {
	nativeOriginGuardCases(t, semver.MustParse("4.18.0"), autoscalerScaleDownConfigurationApplicable)
}

func TestNodePoolPerformanceProfileStatusApplicable(t *testing.T) {
	nativeOriginGuardCases(t, semver.MustParse("4.17.0"), nodePoolPerformanceProfileStatusApplicable)
}

func TestSkipIfNativeOriginBelow(t *testing.T) {
	originalDefault := gomega.Default
	gomega.Default = gomega.NewWithT(t)
	t.Cleanup(func() { gomega.Default = originalDefault })
	const childCaseEnv = "HYPERSHIFT_NATIVE_ORIGIN_GUARD_UNIT_CASE"
	for _, testCase := range []struct {
		name, origin, observed, outcome string
	}{
		{name: "When origin is below the boundary despite newer history, it should skip", origin: "4.18.0", observed: "4.23.0", outcome: "guard skipped"},
		{name: "When origin reaches the boundary despite older history, it should execute", origin: "4.19.0", observed: "4.18.0", outcome: "guard executed"},
		{name: "When configured origin resolution fails, it should fail instead of skipping", origin: "unresolved", observed: "4.23.0", outcome: "guard failed: origin metadata unavailable"},
		{name: "When no release input exists and standalone history reaches the boundary, it should execute", observed: "4.19.0", outcome: "guard executed"},
		{name: "When no release input exists and standalone history is older, it should skip", observed: "4.18.0", outcome: "guard skipped"},
	} {
		if os.Getenv(childCaseEnv) != "" {
			if os.Getenv(childCaseEnv) != testCase.name {
				continue
			}
			for _, name := range []string{"E2E_PREVIOUS_RELEASE_IMAGE", "E2E_LATEST_RELEASE_IMAGE", "E2E_CONDITION_DIAGNOSTIC_RELEASE_IMAGE", "E2E_CONDITION_DIAGNOSTIC_UPGRADE_TARGET"} {
				t.Setenv(name, "")
			}
			scheme := runtime.NewScheme()
			if err := hyperv1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			hc := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Name: "cluster", Namespace: "clusters"}, Status: hyperv1.HostedClusterStatus{Version: &hyperv1.ClusterVersionStatus{History: []configv1.UpdateHistory{{Version: testCase.observed}}}}}
			tc := &internal.TestContext{
				Context: t.Context(), MgmtClient: fake.NewClientBuilder().WithScheme(scheme).WithObjects(hc).Build(), ClusterName: hc.Name, ClusterNamespace: hc.Namespace,
				ConditionDiagnosticReleaseVersions: map[string]semver.Version{}, ConditionDiagnosticReleaseErrors: map[string]error{},
			}
			if testCase.origin != "" {
				t.Setenv("E2E_PREVIOUS_RELEASE_IMAGE", "previous")
				if testCase.origin == "unresolved" {
					tc.ConditionDiagnosticReleaseErrors["previous"] = errors.New("origin metadata unavailable")
				} else {
					tc.ConditionDiagnosticReleaseVersions["previous"] = semver.MustParse(testCase.origin)
				}
			}
			resetFailureConditionPhase(tc)
			defer func() {
				if recovered := recover(); recovered != nil {
					ginkgoError, ok := recovered.(types.GinkgoError)
					var expectedError types.GinkgoError
					expectedOK := errors.As(types.GinkgoErrors.UncaughtGinkgoPanic(types.CodeLocation{}), &expectedError)
					if !ok || !expectedOK || ginkgoError.Heading != expectedError.Heading {
						t.Fatalf("unexpected guard panic: %v", recovered)
					}
					fmt.Println("guard skipped")
				}
			}()
			err := gomega.InterceptGomegaFailure(func() { skipIfNativeOriginBelow(tc, semver.MustParse("4.19.0")) })
			if err != nil {
				if !strings.Contains(err.Error(), "origin metadata unavailable") {
					t.Fatalf("unexpected guard assertion: %v", err)
				}
				fmt.Println("guard failed: origin metadata unavailable")
				return
			}
			fmt.Println("guard executed")
			return
		}
		t.Run(testCase.name, func(t *testing.T) {
			command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestSkipIfNativeOriginBelow$", "-test.v")
			command.Env = append(os.Environ(), childCaseEnv+"="+testCase.name)
			output, err := command.CombinedOutput()
			if err != nil || !strings.Contains(string(output), testCase.outcome) {
				t.Fatalf("guard process error=%v; want %q in output:\n%s", err, testCase.outcome, output)
			}
		})
	}
}

func nativeOriginGuardCases(t *testing.T, minimum semver.Version, applicable func(*internal.TestContext) (bool, error)) {
	t.Helper()
	below := minimum
	below.Minor--
	for _, testCase := range []struct {
		name       string
		configured bool
		origin     string
		observed   string
		target     bool
		override   bool
		failure    bool
		want       bool
		wantError  string
	}{
		{name: "When origin is below the boundary and history is newer, it should omit only unsupported assertions", configured: true, origin: below.String(), observed: minimum.String()},
		{name: "When origin is exactly the boundary and history is older, it should require assertions", configured: true, origin: minimum.String(), observed: below.String(), want: true},
		{name: "When origin is applicable and history is malformed, it should require assertions", configured: true, origin: minimum.String(), observed: "malformed", want: true},
		{name: "When target mode retains an older origin, it should not mistake the desired target for authority", configured: true, origin: below.String(), observed: minimum.String(), target: true},
		{name: "When the latest phase explicitly latches the target, it should use the independent target metadata", configured: true, origin: below.String(), observed: below.String(), target: true, override: true, want: true},
		{name: "When configured resolution fails despite newer history, it should propagate the resolver error", configured: true, observed: minimum.String(), failure: true, wantError: "origin metadata unavailable"},
		{name: "When configured metadata is absent despite newer history, it should reject the unresolved origin", configured: true, observed: minimum.String(), wantError: "lacks resolved independent suite release"},
		{name: "When standalone history is below the boundary, it should preserve compatibility", observed: below.String()},
		{name: "When standalone history is at the boundary, it should retain assertions", observed: minimum.String(), want: true},
		{name: "When standalone history is malformed, it should propagate parsing failure", observed: "malformed", wantError: "error parsing version"},
		{name: "When standalone history is absent, it should propagate missing history", wantError: "no version in status history"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			for _, name := range []string{"E2E_PREVIOUS_RELEASE_IMAGE", "E2E_LATEST_RELEASE_IMAGE", "E2E_CONDITION_DIAGNOSTIC_RELEASE_IMAGE", "E2E_CONDITION_DIAGNOSTIC_UPGRADE_TARGET"} {
				t.Setenv(name, "")
			}
			scheme := runtime.NewScheme()
			if err := hyperv1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			hc := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Name: "cluster", Namespace: "clusters"}}
			if testCase.observed != "" {
				hc.Status.Version = &hyperv1.ClusterVersionStatus{History: []configv1.UpdateHistory{{Version: testCase.observed}}}
			}
			tc := &internal.TestContext{
				Context: t.Context(), MgmtClient: fake.NewClientBuilder().WithScheme(scheme).WithObjects(hc).Build(), ClusterName: hc.Name, ClusterNamespace: hc.Namespace,
				ConditionDiagnosticReleaseVersions: map[string]semver.Version{}, ConditionDiagnosticReleaseErrors: map[string]error{},
			}
			image := "quay.io/openshift-release-dev/ocp-release:4.23.0-x86_64"
			if testCase.configured {
				t.Setenv("E2E_PREVIOUS_RELEASE_IMAGE", image)
				if testCase.target {
					targetImage := "quay.io/openshift-release-dev/ocp-release:4.24.0-x86_64"
					t.Setenv("E2E_LATEST_RELEASE_IMAGE", targetImage)
					t.Setenv("E2E_CONDITION_DIAGNOSTIC_UPGRADE_TARGET", targetImage)
					tc.ConditionDiagnosticReleaseVersions[targetImage] = minimum
					if testCase.override {
						t.Setenv("E2E_CONDITION_DIAGNOSTIC_RELEASE_IMAGE", targetImage)
					}
				}
				resetFailureConditionPhase(tc)
				if testCase.origin != "" {
					tc.ConditionDiagnosticReleaseVersions[image] = semver.MustParse(testCase.origin)
				}
			}
			cause := errors.New("origin metadata unavailable")
			if testCase.failure {
				tc.ConditionDiagnosticReleaseErrors[image] = cause
			}
			got, err := applicable(tc)
			if got != testCase.want || (err != nil) != (testCase.wantError != "") {
				t.Fatalf("applicable=%v error=%v; want %v error containing %q", got, err, testCase.want, testCase.wantError)
			}
			if testCase.wantError != "" && !strings.Contains(err.Error(), testCase.wantError) {
				t.Fatalf("error=%v; want %q", err, testCase.wantError)
			}
			if testCase.failure && !errors.Is(err, cause) {
				t.Fatalf("resolver error identity lost: %v", err)
			}
		})
	}
}
