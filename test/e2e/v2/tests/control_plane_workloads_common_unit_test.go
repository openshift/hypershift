//go:build e2ev2

package tests

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	controlplaneoperatoroverrides "github.com/openshift/hypershift/hypershift-operator/controlplaneoperator-overrides"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	configv1 "github.com/openshift/api/config/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/blang/semver"
)

func TestControlPlaneDeploymentGenerations(t *testing.T) {
	listFailure := errors.New("deployment list unavailable")
	for _, testCase := range []struct {
		name       string
		generation int64
		otherOnly  bool
		empty      bool
		canceled   bool
		failure    error
		wantError  string
	}{
		{name: "When generation equals ten, it should accept the deployment", generation: 10},
		{name: "When generation is below ten, it should accept the deployment", generation: 1},
		{name: "When generation exceeds ten, it should identify the rollout", generation: 11, wantError: "deployment control-plane/unregistered-workload generation 11"},
		{name: "When only another namespace has deployments, it should reject empty coverage", generation: 1, otherOnly: true, wantError: "no control plane deployments"},
		{name: "When no deployments exist, it should reject vacuous coverage", empty: true, wantError: "no control plane deployments"},
		{name: "When listing fails, it should preserve the API error", failure: listFailure, wantError: listFailure.Error()},
		{name: "When canceled, it should preserve cancellation", canceled: true, wantError: context.Canceled.Error()},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := appsv1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			builder := fake.NewClientBuilder().WithScheme(scheme)
			if !testCase.empty {
				namespace := "control-plane"
				if testCase.otherOnly {
					namespace = "another-control-plane"
				}
				builder.WithObjects(&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: "unregistered-workload", Generation: testCase.generation}})
			}
			if testCase.failure != nil {
				builder.WithInterceptorFuncs(interceptor.Funcs{List: func(context.Context, crclient.WithWatch, crclient.ObjectList, ...crclient.ListOption) error {
					return testCase.failure
				}})
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if testCase.canceled {
				cancel()
			}
			err := controlPlaneDeploymentGenerations(ctx, builder.Build(), "control-plane")
			if testCase.wantError == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), testCase.wantError) {
				t.Fatalf("error = %v; want %q", err, testCase.wantError)
			}
			if testCase.failure != nil && !errors.Is(err, testCase.failure) {
				t.Fatalf("API error identity lost: %v", err)
			}
			if testCase.canceled && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation identity lost: %v", err)
			}
		})
	}
}

func TestControlPlaneDeploymentGenerationCheck(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	listFailure := errors.New("deployment list unavailable")
	getFailure := errors.New("hosted cluster unavailable")
	for _, testCase := range []struct {
		name       string
		created    metav1.Time
		generation int64
		getError   error
		listError  error
		canceled   bool
		applicable bool
		wantLists  int
		wantError  string
	}{
		{name: "When creation timestamp is zero and generation is high, it should skip without listing deployments", generation: 11},
		{name: "When cluster is older than four hours and generation is high, it should skip without listing deployments", created: metav1.NewTime(now.Add(-4*time.Hour - time.Second)), generation: 11},
		{name: "When cluster is recent and generation is high, it should fail the namespace assertion", created: metav1.NewTime(now.Add(-time.Hour)), generation: 11, applicable: true, wantLists: 1, wantError: "generation 11 exceeds maximum 10"},
		{name: "When cluster is exactly four hours old and generation is high, it should retain the inclusive age boundary", created: metav1.NewTime(now.Add(-4 * time.Hour)), generation: 11, applicable: true, wantLists: 1, wantError: "generation 11 exceeds maximum 10"},
		{name: "When cluster is recent and generation is ten, it should pass the namespace assertion", created: metav1.NewTime(now.Add(-time.Hour)), generation: 10, applicable: true, wantLists: 1},
		{name: "When a recent cluster deployment list fails, it should preserve the API error", created: metav1.NewTime(now.Add(-time.Hour)), applicable: true, wantLists: 1, listError: listFailure, wantError: listFailure.Error()},
		{name: "When a recent cluster context is canceled, it should preserve cancellation without listing", created: metav1.NewTime(now.Add(-time.Hour)), canceled: true, applicable: true, wantError: context.Canceled.Error()},
		{name: "When fetching the hosted cluster fails, it should fail without listing deployments", getError: getFailure, wantError: getFailure.Error()},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := appsv1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			if err := hyperv1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			hostedCluster := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Name: "cluster", Namespace: "clusters", CreationTimestamp: testCase.created}, Spec: hyperv1.HostedClusterSpec{Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform}}}
			deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "unregistered-workload", Namespace: "control-plane", Generation: testCase.generation}}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			lists := 0
			client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(hostedCluster, deployment).WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, client crclient.WithWatch, key crclient.ObjectKey, object crclient.Object, options ...crclient.GetOption) error {
					if testCase.getError != nil {
						return testCase.getError
					}
					err := client.Get(ctx, key, object, options...)
					if testCase.canceled {
						cancel()
					}
					return err
				},
				List: func(ctx context.Context, client crclient.WithWatch, objects crclient.ObjectList, options ...crclient.ListOption) error {
					lists++
					if testCase.listError != nil {
						return testCase.listError
					}
					return client.List(ctx, objects, options...)
				},
			}).Build()
			tc := &internal.TestContext{Context: ctx, MgmtClient: client, ClusterName: hostedCluster.Name, ClusterNamespace: hostedCluster.Namespace, ControlPlaneNamespace: deployment.Namespace}
			applicable, err := controlPlaneDeploymentGenerationCheck(tc, now)
			if applicable != testCase.applicable || lists != testCase.wantLists {
				t.Errorf("applicable = %v, deployment lists = %d; want %v, %d", applicable, lists, testCase.applicable, testCase.wantLists)
			}
			if testCase.wantError == "" && err != nil || testCase.wantError != "" && (err == nil || !strings.Contains(err.Error(), testCase.wantError)) {
				t.Fatalf("error = %v; want %q", err, testCase.wantError)
			}
			for _, expectedError := range []error{testCase.getError, testCase.listError} {
				if expectedError != nil && !errors.Is(err, expectedError) {
					t.Errorf("error = %v; want preserved identity %v", err, expectedError)
				}
			}
			if testCase.canceled && !errors.Is(err, context.Canceled) {
				t.Errorf("error = %v; want preserved cancellation", err)
			}
		})
	}
}

func TestControlPlaneTrustBundleProbe(t *testing.T) {
	probeFailure := errors.New("exec unavailable")
	for _, testCase := range []struct {
		name     string
		deadline time.Duration
		canceled bool
		failure  error
	}{
		{name: "When exec succeeds, it should bound and release the attempt"},
		{name: "When exec fails, it should preserve the error and release the attempt", failure: probeFailure},
		{name: "When the parent deadline is sooner, it should preserve the earlier bound", deadline: 10 * time.Second},
		{name: "When the parent is canceled, it should propagate cancellation", canceled: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			parent, cancel := context.WithCancel(t.Context())
			defer cancel()
			if testCase.deadline != 0 {
				var cancelDeadline context.CancelFunc
				parent, cancelDeadline = context.WithTimeout(parent, testCase.deadline)
				defer cancelDeadline()
			}
			if testCase.canceled {
				cancel()
			}
			before := time.Now()
			var attemptDone <-chan struct{}
			err := controlPlaneTrustBundleProbe(parent, func(ctx context.Context) error {
				attemptDone = ctx.Done()
				deadline, present := ctx.Deadline()
				if !present || deadline.After(time.Now().Add(time.Minute)) {
					t.Fatal("exec must have a maximum one-minute attempt deadline")
				}
				if parentDeadline, present := parent.Deadline(); present && parentDeadline.Before(before.Add(time.Minute)) {
					if !deadline.Equal(parentDeadline) {
						t.Fatal("exec must preserve an earlier parent deadline")
					}
				} else if deadline.Before(before.Add(time.Minute)) {
					t.Fatal("exec must preserve the full baseline one-minute attempt budget")
				}
				if testCase.canceled && !errors.Is(ctx.Err(), context.Canceled) {
					t.Fatal("exec must inherit parent cancellation")
				}
				return testCase.failure
			})
			if !errors.Is(err, testCase.failure) {
				t.Fatalf("probe returned %v; it must preserve %v", err, testCase.failure)
			}
			select {
			case <-attemptDone:
			default:
				t.Fatal("probe must cancel its attempt before returning")
			}
		})
	}
}

func TestControlPlaneGenerationApplicable(t *testing.T) {
	now := time.Date(2026, 10, 21, 19, 0, 0, 0, time.UTC)
	for _, testCase := range []struct {
		name      string
		timestamp metav1.Time
		expected  bool
	}{
		{name: "When cluster is fresh, it should validate generation", timestamp: metav1.NewTime(now.Add(-time.Hour)), expected: true},
		{name: "When timestamp is zero, it should preserve the existing skip"},
		{name: "When cluster is older than four hours, it should preserve the existing skip", timestamp: metav1.NewTime(now.Add(-4*time.Hour - time.Nanosecond))},
		{name: "When cluster is exactly four hours old, it should validate generation", timestamp: metav1.NewTime(now.Add(-4 * time.Hour)), expected: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			hc := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{CreationTimestamp: testCase.timestamp}}
			if actual := controlPlaneGenerationApplicable(hc, now); actual != testCase.expected {
				t.Fatalf("controlPlaneGenerationApplicable() = %v, want %v", actual, testCase.expected)
			}
		})
	}
}

func TestControlPlaneCustomizationMatches(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		platform    hyperv1.PlatformType
		labels      map[string]string
		tolerations []corev1.Toleration
		pod         corev1.Pod
		expected    bool
	}{
		{name: "When AWS baseline customizations propagate, it should match", platform: hyperv1.AWSPlatform, labels: map[string]string{"hypershift-e2e-test-label": "test"}, tolerations: []corev1.Toleration{{Key: "hypershift-e2e-test-toleration", Operator: corev1.TolerationOpEqual, Value: "true", Effect: corev1.TaintEffectNoSchedule}}, pod: corev1.Pod{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"hypershift-e2e-test-label": "test"}}, Spec: corev1.PodSpec{Tolerations: []corev1.Toleration{{Key: "hypershift-e2e-test-toleration", Operator: corev1.TolerationOpEqual, Value: "true", Effect: corev1.TaintEffectNoSchedule}}}}, expected: true},
		{name: "When Azure has no customizations, it should not require AWS inputs", platform: hyperv1.AzurePlatform, expected: true},
		{name: "When arbitrary labels and tolerations propagate, it should match", labels: map[string]string{"team": "control-plane"}, tolerations: []corev1.Toleration{{Key: "dedicated", Operator: corev1.TolerationOpExists}}, pod: corev1.Pod{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"team": "control-plane", "app": "etcd"}}, Spec: corev1.PodSpec{Tolerations: []corev1.Toleration{{Key: "dedicated", Operator: corev1.TolerationOpExists}}}}, expected: true},
		{name: "When a configured label is missing, it should fail", labels: map[string]string{"team": "control-plane"}},
		{name: "When a configured toleration is missing, it should fail", tolerations: []corev1.Toleration{{Key: "dedicated", Operator: corev1.TolerationOpExists}}},
		{name: "When toleration seconds values match through distinct pointers, it should match", tolerations: []corev1.Toleration{{Key: "dedicated", Effect: corev1.TaintEffectNoExecute, TolerationSeconds: ptr.To(int64(60))}}, pod: corev1.Pod{Spec: corev1.PodSpec{Tolerations: []corev1.Toleration{{Key: "dedicated", Effect: corev1.TaintEffectNoExecute, TolerationSeconds: ptr.To(int64(60))}}}}, expected: true},
		{name: "When toleration seconds differ, it should fail", tolerations: []corev1.Toleration{{Key: "dedicated", Effect: corev1.TaintEffectNoExecute, TolerationSeconds: ptr.To(int64(60))}}, pod: corev1.Pod{Spec: corev1.PodSpec{Tolerations: []corev1.Toleration{{Key: "dedicated", Effect: corev1.TaintEffectNoExecute, TolerationSeconds: ptr.To(int64(90))}}}}},
		{name: "When label value differs, it should fail", labels: map[string]string{"team": "control-plane"}, pod: corev1.Pod{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"team": "other"}}}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			hc := &hyperv1.HostedCluster{}
			hc.Spec.Platform.Type = testCase.platform
			hc.Spec.Labels, hc.Spec.Tolerations = testCase.labels, testCase.tolerations
			if actual := controlPlaneCustomizationMatches(testCase.pod, hc); actual != testCase.expected {
				t.Fatalf("controlPlaneCustomizationMatches() = %v, want %v", actual, testCase.expected)
			}
		})
	}
}

func TestControlPlaneCustomizablePod(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		labels   map[string]string
		expected bool
	}{
		{name: "When a normal component exists, it should be customizable", labels: map[string]string{"app": "kube-apiserver"}, expected: true},
		{name: "When app is missing, it should still require customization", expected: true},
		{name: "When virt launcher exists, it should be excluded", labels: map[string]string{"kubevirt.io": "virt-launcher"}},
		{name: "When console debug exists, it should be excluded", labels: map[string]string{"app": "vmi-console-debug"}},
		{name: "When CDI labels exist, it should be excluded", labels: map[string]string{"cdi.kubevirt.io": ""}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if actual := controlPlaneCustomizablePod(corev1.Pod{ObjectMeta: metav1.ObjectMeta{Labels: testCase.labels}}); actual != testCase.expected {
				t.Fatalf("controlPlaneCustomizablePod() = %v, want %v", actual, testCase.expected)
			}
		})
	}
}

func TestControlPlaneFilesystemExempt(t *testing.T) {
	for _, testCase := range []struct {
		name, app, container, component string
		temporary, expected             bool
	}{
		{name: "When packageserver main container uses tmp, it should be exempt", app: "packageserver", container: "packageserver", temporary: true, expected: true},
		{name: "When packageserver has a sidecar, it should require tmp", app: "packageserver", container: "sidecar", temporary: true},
		{name: "When packageserver rootfs is checked, it should not be exempt", app: "packageserver", container: "packageserver"},
		{name: "When snapshot controller rootfs is checked, it should not be exempt", app: "csi-snapshot-controller"},
		{name: "When snapshot controller tmp is checked, it should be exempt", app: "csi-snapshot-controller", temporary: true, expected: true},
		{name: "When AWS CSI rootfs is checked, it should be exempt", app: "aws-ebs-csi-driver-controller", expected: true},
		{name: "When etcd component is checked, it should be exempt", component: "etcd", expected: true},
		{name: "When an unknown workload is checked, it should not be exempt"},
		{name: "When an old broad shared resource exemption is checked, it should not be exempt", app: "shared-resource-csi-driver-operator"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			pod := corev1.Pod{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": testCase.app, "hypershift.openshift.io/control-plane-component": testCase.component}}}
			if actual := controlPlaneFilesystemExempt(pod, corev1.Container{Name: testCase.container}, testCase.temporary); actual != testCase.expected {
				t.Fatalf("controlPlaneFilesystemExempt() = %v, want %v", actual, testCase.expected)
			}
		})
	}
}

func TestControlPlaneCommonReleaseChecks(t *testing.T) {
	for _, testCase := range []struct {
		name, previous, latest, override, target, observed string
		previousVersion, latestVersion                     string
		want                                               controlPlaneCommonChecks
		wantError                                          bool
	}{
		{name: "When independent 4.15 contradicts observed 4.23, it should keep only the legacy snapshot exception", previous: "previous", previousVersion: "4.15.0", observed: "4.23.0", want: controlPlaneCommonChecks{snapshotToken: true}},
		{name: "When independent 4.16 has missing history, it should enable only token checks", previous: "previous", previousVersion: "4.16.0", want: controlPlaneCommonChecks{tokens: true, snapshotToken: true}},
		{name: "When independent 4.17 has malformed history, it should retain snapshot exemption", previous: "previous", previousVersion: "4.17.0", observed: "malformed", want: controlPlaneCommonChecks{tokens: true, snapshotToken: true}},
		{name: "When independent 4.18 contradicts older history, it should remove snapshot token exemption", previous: "previous", previousVersion: "4.18.0", observed: "4.16.0", want: controlPlaneCommonChecks{tokens: true}},
		{name: "When independent 4.19 has missing history, it should add termination checks", previous: "previous", previousVersion: "4.19.0", want: controlPlaneCommonChecks{tokens: true, termination: true}},
		{name: "When independent 4.20 has malformed history, it should add filesystem and eviction checks", previous: "previous", previousVersion: "4.20.0", observed: "malformed", want: controlPlaneCommonChecks{tokens: true, termination: true, filesystem: true, safeEviction: true}},
		{name: "When proven CP phase selects latest, it should enable latest checks", previous: "previous", latest: "latest", override: "latest", target: "latest", previousVersion: "4.15.0", latestVersion: "4.20.0", observed: "4.15.0", want: controlPlaneCommonChecks{tokens: true, termination: true, filesystem: true, safeEviction: true}},
		{name: "When AutoNode retains previous despite target mode, it should not enable latest checks", previous: "previous", latest: "latest", target: "latest", previousVersion: "4.17.0", latestVersion: "4.20.0", observed: "4.20.0", want: controlPlaneCommonChecks{tokens: true, snapshotToken: true}},
		{name: "When configured metadata is unresolved, it should fail without observed fallback", previous: "previous", observed: "4.23.0", wantError: true},
		{name: "When release override is invalid, it should fail rather than skip", latest: "latest", override: "other", latestVersion: "4.20.0", observed: "4.23.0", wantError: true},
		{name: "When upgrade target is invalid, it should fail rather than fall back", previous: "previous", previousVersion: "4.20.0", target: "other", wantError: true},
		{name: "When standalone inputs are absent, it should retain observed version compatibility", observed: "4.20.0", want: controlPlaneCommonChecks{tokens: true, termination: true, filesystem: true, safeEviction: true}},
		{name: "When standalone history is malformed, it should preserve its error", observed: "malformed", wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("E2E_PREVIOUS_RELEASE_IMAGE", testCase.previous)
			t.Setenv("E2E_LATEST_RELEASE_IMAGE", testCase.latest)
			t.Setenv("E2E_CONDITION_DIAGNOSTIC_RELEASE_IMAGE", testCase.override)
			t.Setenv("E2E_CONDITION_DIAGNOSTIC_UPGRADE_TARGET", testCase.target)
			scheme := runtime.NewScheme()
			if err := hyperv1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			hc := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Name: "cluster", Namespace: "clusters"}}
			if testCase.observed != "" {
				hc.Status.Version = &hyperv1.ClusterVersionStatus{History: []configv1.UpdateHistory{{Version: testCase.observed}}}
			}
			reads := 0
			client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(hc).WithInterceptorFuncs(interceptor.Funcs{Get: func(ctx context.Context, client crclient.WithWatch, key crclient.ObjectKey, object crclient.Object, opts ...crclient.GetOption) error {
				reads++
				return client.Get(ctx, key, object, opts...)
			}}).Build()
			tc := &internal.TestContext{Context: t.Context(), MgmtClient: client, ClusterName: hc.Name, ClusterNamespace: hc.Namespace, ConditionDiagnosticReleaseVersions: map[string]semver.Version{}, ConditionDiagnosticReleaseErrors: map[string]error{"previous": errors.New("independent metadata unavailable")}}
			if testCase.previousVersion != "" {
				tc.ConditionDiagnosticReleaseVersions["previous"] = semver.MustParse(testCase.previousVersion)
			}
			if testCase.latestVersion != "" {
				tc.ConditionDiagnosticReleaseVersions["latest"] = semver.MustParse(testCase.latestVersion)
			}
			resetFailureConditionPhase(tc)
			checks, err := controlPlaneCommonReleaseChecks(tc)
			if (err != nil) != testCase.wantError || !testCase.wantError && checks != testCase.want {
				t.Fatalf("checks=%+v error=%v, expected %+v", checks, err, testCase.want)
			}
			configured := testCase.previous != "" || testCase.latest != "" || testCase.override != "" || testCase.target != ""
			if configured && reads != 0 || !configured && reads != 1 {
				t.Fatalf("originating release lookup used management history: reads=%d configured=%v", reads, configured)
			}
		})
	}
	t.Run("When actual AWS CPO mapped releases are selected, it should use their keyed cached metadata instead of observed HC version", func(t *testing.T) {
		latest, previous := controlplaneoperatoroverrides.LatestOverrideTestReleases("aws")
		if latest == "" || previous == "" || latest == previous {
			t.Fatal("actual AWS CPO mapped pair must be distinct")
		}
		t.Setenv("E2E_PREVIOUS_RELEASE_IMAGE", previous)
		t.Setenv("E2E_LATEST_RELEASE_IMAGE", latest)
		t.Setenv("E2E_CONDITION_DIAGNOSTIC_RELEASE_IMAGE", "")
		t.Setenv("E2E_CONDITION_DIAGNOSTIC_UPGRADE_TARGET", "")
		tc := &internal.TestContext{ConditionDiagnosticReleaseVersions: map[string]semver.Version{previous: semver.MustParse("4.17.0"), latest: semver.MustParse("4.17.0")}}
		resetFailureConditionPhase(tc)
		checks, err := controlPlaneCommonReleaseChecks(tc)
		if err != nil || checks != (controlPlaneCommonChecks{tokens: true, snapshotToken: true}) {
			t.Fatalf("actual mapped pair origin checks=%+v error=%v", checks, err)
		}
	})
}

func commonWorkloadChecksFixture(t *testing.T, version string) controlPlaneCommonChecks {
	t.Helper()
	image := "quay.io/openshift-release-dev/ocp-release:" + version + "-x86_64"
	t.Setenv("E2E_PREVIOUS_RELEASE_IMAGE", image)
	t.Setenv("E2E_LATEST_RELEASE_IMAGE", "")
	t.Setenv("E2E_CONDITION_DIAGNOSTIC_RELEASE_IMAGE", "")
	t.Setenv("E2E_CONDITION_DIAGNOSTIC_UPGRADE_TARGET", "")
	tc := &internal.TestContext{ConditionDiagnosticReleaseVersions: map[string]semver.Version{image: semver.MustParse(version)}}
	resetFailureConditionPhase(tc)
	checks, err := controlPlaneCommonReleaseChecks(tc)
	if err != nil {
		t.Fatal(err)
	}
	return checks
}

func commonWorkloadPodFixture() corev1.Pod {
	return corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "custom-operator-0", Labels: map[string]string{"app": "custom-operator"}}, Spec: corev1.PodSpec{
		Priority:       ptr.To(int32(100002000)),
		Containers:     []corev1.Container{{Name: "operator", Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("10m"), corev1.ResourceMemory: resource.MustParse("16Mi")}}, TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError, SecurityContext: &corev1.SecurityContext{ReadOnlyRootFilesystem: ptr.To(true)}, VolumeMounts: []corev1.VolumeMount{{Name: "tmp-dir", MountPath: "/tmp"}}}},
		InitContainers: []corev1.Container{{Name: "init", TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError}},
	}}
}

func TestControlPlanePodResources(t *testing.T) {
	for _, testCase := range []struct {
		name, version, wantError string
		mutate                   func(*corev1.Pod)
	}{
		{name: "When all assertions pass at 4.20, it should accept ordinary and init containers", version: "4.20.0"},
		{name: "When CPU is missing below all gates, it should still fail", version: "4.15.0", mutate: func(pod *corev1.Pod) { delete(pod.Spec.Containers[0].Resources.Requests, corev1.ResourceCPU) }, wantError: "must request cpu"},
		{name: "When memory is missing below all gates, it should still fail", version: "4.15.0", mutate: func(pod *corev1.Pod) { delete(pod.Spec.Containers[0].Resources.Requests, corev1.ResourceMemory) }, wantError: "must request memory"},
		{name: "When priority is excessive below all gates, it should still fail", version: "4.15.0", mutate: func(pod *corev1.Pod) { pod.Spec.Priority = ptr.To(int32(100002001)) }, wantError: "priority"},
		{name: "When multus uses excessive priority, it should retain its exemption", version: "4.20.0", mutate: func(pod *corev1.Pod) {
			pod.Name = "multus-admission-controller-0"
			pod.Spec.Priority = ptr.To(int32(100002001))
		}},
		{name: "When catalog rollout lacks resource requests, it should retain its exemption", version: "4.20.0", mutate: func(pod *corev1.Pod) {
			pod.Name = "custom-catalog-rollout-0"
			pod.Spec.Containers[0].Resources.Requests = nil
		}},
		{name: "When ordinary termination is wrong before 4.19, it should preserve older applicability", version: "4.18.0", mutate: func(pod *corev1.Pod) {
			pod.Spec.Containers[0].TerminationMessagePolicy = corev1.TerminationMessageReadFile
		}},
		{name: "When ordinary termination is wrong at 4.19, it should fail", version: "4.19.0", mutate: func(pod *corev1.Pod) {
			pod.Spec.Containers[0].TerminationMessagePolicy = corev1.TerminationMessageReadFile
		}, wantError: "container operator must use termination policy"},
		{name: "When init termination is wrong at 4.19, it should fail", version: "4.19.0", mutate: func(pod *corev1.Pod) {
			pod.Spec.InitContainers[0].TerminationMessagePolicy = corev1.TerminationMessageReadFile
		}, wantError: "container init must use termination policy"},
		{name: "When CNO termination is wrong, it should retain its exact exception", version: "4.20.0", mutate: func(pod *corev1.Pod) {
			pod.Name = "network-node-identity-0"
			pod.Spec.InitContainers[0].TerminationMessagePolicy = corev1.TerminationMessageReadFile
		}},
		{name: "When unnecessary token is mounted before 4.16, it should retain older applicability", version: "4.15.0", mutate: func(pod *corev1.Pod) { pod.Spec.Volumes = []corev1.Volume{{Name: "kube-api-access-token"}} }},
		{name: "When unnecessary token is mounted at 4.16, it should fail", version: "4.16.0", mutate: func(pod *corev1.Pod) { pod.Spec.Volumes = []corev1.Volume{{Name: "kube-api-access-token"}} }, wantError: "unnecessary service account token"},
		{name: "When snapshot webhook mounts token before 4.18, it should retain its exception", version: "4.17.0", mutate: func(pod *corev1.Pod) {
			pod.Name = "csi-snapshot-webhook-0"
			pod.Spec.Volumes = []corev1.Volume{{Name: "kube-api-access-token"}}
		}},
		{name: "When snapshot webhook mounts token at 4.18, it should fail", version: "4.18.0", mutate: func(pod *corev1.Pod) {
			pod.Name = "csi-snapshot-webhook-0"
			pod.Spec.Volumes = []corev1.Volume{{Name: "kube-api-access-token"}}
		}, wantError: "unnecessary service account token"},
		{name: "When audited snapshot controller mounts token after 4.18, it should retain its exception", version: "4.20.0", mutate: func(pod *corev1.Pod) {
			pod.Name = "csi-snapshot-controller-0"
			pod.Spec.Volumes = []corev1.Volume{{Name: "kube-api-access-token"}}
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			checks := commonWorkloadChecksFixture(t, testCase.version)
			pod := commonWorkloadPodFixture()
			if testCase.mutate != nil {
				testCase.mutate(&pod)
			}
			err := controlPlanePodResources(pod, hyperv1.AWSPlatform, checks)
			if testCase.wantError == "" && err != nil || testCase.wantError != "" && (err == nil || !strings.Contains(err.Error(), testCase.wantError)) {
				t.Fatalf("resource assertions error=%v, want %q", err, testCase.wantError)
			}
		})
	}
}

func TestControlPlanePodFilesystem(t *testing.T) {
	for _, testCase := range []struct {
		name, wantError string
		mutate          func(*corev1.Pod)
	}{
		{name: "When ordinary rootfs and tmp are valid, it should pass"},
		{name: "When security context is nil, it should fail safely", mutate: func(pod *corev1.Pod) { pod.Spec.Containers[0].SecurityContext = nil }, wantError: "read-only root filesystem"},
		{name: "When readonly field is absent, it should fail", mutate: func(pod *corev1.Pod) { pod.Spec.Containers[0].SecurityContext.ReadOnlyRootFilesystem = nil }, wantError: "read-only root filesystem"},
		{name: "When rootfs is writable, it should fail", mutate: func(pod *corev1.Pod) { pod.Spec.Containers[0].SecurityContext.ReadOnlyRootFilesystem = ptr.To(false) }, wantError: "read-only root filesystem"},
		{name: "When tmp mount is missing, it should fail", mutate: func(pod *corev1.Pod) { pod.Spec.Containers[0].VolumeMounts = nil }, wantError: "mount /tmp"},
		{name: "When AWS CSI is exempt, it should allow both missing settings", mutate: func(pod *corev1.Pod) {
			pod.Labels["app"] = "aws-ebs-csi-driver-controller"
			pod.Spec.Containers[0].SecurityContext = nil
			pod.Spec.Containers[0].VolumeMounts = nil
		}},
		{name: "When packageserver main lacks tmp, it should retain only tmp exemption", mutate: func(pod *corev1.Pod) {
			pod.Labels["app"] = "packageserver"
			pod.Spec.Containers[0].Name = "packageserver"
			pod.Spec.Containers[0].VolumeMounts = nil
		}},
		{name: "When packageserver sidecar lacks tmp, it should fail", mutate: func(pod *corev1.Pod) { pod.Labels["app"] = "packageserver"; pod.Spec.Containers[0].VolumeMounts = nil }, wantError: "mount /tmp"},
		{name: "When snapshot rootfs is writable, it should fail despite tmp exemption", mutate: func(pod *corev1.Pod) {
			pod.Labels["app"] = "csi-snapshot-controller"
			pod.Spec.Containers[0].SecurityContext = nil
			pod.Spec.Containers[0].VolumeMounts = nil
		}, wantError: "read-only root filesystem"},
		{name: "When etcd component is exempt, it should retain both exemptions", mutate: func(pod *corev1.Pod) {
			pod.Labels["hypershift.openshift.io/control-plane-component"] = "etcd"
			pod.Spec.Containers[0].SecurityContext = nil
			pod.Spec.Containers[0].VolumeMounts = nil
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			checks := commonWorkloadChecksFixture(t, "4.20.0")
			if !checks.filesystem {
				t.Fatal("4.20 filesystem assertion must be selected")
			}
			pod := commonWorkloadPodFixture()
			if testCase.mutate != nil {
				testCase.mutate(&pod)
			}
			err := controlPlanePodFilesystem(pod)
			if testCase.wantError == "" && err != nil || testCase.wantError != "" && (err == nil || !strings.Contains(err.Error(), testCase.wantError)) {
				t.Fatalf("filesystem error=%v, want %q", err, testCase.wantError)
			}
		})
	}
}

func TestControlPlanePodSafeEviction(t *testing.T) {
	for _, testCase := range []struct {
		name, app, label, value, annotation string
		volume                              corev1.VolumeSource
		wantError                           bool
	}{
		{name: "When audited disk emptydir is annotated, it should pass", app: "kube-apiserver", annotation: "local", volume: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		{name: "When audited disk emptydir is missing annotation, it should fail", app: "kube-apiserver", volume: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}, wantError: true},
		{name: "When hostpath is missing annotation, it should fail", app: "kube-apiserver", volume: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{}}, wantError: true},
		{name: "When memory emptydir is not annotated, it should remain exempt", app: "kube-apiserver", volume: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{Medium: corev1.StorageMediumMemory}}},
		{name: "When annotation only contains a substring, it should still require exact volume membership", app: "kube-apiserver", annotation: "local-other", volume: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}, wantError: true},
		{name: "When unaudited pod annotates a non-tmp volume, it should fail", app: "custom", annotation: "local", wantError: true},
		{name: "When unaudited pod has no annotation, it should retain baseline exemption", app: "custom", volume: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		{name: "When unaudited tmp annotation omits another local volume, it should fail", app: "custom", annotation: "tmp-dir", volume: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}, wantError: true},
		{name: "When name label audits image registry, it should require local annotation", label: "name", value: "cluster-image-registry-operator", volume: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}, wantError: true},
		{name: "When kubevirt label audits launcher, it should require local annotation", label: "kubevirt.io", value: "virt-launcher", volume: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}, wantError: true},
		{name: "When featuregate generator component is exempt, it should retain exemption", label: "hypershift.openshift.io/control-plane-component", value: "featuregate-generator", volume: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			checks := commonWorkloadChecksFixture(t, "4.20.0")
			if !checks.safeEviction {
				t.Fatal("4.20 eviction assertion must be selected")
			}
			pod := corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "workload", Labels: map[string]string{"app": testCase.app, testCase.label: testCase.value}, Annotations: map[string]string{"cluster-autoscaler.kubernetes.io/safe-to-evict-local-volumes": testCase.annotation}}, Spec: corev1.PodSpec{Volumes: []corev1.Volume{{Name: "local", VolumeSource: testCase.volume}}}}
			if err := controlPlanePodSafeEviction(pod); (err != nil) != testCase.wantError {
				t.Fatalf("eviction assertion error=%v, want error %v", err, testCase.wantError)
			}
		})
	}
}
