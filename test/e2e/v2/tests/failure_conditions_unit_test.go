//go:build e2ev2

package tests

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/pkg/manifests"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	configv1 "github.com/openshift/api/config/v1"
	imagev1 "github.com/openshift/api/image/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/blang/semver"
	"github.com/onsi/ginkgo/v2/types"
)

func TestReportFailureConditions(t *testing.T) {
	for _, testCase := range []struct {
		name                string
		report              types.SpecReport
		unresolved          bool
		wantRead, wantError bool
	}{
		{name: "When a passed spec has a client, it should not fetch failure state", report: types.SpecReport{State: types.SpecStatePassed}},
		{name: "When a genuine skip has a client, it should not fetch failure state", report: types.SpecReport{State: types.SpecStateSkipped}},
		{name: "When a failed spec has independent gates, it should fetch without changing its result", report: types.SpecReport{State: types.SpecStateFailed}, wantRead: true},
		{name: "When an informing failure has independent gates, it should fetch without changing its skip result", report: types.SpecReport{State: types.SpecStateSkipped, ContainerHierarchyLabels: [][]string{{internal.InformingLabel}}, Failure: types.Failure{Message: "informing test failure: original assertion"}}, wantRead: true},
		{name: "When a diagnostic lacks independent input, it should preserve the failed result and qualify its error", report: types.SpecReport{State: types.SpecStateFailed}, unresolved: true, wantError: true},
		{name: "When an informing diagnostic lacks independent input, it should preserve the original skip and failure message", report: types.SpecReport{State: types.SpecStateSkipped, ContainerHierarchyLabels: [][]string{{internal.InformingLabel}}, Failure: types.Failure{Message: "informing test failure: original assertion"}}, unresolved: true, wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			hc := failureConditionFixture(t, "4.22.0", false)
			scheme := runtime.NewScheme()
			if err := hyperv1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			reads := 0
			client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(hc).WithInterceptorFuncs(interceptor.Funcs{Get: func(ctx context.Context, client crclient.WithWatch, key crclient.ObjectKey, object crclient.Object, opts ...crclient.GetOption) error {
				reads++
				return client.Get(ctx, key, object, opts...)
			}}).Build()
			tc := &internal.TestContext{Context: t.Context(), MgmtClient: client, ClusterName: hc.Name, ClusterNamespace: hc.Namespace, ExpectedNodePools: map[string]int32{}}
			if !testCase.unresolved {
				setFailureConditionTestVersion(tc, "4.22.0")
			}
			original := testCase.report
			err := reportFailureConditions(tc, testCase.report)
			if (err != nil) != testCase.wantError || (reads > 0) != testCase.wantRead {
				t.Fatalf("error=%v management reads=%d", err, reads)
			}
			if !reflect.DeepEqual(testCase.report, original) {
				t.Fatal("diagnostic changed original report")
			}
		})
	}
	for _, testCase := range []struct {
		name      string
		report    types.SpecReport
		wantError bool
	}{
		{name: "When a spec passed, it should not collect diagnostics", report: types.SpecReport{State: types.SpecStatePassed}},
		{name: "When a spec genuinely skipped, it should not collect diagnostics", report: types.SpecReport{State: types.SpecStateSkipped}},
		{name: "When a spec failed without context, it should return a diagnostic error", report: types.SpecReport{State: types.SpecStateFailed}, wantError: true},
		{name: "When an informing assertion became a skip, it should still collect diagnostics", report: types.SpecReport{State: types.SpecStateSkipped, ContainerHierarchyLabels: [][]string{{internal.InformingLabel}}, Failure: types.Failure{Message: "informing test failure: original assertion"}}, wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			original := testCase.report
			if err := reportFailureConditions(nil, testCase.report); (err != nil) != testCase.wantError {
				t.Fatalf("diagnostic error = %v, want error %v", err, testCase.wantError)
			}
			if !reflect.DeepEqual(testCase.report, original) {
				t.Fatal("diagnostic changed original report")
			}
		})
	}
	t.Run("When the failed spec context is canceled, it should collect using a bounded detached context", func(t *testing.T) {
		hc := failureConditionFixture(t, "4.22.0", false)
		scheme := runtime.NewScheme()
		if err := hyperv1.AddToScheme(scheme); err != nil {
			t.Fatal(err)
		}
		observed := false
		client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(hc).WithInterceptorFuncs(interceptor.Funcs{Get: func(ctx context.Context, client crclient.WithWatch, key crclient.ObjectKey, object crclient.Object, opts ...crclient.GetOption) error {
			deadline, ok := ctx.Deadline()
			if ctx.Err() != nil || !ok || time.Until(deadline) > 2*time.Second {
				t.Fatal("diagnostic did not detach and bound its context")
			}
			observed = true
			return client.Get(ctx, key, object, opts...)
		}}).Build()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		tc := &internal.TestContext{Context: ctx, MgmtClient: client, ClusterName: hc.Name, ClusterNamespace: hc.Namespace, ExpectedNodePools: map[string]int32{}}
		setFailureConditionTestVersion(tc, "4.22.0")
		if err := reportFailureConditions(tc, types.SpecReport{State: types.SpecStateFailed}); err != nil {
			t.Fatal(err)
		}
		if !observed {
			t.Fatal("diagnostic did not fetch cluster")
		}
	})
}

func TestFailureConditionVersion(t *testing.T) {
	t.Setenv("E2E_CONDITION_DIAGNOSTIC_RELEASE_IMAGE", "")
	t.Run("When ordinary CP Main selects latest and a later spec resets, it should use only the independent selected phase", func(t *testing.T) {
		t.Setenv("E2E_PREVIOUS_RELEASE_IMAGE", "previous")
		t.Setenv("E2E_LATEST_RELEASE_IMAGE", "latest")
		tc := &internal.TestContext{ConditionDiagnosticReleaseImage: failureConditionInitialReleaseImage(), ConditionDiagnosticReleaseVersions: map[string]semver.Version{"previous": semver.MustParse("4.21.0"), "latest": semver.MustParse("4.22.0")}}
		version, err := failureConditionVersion(tc)
		if err != nil || !version.Equals(semver.MustParse("4.21.0")) {
			t.Fatalf("initial phase version=%s error=%v", version, err)
		}
		tc.ConditionDiagnosticUpgradeTarget = internal.GetEnvVarValue("E2E_LATEST_RELEASE_IMAGE")
		tc.ConditionDiagnosticReleaseImage = tc.ConditionDiagnosticUpgradeTarget
		version, err = failureConditionVersion(tc)
		if err != nil || !version.Equals(semver.MustParse("4.22.0")) {
			t.Fatalf("CP Main phase version=%s error=%v", version, err)
		}
		tc.ConditionDiagnosticUpgradeTarget = ""
		tc.ConditionDiagnosticReleaseImage = failureConditionInitialReleaseImage()
		version, err = failureConditionVersion(tc)
		if err != nil || !version.Equals(semver.MustParse("4.21.0")) {
			t.Fatalf("reset phase version=%s error=%v", version, err)
		}
	})
	for _, testCase := range []struct {
		name, image string
		resolved    bool
		wantError   bool
	}{
		{name: "When independent input is absent, it should qualify unavailable gates", wantError: true},
		{name: "When independent resolution failed, it should retain the resolver error", image: "suite", wantError: true},
		{name: "When independent metadata is cached, it should use its version without observed history", image: "suite", resolved: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			tc := &internal.TestContext{ConditionDiagnosticReleaseImage: testCase.image, ConditionDiagnosticReleaseErrors: map[string]error{"suite": errors.New("registry unavailable")}}
			if testCase.resolved {
				setFailureConditionTestVersion(tc, "4.22.0")
			}
			version, err := failureConditionVersion(tc)
			if (err != nil) != testCase.wantError {
				t.Fatalf("version=%s error=%v", version, err)
			}
			if !testCase.wantError && !version.Equals(semver.MustParse("4.22.0")) {
				t.Fatalf("unexpected normalized version %s", version)
			}
			if testCase.image != "" && !testCase.resolved && !strings.Contains(err.Error(), "registry unavailable") {
				t.Fatalf("resolver error lost: %v", err)
			}
		})
	}
}

func TestFailureConditionInitialReleaseImage(t *testing.T) {
	t.Setenv("E2E_CONDITION_DIAGNOSTIC_RELEASE_IMAGE", "")
	for _, testCase := range []struct {
		name, previous, latest, expected string
	}{
		{name: "When previous is configured, it should retain the independent initial previous gate", previous: "previous", latest: "latest", expected: "previous"},
		{name: "When previous is absent, it should fall back to independent latest input", latest: "latest", expected: "latest"},
		{name: "When both inputs are absent, it should leave the diagnostic unqualified rather than guess"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("E2E_PREVIOUS_RELEASE_IMAGE", testCase.previous)
			t.Setenv("E2E_LATEST_RELEASE_IMAGE", testCase.latest)
			if actual := failureConditionInitialReleaseImage(); actual != testCase.expected {
				t.Fatalf("initial release=%s, expected %s", actual, testCase.expected)
			}
		})
	}
}

func TestFailureConditionMismatch(t *testing.T) {
	for _, version := range []string{"4.14.0", "4.20.0", "4.21.0", "4.22.0", "4.23.0"} {
		t.Run("When observed version is "+version+", it should apply the matching condition gates", func(t *testing.T) {
			hc := failureConditionFixture(t, version, false)
			if err := failureConditionMismatch(hc, false, semver.MustParse(version), false, true); err != nil {
				t.Fatal(err)
			}
			hc.Status.Conditions = nil
			if err := failureConditionMismatch(hc, false, semver.MustParse(version), false, true); err == nil || !strings.Contains(err.Error(), "absent, expected") {
				t.Fatalf("missing-condition error=%v", err)
			}
		})
	}
	t.Run("When zero workers are configured but worker conditions are healthy, it should report wrong polarity", func(t *testing.T) {
		hc := failureConditionFixture(t, "4.23.0", true)
		if err := failureConditionMismatch(hc, false, semver.MustParse("4.23.0"), false, true); err == nil || !strings.Contains(err.Error(), "expected False") {
			t.Fatalf("worker-polarity error=%v", err)
		}
	})
	t.Run("When upgrade-only conditions are absent, it should retain the baseline upgrade masks", func(t *testing.T) {
		hc := failureConditionFixture(t, "4.23.0", false)
		for index := range hc.Status.Conditions {
			if hc.Status.Conditions[index].Type == string(hyperv1.ControlPlaneConnectionAvailable) || hc.Status.Conditions[index].Type == string(hyperv1.ConfigOperatorReconciliationSucceeded) {
				hc.Status.Conditions[index].Status = metav1.ConditionFalse
			}
		}
		if err := failureConditionMismatch(hc, false, semver.MustParse("4.23.0"), true, true); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("When CPV has a desired image and Completed history, it should not require registry identity resolution", func(t *testing.T) {
		hc := failureConditionFixture(t, "4.22.0", false)
		hc.Status.ControlPlaneVersion.History[0].Image = "different-history-reference"
		if err := failureConditionMismatch(hc, false, semver.MustParse("4.22.0"), false, true); err != nil {
			t.Fatal(err)
		}
		hc.Status.ControlPlaneVersion.History = nil
		if err := failureConditionMismatch(hc, false, semver.MustParse("4.22.0"), false, true); err == nil {
			t.Fatal("missing CPV history passed")
		}
		if err := failureConditionMismatch(hc, false, semver.MustParse("4.22.0"), true, false); err != nil {
			t.Fatalf("premature CPV enforcement: %v", err)
		}
	})
}

func TestFailureControlPlaneVersionReady(t *testing.T) {
	for _, testCase := range []struct {
		name, target, current string
		cpc                   bool
		status                metav1.ConditionStatus
		apiError              bool
		wantReady, wantError  bool
	}{
		{name: "When no upgrade is active, it should enforce CPV immediately", wantReady: true},
		{name: "When target is not requested yet, it should defer CPV", target: "target", current: "previous"},
		{name: "When CPO is missing, it should defer CPV", target: "target", current: "target"},
		{name: "When CPO has not rolled, it should defer CPV", target: "target", current: "target", cpc: true, status: metav1.ConditionFalse},
		{name: "When target CPO rolled, it should enforce CPV", target: "target", current: "target", cpc: true, status: metav1.ConditionTrue, wantReady: true},
		{name: "When CPO observation fails, it should preserve the API error", target: "target", current: "target", apiError: true, wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := hyperv1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			hc := failureConditionFixture(t, "4.22.0", false)
			hc.Spec.Release.Image = testCase.current
			builder := fake.NewClientBuilder().WithScheme(scheme)
			if testCase.cpc {
				builder.WithObjects(&hyperv1.ControlPlaneComponent{ObjectMeta: metav1.ObjectMeta{Name: "control-plane-operator", Namespace: manifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name)}, Status: hyperv1.ControlPlaneComponentStatus{Conditions: []metav1.Condition{{Type: string(hyperv1.ControlPlaneComponentRolloutComplete), Status: testCase.status}}}})
			}
			if testCase.apiError {
				builder.WithInterceptorFuncs(interceptor.Funcs{Get: func(context.Context, crclient.WithWatch, crclient.ObjectKey, crclient.Object, ...crclient.GetOption) error {
					return errors.New("CPO observation unavailable")
				}})
			}
			tc := &internal.TestContext{MgmtClient: builder.Build(), ConditionDiagnosticUpgradeTarget: testCase.target}
			ready, err := failureControlPlaneVersionReady(t.Context(), tc, hc)
			if ready != testCase.wantReady || (err != nil) != testCase.wantError {
				t.Fatalf("ready=%v error=%v", ready, err)
			}
		})
	}
}

func TestReconcileFailureConditionsIndependentControlPlaneVersion(t *testing.T) {
	for _, version := range []string{"4.21.0", "4.22.0"} {
		t.Run("When independent "+version+" gates have no CPV, it should apply the independent CPV introduction boundary", func(t *testing.T) {
			hc := failureConditionFixture(t, version, false)
			hc.Status.Version = nil
			hc.Status.ControlPlaneVersion = hyperv1.ControlPlaneVersionStatus{}
			scheme := runtime.NewScheme()
			if err := hyperv1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(hc).Build()
			tc := &internal.TestContext{MgmtClient: client, ClusterName: hc.Name, ClusterNamespace: hc.Namespace, ExpectedNodePools: map[string]int32{}}
			setFailureConditionTestVersion(tc, version)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
			defer cancel()
			err := reconcileFailureConditions(ctx, tc)
			if version == "4.21.0" && err != nil {
				t.Fatal(err)
			}
			if version == "4.22.0" && (!errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "controlPlaneVersion")) {
				t.Fatalf("independent CPV gate lost: %v", err)
			}
		})
	}
}

func TestReconcileFailureConditionsIndependentRelease(t *testing.T) {
	for _, version := range []string{"4.21.0", "4.22.0", "4.23.0"} {
		for _, history := range []string{"", "malformed", "4.14.0"} {
			for _, workers := range []bool{false, true} {
				name := "When independent " + version + " gates have history " + history + " and zero configured workers, it should reconcile without trusting history"
				if workers {
					name = "When independent " + version + " gates have history " + history + " and configured workers, it should reconcile without trusting history"
				}
				t.Run(name, func(t *testing.T) {
					hc := failureConditionFixture(t, version, workers)
					hc.Status.Version = nil
					if history != "" {
						hc.Status.Version = &hyperv1.ClusterVersionStatus{History: []configv1.UpdateHistory{{Version: history}}}
					}
					scheme := runtime.NewScheme()
					if err := hyperv1.AddToScheme(scheme); err != nil {
						t.Fatal(err)
					}
					client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(hc).Build()
					tc := &internal.TestContext{MgmtClient: client, ClusterName: hc.Name, ClusterNamespace: hc.Namespace, ExpectedNodePools: map[string]int32{}}
					if workers {
						tc.ExpectedNodePools["configured-pool"] = 1
					}
					setFailureConditionTestVersion(tc, version)
					if err := reconcileFailureConditions(t.Context(), tc); err != nil {
						t.Fatal(err)
					}
				})
			}
		}
	}
}

func TestReconcileFailureConditionsIndependentConditionBoundaries(t *testing.T) {
	for _, testCase := range []struct {
		version string
		missing hyperv1.ConditionType
	}{
		{version: "4.21.0", missing: hyperv1.DataPlaneConnectionAvailable},
		{version: "4.22.0", missing: hyperv1.ControlPlaneConnectionAvailable},
		{version: "4.23.0", missing: hyperv1.ConfigOperatorReconciliationSucceeded},
	} {
		t.Run("When independent "+testCase.version+" requires "+string(testCase.missing)+", it should enforce it despite older observed history", func(t *testing.T) {
			hc := failureConditionFixture(t, testCase.version, true)
			hc.Status.Version.History[0].Version = "4.14.0"
			for index := range hc.Status.Conditions {
				if hc.Status.Conditions[index].Type == string(testCase.missing) {
					hc.Status.Conditions[index].Status = metav1.ConditionFalse
				}
			}
			scheme := runtime.NewScheme()
			if err := hyperv1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(hc).Build()
			tc := &internal.TestContext{MgmtClient: client, ClusterName: hc.Name, ClusterNamespace: hc.Namespace, ExpectedNodePools: map[string]int32{"configured-pool": 1}}
			setFailureConditionTestVersion(tc, testCase.version)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
			defer cancel()
			err := reconcileFailureConditions(ctx, tc)
			if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), string(testCase.missing)) {
				t.Fatalf("lost independent condition gate: %v", err)
			}
		})
	}
}

func TestReconcileFailureConditions(t *testing.T) {
	t.Run("When configured intent is missing, it should not guess worker polarity", func(t *testing.T) {
		if err := reconcileFailureConditions(t.Context(), &internal.TestContext{}); err == nil {
			t.Fatal("missing intent accepted")
		}
	})
	t.Run("When cluster observation fails, it should retain the last error with the timeout", func(t *testing.T) {
		scheme := runtime.NewScheme()
		if err := hyperv1.AddToScheme(scheme); err != nil {
			t.Fatal(err)
		}
		client := fake.NewClientBuilder().WithScheme(scheme).Build()
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
		defer cancel()
		tc := &internal.TestContext{MgmtClient: client, ClusterName: "cluster", ClusterNamespace: "clusters", ExpectedNodePools: map[string]int32{}}
		setFailureConditionTestVersion(tc, "4.22.0")
		err := reconcileFailureConditions(ctx, tc)
		if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "not found") {
			t.Fatalf("lost observation/timeout error: %v", err)
		}
	})
	t.Run("When the first observation fails, it should fetch fresh state on retry", func(t *testing.T) {
		scheme := runtime.NewScheme()
		if err := hyperv1.AddToScheme(scheme); err != nil {
			t.Fatal(err)
		}
		hc := failureConditionFixture(t, "4.22.0", false)
		reads := 0
		client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(hc).WithInterceptorFuncs(interceptor.Funcs{Get: func(ctx context.Context, client crclient.WithWatch, key crclient.ObjectKey, object crclient.Object, opts ...crclient.GetOption) error {
			reads++
			if reads == 1 {
				return errors.New("temporary API failure")
			}
			return client.Get(ctx, key, object, opts...)
		}}).Build()
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		tc := &internal.TestContext{MgmtClient: client, ClusterName: hc.Name, ClusterNamespace: hc.Namespace, ExpectedNodePools: map[string]int32{}}
		setFailureConditionTestVersion(tc, "4.22.0")
		if err := reconcileFailureConditions(ctx, tc); err != nil {
			t.Fatal(err)
		}
		if reads != 2 {
			t.Fatalf("expected two independent observations, got %d", reads)
		}
	})
	t.Run("When CPO readiness was observed, it should latch CPV enforcement across subsequent polls", func(t *testing.T) {
		scheme := runtime.NewScheme()
		if err := hyperv1.AddToScheme(scheme); err != nil {
			t.Fatal(err)
		}
		hc := failureConditionFixture(t, "4.22.0", false)
		hc.Spec.Release.Image = "target"
		hc.Status.ControlPlaneVersion.History = nil
		clusterReads, cpcReads := 0, 0
		client := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{Get: func(ctx context.Context, client crclient.WithWatch, key crclient.ObjectKey, object crclient.Object, opts ...crclient.GetOption) error {
			switch result := object.(type) {
			case *hyperv1.HostedCluster:
				clusterReads++
				hc.DeepCopyInto(result)
			case *hyperv1.ControlPlaneComponent:
				cpcReads++
				if cpcReads == 1 {
					result.Status.Conditions = []metav1.Condition{{Type: string(hyperv1.ControlPlaneComponentRolloutComplete), Status: metav1.ConditionTrue}}
				}
			default:
				t.Fatalf("unexpected API request for %T", object)
			}
			return nil
		}}).Build()
		ctx, cancel := context.WithTimeout(t.Context(), 250*time.Millisecond)
		defer cancel()
		tc := &internal.TestContext{MgmtClient: client, ClusterName: hc.Name, ClusterNamespace: hc.Namespace, ExpectedNodePools: map[string]int32{}, ConditionDiagnosticUpgradeTarget: "target"}
		setFailureConditionTestVersion(tc, "4.22.0")
		err := reconcileFailureConditions(ctx, tc)
		if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "controlPlaneVersion") || clusterReads < 2 || cpcReads != 1 {
			t.Fatalf("error=%v cluster reads=%d CPO reads=%d", err, clusterReads, cpcReads)
		}
	})
}

func TestReconcileFailureConditionsConfiguredTarget(t *testing.T) {
	for _, testCase := range []struct {
		name, target string
		rolled       bool
		wantError    bool
	}{
		{name: "When initial CP or AutoNode target is pending, it should use previous gates with temporary masks and defer CPV", target: "latest"},
		{name: "When configured target CPO has rolled, it should enforce previous-gated CPV despite contradictory HC history", target: "latest", rolled: true, wantError: true},
		{name: "When no target is configured, it should enforce steady-state conditions and CPV", wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("E2E_PREVIOUS_RELEASE_IMAGE", "previous")
			t.Setenv("E2E_LATEST_RELEASE_IMAGE", "latest")
			t.Setenv("E2E_CONDITION_DIAGNOSTIC_RELEASE_IMAGE", "")
			t.Setenv("E2E_CONDITION_DIAGNOSTIC_UPGRADE_TARGET", testCase.target)
			hc := failureConditionFixture(t, "4.22.0", false)
			hc.Status.Version.History[0].Version = "4.14.0"
			hc.Spec.Release.Image = "previous"
			hc.Status.ControlPlaneVersion = hyperv1.ControlPlaneVersionStatus{}
			for index := range hc.Status.Conditions {
				if hc.Status.Conditions[index].Type == string(hyperv1.ControlPlaneConnectionAvailable) || hc.Status.Conditions[index].Type == string(hyperv1.ConfigOperatorReconciliationSucceeded) {
					hc.Status.Conditions[index].Status = metav1.ConditionFalse
				}
			}
			scheme := runtime.NewScheme()
			if err := hyperv1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			builder := fake.NewClientBuilder().WithScheme(scheme)
			if testCase.rolled {
				hc.Spec.Release.Image = "latest"
				builder.WithObjects(&hyperv1.ControlPlaneComponent{ObjectMeta: metav1.ObjectMeta{Name: "control-plane-operator", Namespace: manifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name)}, Status: hyperv1.ControlPlaneComponentStatus{Conditions: []metav1.Condition{{Type: string(hyperv1.ControlPlaneComponentRolloutComplete), Status: metav1.ConditionTrue}}}})
			}
			client := builder.WithObjects(hc).Build()
			tc := &internal.TestContext{MgmtClient: client, ClusterName: hc.Name, ClusterNamespace: hc.Namespace, ExpectedNodePools: map[string]int32{}, ConditionDiagnosticReleaseVersions: map[string]semver.Version{"previous": semver.MustParse("4.22.0"), "latest": semver.MustParse("4.23.0")}}
			resetFailureConditionPhase(tc)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
			defer cancel()
			err := reconcileFailureConditions(ctx, tc)
			if (err != nil) != testCase.wantError || tc.ConditionDiagnosticReleaseImage != "previous" || tc.ConditionDiagnosticUpgradeTarget != testCase.target {
				t.Fatalf("independent target/release reconciliation error=%v selector=%s target=%s", err, tc.ConditionDiagnosticReleaseImage, tc.ConditionDiagnosticUpgradeTarget)
			}
			if testCase.wantError && !strings.Contains(err.Error(), "controlPlaneVersion") {
				t.Fatalf("CPV gate was not enforced: %v", err)
			}
		})
	}
}

func setFailureConditionTestVersion(tc *internal.TestContext, version string) {
	tc.ConditionDiagnosticReleaseImage = "suite"
	tc.ConditionDiagnosticReleaseVersions = map[string]semver.Version{"suite": semver.MustParse(version)}
}

type failureConditionReleaseProvider func(context.Context, string, []byte) (*releaseinfo.ReleaseImage, error)

func (provider failureConditionReleaseProvider) Lookup(ctx context.Context, image string, pullSecret []byte) (*releaseinfo.ReleaseImage, error) {
	return provider(ctx, image, pullSecret)
}

func TestPrepareFailureConditionVersions(t *testing.T) {
	for _, testCase := range []struct {
		name, previous, latest, selected, override, target string
		versions                                           map[string]string
		wantError                                          bool
	}{
		{name: "When previous and latest are configured, it should initially select previous and cache both", previous: "previous", latest: "latest", selected: "previous", versions: map[string]string{"previous": "4.21.9", "latest": "4.22.0-rc.1"}},
		{name: "When only latest is configured, it should use the independent latest metadata", latest: "latest", selected: "latest", versions: map[string]string{"latest": "4.23.1"}},
		{name: "When both inputs are absent, it should report unavailable independent gates", wantError: true},
		{name: "When the registry fails, it should retain a diagnostic error without deriving HC history", latest: "latest", selected: "latest", wantError: true},
		{name: "When payload metadata is malformed, it should reject tag-derived guesses", latest: "latest", selected: "latest", versions: map[string]string{"latest": "invalid"}, wantError: true},
		{name: "When a post-CP child carries independent latest and target inputs, it should prepare latest gates and retain the upgrade target", previous: "previous", latest: "latest", selected: "latest", override: "latest", target: "latest", versions: map[string]string{"previous": "4.21.0", "latest": "4.22.0"}},
		{name: "When an initial CP or AutoNode child carries target only, it should prepare previous gates with upgrade mode", previous: "previous", latest: "latest", selected: "previous", target: "latest", versions: map[string]string{"previous": "4.21.0", "latest": "4.22.0"}},
		{name: "When latest release override has no target, it should not manufacture upgrade mode", previous: "previous", latest: "latest", selected: "latest", override: "latest", versions: map[string]string{"previous": "4.21.0", "latest": "4.22.0"}},
		{name: "When the override is unsupported, it should nonassertingly reject it before registry work", previous: "previous", latest: "latest", selected: "stale", override: "stale", wantError: true},
		{name: "When target differs from latest, it should nonassertingly reject it before registry work", previous: "previous", latest: "latest", selected: "previous", target: "stale", wantError: true},
		{name: "When target has no configured latest, it should reject it without guessing metadata", previous: "previous", selected: "previous", target: "stale", wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("E2E_PREVIOUS_RELEASE_IMAGE", testCase.previous)
			t.Setenv("E2E_LATEST_RELEASE_IMAGE", testCase.latest)
			t.Setenv("E2E_CONDITION_DIAGNOSTIC_RELEASE_IMAGE", testCase.override)
			t.Setenv("E2E_CONDITION_DIAGNOSTIC_UPGRADE_TARGET", testCase.target)
			hc := failureConditionFixture(t, "4.14.0", false)
			hc.Spec.PullSecret.Name = "pull-secret"
			scheme := runtime.NewScheme()
			if err := hyperv1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			if err := corev1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: hc.Spec.PullSecret.Name, Namespace: hc.Namespace}, Data: map[string][]byte{corev1.DockerConfigJsonKey: []byte(`{"auths":{}}`)}}
			client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(hc, secret).Build()
			tc := &internal.TestContext{Context: t.Context(), MgmtClient: client, ClusterName: hc.Name, ClusterNamespace: hc.Namespace}
			lookups := 0
			provider := failureConditionReleaseProvider(func(ctx context.Context, image string, pullSecret []byte) (*releaseinfo.ReleaseImage, error) {
				lookups++
				if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 30*time.Second || len(pullSecret) == 0 {
					t.Fatal("independent resolver lacks bounded context or configured pull secret")
				}
				version, exists := testCase.versions[image]
				if !exists {
					return nil, errors.New("registry unavailable")
				}
				return &releaseinfo.ReleaseImage{ImageStream: &imagev1.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: version}}}, nil
			})
			err := prepareFailureConditionVersions(tc, provider)
			if (err != nil) != testCase.wantError || tc.ConditionDiagnosticReleaseImage != testCase.selected {
				t.Fatalf("error=%v selected=%s", err, tc.ConditionDiagnosticReleaseImage)
			}
			if (testCase.override == "stale" || testCase.target == "stale") && lookups != 0 {
				t.Fatal("invalid diagnostic input performed registry discovery")
			}
			if !testCase.wantError && tc.ConditionDiagnosticUpgradeTarget != testCase.target {
				t.Fatal("post-CP phase lost its supported upgrade masks and CPO gate")
			}
			if testCase.wantError {
				return
			}
			before := lookups
			if err := prepareFailureConditionVersions(tc, provider); err != nil || lookups != before {
				t.Fatalf("successful independent versions were not cached: lookups=%d error=%v", lookups, err)
			}
			for image, value := range testCase.versions {
				want := semver.MustParse(value)
				want.Patch, want.Pre, want.Build = 0, nil, nil
				if !tc.ConditionDiagnosticReleaseVersions[image].Equals(want) {
					t.Fatalf("independent %s gates=%s, wanted %s", image, tc.ConditionDiagnosticReleaseVersions[image], want)
				}
			}
		})
	}
}

func TestResetFailureConditionPhase(t *testing.T) {
	for _, testCase := range []struct {
		name, latest, override, selected, target, inputTarget string
		missingMetadata, wantError                            bool
	}{
		{name: "When an initial child has no override, it should reset to previous without a CP target", latest: "latest", selected: "previous"},
		{name: "When a proven later CP child carries latest and target, it should retain independent latest and CP upgrade masks", latest: "latest", override: "latest", inputTarget: "latest", selected: "latest", target: "latest"},
		{name: "When an initial CP or AutoNode child carries target only, it should retain previous gates and upgrade masks", latest: "latest", inputTarget: "latest", selected: "previous", target: "latest"},
		{name: "When latest override is present without target, it should select latest without upgrade masks", latest: "latest", override: "latest", selected: "latest"},
		{name: "When a later CP child lacks cached metadata, it should qualify failure instead of using observed history", latest: "latest", override: "latest", inputTarget: "latest", selected: "latest", target: "latest", missingMetadata: true, wantError: true},
		{name: "When the override names a different input, it should report an unsupported diagnostic phase", latest: "latest", override: "unsupported", selected: "unsupported", wantError: true},
		{name: "When latest input is absent, it should reject an inherited override", override: "latest", selected: "latest", wantError: true},
		{name: "When the target names a different input, it should report unsupported target without enabling upgrade mode", latest: "latest", inputTarget: "unsupported", selected: "previous", wantError: true},
		{name: "When latest is absent, it should reject inherited target without enabling upgrade mode", inputTarget: "latest", selected: "previous", wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("E2E_PREVIOUS_RELEASE_IMAGE", "previous")
			t.Setenv("E2E_LATEST_RELEASE_IMAGE", testCase.latest)
			t.Setenv("E2E_CONDITION_DIAGNOSTIC_RELEASE_IMAGE", testCase.override)
			t.Setenv("E2E_CONDITION_DIAGNOSTIC_UPGRADE_TARGET", testCase.inputTarget)
			tc := &internal.TestContext{ConditionDiagnosticUpgradeTarget: "stale", ConditionDiagnosticReleaseImage: "stale", ConditionDiagnosticReleaseVersions: map[string]semver.Version{"previous": semver.MustParse("4.21.0"), "latest": semver.MustParse("4.22.0")}}
			if testCase.missingMetadata {
				delete(tc.ConditionDiagnosticReleaseVersions, "latest")
			}
			resetFailureConditionPhase(tc)
			if tc.ConditionDiagnosticReleaseImage != testCase.selected || tc.ConditionDiagnosticUpgradeTarget != testCase.target {
				t.Fatalf("wrong phase selector=%s target=%s", tc.ConditionDiagnosticReleaseImage, tc.ConditionDiagnosticUpgradeTarget)
			}
			version, err := failureConditionVersion(tc)
			if (err != nil) != testCase.wantError {
				t.Fatalf("version=%s error=%v", version, err)
			}
			if !testCase.wantError {
				want := "4.21.0"
				if testCase.selected == "latest" {
					want = "4.22.0"
				}
				if !version.Equals(semver.MustParse(want)) {
					t.Fatalf("selected phase used %s, expected %s", version, want)
				}
			}
		})
	}
}

func TestValidateFailureConditionUpgradeTarget(t *testing.T) {
	for _, testCase := range []struct {
		name, latest, target string
		wantError            bool
	}{
		{name: "When target is absent, it should preserve standalone compatibility"},
		{name: "When target equals configured latest, it should accept upgrade mode", latest: "latest", target: "latest"},
		{name: "When target differs from latest, it should report unsupported input", latest: "latest", target: "previous", wantError: true},
		{name: "When latest is missing, it should reject a nonempty target", target: "latest", wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("E2E_LATEST_RELEASE_IMAGE", testCase.latest)
			t.Setenv("E2E_CONDITION_DIAGNOSTIC_UPGRADE_TARGET", testCase.target)
			if err := validateFailureConditionUpgradeTarget(); (err != nil) != testCase.wantError {
				t.Fatalf("target validation error=%v", err)
			}
		})
	}
}

func TestValidateFailureConditionReleaseOverride(t *testing.T) {
	for _, testCase := range []struct {
		name, latest, override string
		wantError              bool
	}{
		{name: "When no override is supplied, it should preserve legacy compatibility"},
		{name: "When override is the actual latest input, it should accept the phase", latest: "latest", override: "latest"},
		{name: "When override differs from latest, it should reject the phase", latest: "latest", override: "other", wantError: true},
		{name: "When latest input is missing, it should reject a nonempty phase", override: "latest", wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("E2E_LATEST_RELEASE_IMAGE", testCase.latest)
			t.Setenv("E2E_CONDITION_DIAGNOSTIC_RELEASE_IMAGE", testCase.override)
			if err := validateFailureConditionReleaseOverride(); (err != nil) != testCase.wantError {
				t.Fatalf("phase validation error=%v", err)
			}
		})
	}
}

func TestResolveFailureConditionVersion(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		cancel     bool
		missingKey bool
		payload    *releaseinfo.ReleaseImage
		wantLookup bool
	}{
		{name: "When the total context is canceled, it should not discover registry metadata", cancel: true},
		{name: "When the configured secret lacks auth data, it should qualify the diagnostic", missingKey: true},
		{name: "When registry metadata has no image stream, it should not panic", payload: &releaseinfo.ReleaseImage{}, wantLookup: true},
		{name: "When registry metadata is nil, it should not panic", wantLookup: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			hc := failureConditionFixture(t, "4.22.0", false)
			hc.Spec.PullSecret.Name = "pull-secret"
			scheme := runtime.NewScheme()
			if err := hyperv1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			if err := corev1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: hc.Spec.PullSecret.Name, Namespace: hc.Namespace}, Data: map[string][]byte{corev1.DockerConfigJsonKey: []byte(`{"auths":{}}`)}}
			if testCase.missingKey {
				secret.Data = nil
			}
			client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(hc, secret).Build()
			tc := &internal.TestContext{MgmtClient: client, ClusterName: hc.Name, ClusterNamespace: hc.Namespace}
			lookedUp := false
			provider := failureConditionReleaseProvider(func(context.Context, string, []byte) (*releaseinfo.ReleaseImage, error) {
				lookedUp = true
				return testCase.payload, nil
			})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if testCase.cancel {
				cancel()
			}
			_, err := resolveFailureConditionVersion(ctx, tc, "independent-input", provider)
			if err == nil || lookedUp != testCase.wantLookup {
				t.Fatalf("error=%v registry lookup=%v", err, lookedUp)
			}
			if testCase.cancel && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", err)
			}
		})
	}
	t.Run("When registry discovery reaches the total deadline, it should stop and preserve cancellation", func(t *testing.T) {
		hc := failureConditionFixture(t, "4.22.0", false)
		hc.Spec.PullSecret.Name = "pull-secret"
		scheme := runtime.NewScheme()
		if err := hyperv1.AddToScheme(scheme); err != nil {
			t.Fatal(err)
		}
		if err := corev1.AddToScheme(scheme); err != nil {
			t.Fatal(err)
		}
		secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: hc.Spec.PullSecret.Name, Namespace: hc.Namespace}, Data: map[string][]byte{corev1.DockerConfigJsonKey: []byte(`{"auths":{}}`)}}
		client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(hc, secret).Build()
		tc := &internal.TestContext{MgmtClient: client, ClusterName: hc.Name, ClusterNamespace: hc.Namespace}
		provider := failureConditionReleaseProvider(func(ctx context.Context, _ string, _ []byte) (*releaseinfo.ReleaseImage, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		})
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
		defer cancel()
		if _, err := resolveFailureConditionVersion(ctx, tc, "independent-input", provider); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("registry cancellation lost: %v", err)
		}
	})
}

func failureConditionFixture(t *testing.T, version string, workers bool) *hyperv1.HostedCluster {
	t.Helper()
	hc := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Name: "cluster", Namespace: "clusters"}, Spec: hyperv1.HostedClusterSpec{Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform}}}
	hc.Status.Version = &hyperv1.ClusterVersionStatus{History: []configv1.UpdateHistory{{Version: version}}}
	hc.Status.ControlPlaneVersion.Desired.Image = "quay.io/openshift-release-dev/ocp-release:4.22.0-x86_64"
	hc.Status.ControlPlaneVersion.History = []hyperv1.ControlPlaneUpdateHistory{{State: configv1.CompletedUpdate}}
	for conditionType, status := range healthExpectedConditions(hc, workers, semver.MustParse(version)) {
		hc.Status.Conditions = append(hc.Status.Conditions, metav1.Condition{Type: string(conditionType), Status: status})
	}
	return hc
}
