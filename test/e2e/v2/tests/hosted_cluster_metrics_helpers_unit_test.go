//go:build e2ev2

package tests

import (
	"context"
	"errors"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	controlplaneoperatoroverrides "github.com/openshift/hypershift/hypershift-operator/controlplaneoperator-overrides"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/blang/semver"
)

func TestMetricsNodeTuningApplicable(t *testing.T) {
	listFailure := errors.New("management NodePool list unavailable")
	for _, testCase := range []struct {
		name, previous, latest, override, target string
		previousVersion, latestVersion, observed string
		replicas                                 []*int32
		private, foreign, otherNamespace         bool
		autoscale, disabled, azure, canceled     bool
		listError, applicable, wantError         bool
	}{
		{name: "When the independent release is too old, it should ignore newer observed history", previous: "previous", previousVersion: "4.21.0", observed: "5.0.0", replicas: []*int32{ptr.To(int32(1))}},
		{name: "When static AWS workers are desired without observed history or guest access, it should require metrics", previous: "previous", previousVersion: "4.22.0", replicas: []*int32{ptr.To(int32(1))}, applicable: true},
		{name: "When observed history contradicts an eligible originating release, it should retain independent eligibility", previous: "previous", previousVersion: "4.22.0", observed: "4.14.0", replicas: []*int32{ptr.To(int32(1))}, applicable: true},
		{name: "When observed history is malformed, it should use configured release metadata", latest: "latest", latestVersion: "4.22.0", observed: "malformed", replicas: []*int32{ptr.To(int32(1))}, applicable: true},
		{name: "When ordinary CP phase selects latest, it should apply the latest eligibility boundary", previous: "previous", latest: "latest", override: "latest", previousVersion: "4.21.0", latestVersion: "4.22.0", replicas: []*int32{ptr.To(int32(1))}, applicable: true},
		{name: "When AutoNode retains originating previous gates, it should not use the target boundary", previous: "previous", latest: "latest", target: "latest", previousVersion: "4.21.0", latestVersion: "4.23.0", observed: "4.23.0", replicas: []*int32{ptr.To(int32(1))}},
		{name: "When configured metadata is unresolved, it should fail rather than trust observed history", previous: "previous", observed: "4.23.0", replicas: []*int32{ptr.To(int32(1))}, wantError: true},
		{name: "When standalone history is eligible, it should preserve native compatibility", observed: "4.22.0", replicas: []*int32{ptr.To(int32(1))}, applicable: true},
		{name: "When standalone history is malformed, it should fail", observed: "malformed", wantError: true},
		{name: "When static replicas are zero, it should not require a worker endpoint", observed: "4.22.0", replicas: []*int32{ptr.To(int32(0))}},
		{name: "When static replicas are absent, it should not infer workers", observed: "4.22.0", replicas: []*int32{nil}},
		{name: "When only AWS autoscaling maxima are positive, it should not substitute them for static replicas", observed: "4.22.0", replicas: []*int32{nil}, autoscale: true},
		{name: "When a private AWS pool has static workers, it should require management-side metrics", observed: "4.22.0", replicas: []*int32{ptr.To(int32(1))}, private: true, applicable: true},
		{name: "When only another cluster has workers, it should not count them", observed: "4.22.0", replicas: []*int32{ptr.To(int32(1))}, foreign: true},
		{name: "When only another namespace has workers, it should not count them", observed: "4.22.0", replicas: []*int32{ptr.To(int32(1))}, otherNamespace: true},
		{name: "When replica totals exceed int32, it should preserve positive applicability", observed: "4.22.0", replicas: []*int32{ptr.To(int32(2147483647)), ptr.To(int32(1))}, applicable: true},
		{name: "When desired replicas are negative, it should reject invalid intent", observed: "4.22.0", replicas: []*int32{ptr.To(int32(-1))}, wantError: true},
		{name: "When NodeTuning capability is disabled, it should preserve the capability exclusion", observed: "4.22.0", replicas: []*int32{ptr.To(int32(1))}, disabled: true},
		{name: "When management listing fails, it should propagate the error rather than skip", observed: "4.22.0", listError: true, wantError: true},
		{name: "When the caller is canceled, it should preserve cancellation", observed: "4.22.0", canceled: true, wantError: true},
		{name: "When nonAWS private autoscaling workers exist, it should preserve native worker discovery", observed: "4.22.0", replicas: []*int32{nil}, autoscale: true, azure: true, applicable: true},
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
			hostedCluster := failureConditionFixture(t, "4.23.0", false)
			if testCase.observed == "" {
				hostedCluster.Status.Version = nil
			} else {
				hostedCluster.Status.Version.History[0].Version = testCase.observed
			}
			hostedCluster.Spec.Platform.AWS = &hyperv1.AWSPlatformSpec{EndpointAccess: hyperv1.Public}
			if testCase.private {
				hostedCluster.Spec.Platform.AWS.EndpointAccess = hyperv1.Private
			}
			if testCase.azure {
				hostedCluster.Spec.Platform = hyperv1.PlatformSpec{Type: hyperv1.AzurePlatform, Azure: &hyperv1.AzurePlatformSpec{Topology: hyperv1.AzureTopologyPrivate}}
			}
			if testCase.disabled {
				hostedCluster.Spec.Capabilities = &hyperv1.Capabilities{Disabled: []hyperv1.OptionalCapability{hyperv1.NodeTuningCapability}}
			}
			objects := []crclient.Object{hostedCluster}
			for index, replicas := range testCase.replicas {
				pool := &hyperv1.NodePool{ObjectMeta: metav1.ObjectMeta{Name: []string{"workers", "workers-two"}[index], Namespace: hostedCluster.Namespace}, Spec: hyperv1.NodePoolSpec{ClusterName: hostedCluster.Name, Replicas: replicas}}
				if testCase.foreign {
					pool.Spec.ClusterName = "foreign"
				}
				if testCase.otherNamespace {
					pool.Namespace = "other"
				}
				if testCase.autoscale {
					pool.Spec.AutoScaling = &hyperv1.NodePoolAutoScaling{Min: ptr.To(int32(1)), Max: 3}
				}
				objects = append(objects, pool)
			}
			builder := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...)
			if testCase.listError {
				builder.WithInterceptorFuncs(interceptor.Funcs{List: func(context.Context, crclient.WithWatch, crclient.ObjectList, ...crclient.ListOption) error {
					return listFailure
				}})
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if testCase.canceled {
				cancel()
			}
			tc := &internal.TestContext{Context: ctx, MgmtClient: builder.Build(), ClusterName: hostedCluster.Name, ClusterNamespace: hostedCluster.Namespace, ConditionDiagnosticReleaseVersions: map[string]semver.Version{}}
			if testCase.previousVersion != "" {
				tc.ConditionDiagnosticReleaseVersions["previous"] = semver.MustParse(testCase.previousVersion)
			}
			if testCase.latestVersion != "" {
				tc.ConditionDiagnosticReleaseVersions["latest"] = semver.MustParse(testCase.latestVersion)
			}
			resetFailureConditionPhase(tc)
			applicable, err := metricsNodeTuningApplicable(tc, hostedCluster)
			if applicable != testCase.applicable || (err != nil) != testCase.wantError {
				t.Fatalf("applicability = %v, %v; want %v, error %v", applicable, err, testCase.applicable, testCase.wantError)
			}
			if testCase.listError && !errors.Is(err, listFailure) {
				t.Fatalf("management list error identity lost: %v", err)
			}
			if testCase.canceled && !errors.Is(err, context.Canceled) {
				t.Fatalf("context cancellation identity lost: %v", err)
			}
		})
	}
	t.Run("When the actual mapped CPO pair is transported without an override, it should use its keyed originating metadata", func(t *testing.T) {
		latest, previous := controlplaneoperatoroverrides.LatestOverrideTestReleases("aws")
		if latest == "" || previous == "" || latest == previous {
			t.Fatal("actual mapped AWS CPO releases must be distinct")
		}
		t.Setenv("E2E_PREVIOUS_RELEASE_IMAGE", previous)
		t.Setenv("E2E_LATEST_RELEASE_IMAGE", latest)
		t.Setenv("E2E_CONDITION_DIAGNOSTIC_RELEASE_IMAGE", "")
		t.Setenv("E2E_CONDITION_DIAGNOSTIC_UPGRADE_TARGET", "")
		scheme := runtime.NewScheme()
		if err := hyperv1.AddToScheme(scheme); err != nil {
			t.Fatal(err)
		}
		hostedCluster := failureConditionFixture(t, "4.14.0", false)
		hostedCluster.Spec.Platform.AWS = &hyperv1.AWSPlatformSpec{EndpointAccess: hyperv1.Public}
		pool := &hyperv1.NodePool{ObjectMeta: metav1.ObjectMeta{Name: "workers", Namespace: hostedCluster.Namespace}, Spec: hyperv1.NodePoolSpec{ClusterName: hostedCluster.Name, Replicas: ptr.To(int32(1))}}
		tc := &internal.TestContext{Context: t.Context(), MgmtClient: fake.NewClientBuilder().WithScheme(scheme).WithObjects(hostedCluster, pool).Build(), ClusterName: hostedCluster.Name, ClusterNamespace: hostedCluster.Namespace, ConditionDiagnosticReleaseVersions: map[string]semver.Version{previous: semver.MustParse("4.22.0"), latest: semver.MustParse("4.21.0")}}
		resetFailureConditionPhase(tc)
		applicable, err := metricsNodeTuningApplicable(tc, hostedCluster)
		if err != nil || !applicable {
			t.Fatalf("actual mapped CPO originating metadata eligibility=%v, error=%v", applicable, err)
		}
	})
}

func TestMetricsNodeTuningProbe(t *testing.T) {
	probeFailure := errors.New("exec unavailable")
	for _, testCase := range []struct {
		name     string
		deadline time.Duration
		canceled bool
		failure  error
	}{
		{name: "When exec succeeds, it should bound and release its attempt"},
		{name: "When exec fails, it should preserve the response and error", failure: probeFailure},
		{name: "When the parent has an earlier deadline, it should retain that bound", deadline: 10 * time.Second},
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
			output, err := metricsNodeTuningProbe(parent, func(ctx context.Context) (string, error) {
				attemptDone = ctx.Done()
				deadline, present := ctx.Deadline()
				if !present || deadline.After(time.Now().Add(30*time.Second)) {
					t.Fatal("exec must have a maximum 30-second deadline")
				}
				if parentDeadline, present := parent.Deadline(); present {
					if !deadline.Equal(parentDeadline) {
						t.Fatal("exec must preserve the earlier parent deadline")
					}
				} else if deadline.Before(before.Add(30 * time.Second)) {
					t.Fatal("exec must retain its full baseline attempt budget")
				}
				if testCase.canceled && !errors.Is(ctx.Err(), context.Canceled) {
					t.Fatal("exec must inherit caller cancellation")
				}
				return "metrics response", testCase.failure
			})
			if output != "metrics response" || !errors.Is(err, testCase.failure) {
				t.Fatalf("probe = %q, %v; response or error changed", output, err)
			}
			select {
			case <-attemptDone:
			default:
				t.Fatal("probe must release its child context before returning")
			}
		})
	}
}

func TestMetricsVectorSize(t *testing.T) {
	for _, testCase := range []struct {
		name, output string
		count        int
		wantError    bool
	}{
		{name: "When a vector contains a series, it should count it", output: `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1,"2"]}]}}`, count: 1},
		{name: "When a successful vector is empty, it should prove absence", output: `{"status":"success","data":{"resultType":"vector","result":[]}}`},
		{name: "When the response is malformed, it should reject it", output: `{`, wantError: true},
		{name: "When Prometheus returns an error, it should not prove absence", output: `{"status":"error","data":{"resultType":"vector","result":[]}}`, wantError: true},
		{name: "When result is missing, it should not prove absence", output: `{"status":"success","data":{"resultType":"vector"}}`, wantError: true},
		{name: "When the result is a scalar, it should reject it", output: `{"status":"success","data":{"resultType":"scalar","result":[]}}`, wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			count, err := metricsVectorSize(testCase.output)
			if (err != nil) != testCase.wantError || count != testCase.count {
				t.Fatalf("metricsVectorSize() = %d, %v; want count %d, error %v", count, err, testCase.count, testCase.wantError)
			}
		})
	}
}

func TestMetricsForwarderTargetUp(t *testing.T) {
	for _, testCase := range []struct {
		name, output  string
		up, wantError bool
	}{
		{name: "When one target matches pool URL and health, it should report up", output: `{"status":"success","data":{"activeTargets":[{"scrapePool":"serviceMonitor/control-plane-metrics-forwarder/0","scrapeUrl":"http://forwarder/metrics/kube-apiserver","health":"up"}]}}`, up: true},
		{name: "When only an unrelated target is healthy, it should report down", output: `{"status":"success","data":{"activeTargets":[{"scrapePool":"control-plane-metrics-forwarder","scrapeUrl":"http://forwarder/metrics/kube-apiserver","health":"down"},{"scrapePool":"other","scrapeUrl":"http://other/metrics","health":"up"}]}}`},
		{name: "When the forwarder target has the wrong URL, it should report down", output: `{"status":"success","data":{"activeTargets":[{"scrapePool":"control-plane-metrics-forwarder","scrapeUrl":"http://forwarder/metrics/etcd","health":"up"}]}}`},
		{name: "When no targets exist, it should report down", output: `{"status":"success","data":{"activeTargets":[]}}`},
		{name: "When the API fails, it should return an error", output: `{"status":"error"}`, wantError: true},
		{name: "When JSON is invalid, it should return an error", output: `not json`, wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			up, err := metricsForwarderTargetUp(testCase.output)
			if up != testCase.up || (err != nil) != testCase.wantError {
				t.Fatalf("metricsForwarderTargetUp() = %v, %v; want %v, error %v", up, err, testCase.up, testCase.wantError)
			}
		})
	}
}

func TestMetricsDeploymentReady(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		mutate func(*appsv1.Deployment)
		ready  bool
	}{
		{name: "When all replicas are updated ready and available, it should report ready", ready: true},
		{name: "When replicas default to one, it should report ready", mutate: func(deployment *appsv1.Deployment) { deployment.Spec.Replicas = nil }, ready: true},
		{name: "When the controller has not observed the generation, it should report unready", mutate: func(deployment *appsv1.Deployment) { deployment.Status.ObservedGeneration = 0 }},
		{name: "When old replicas remain, it should report unready", mutate: func(deployment *appsv1.Deployment) { deployment.Status.UpdatedReplicas = 0 }},
		{name: "When replicas are not ready, it should report unready", mutate: func(deployment *appsv1.Deployment) { deployment.Status.ReadyReplicas = 0 }},
		{name: "When replicas are unavailable, it should report unready", mutate: func(deployment *appsv1.Deployment) {
			deployment.Status.AvailableReplicas = 0
			deployment.Status.UnavailableReplicas = 1
		}},
		{name: "When deployment is scaled to zero, it should not prove a working pipeline", mutate: func(deployment *appsv1.Deployment) {
			deployment.Spec.Replicas = ptr.To(int32(0))
			deployment.Status = appsv1.DeploymentStatus{ObservedGeneration: 1}
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			deployment := &appsv1.Deployment{Spec: appsv1.DeploymentSpec{Replicas: ptr.To(int32(1))}, Status: appsv1.DeploymentStatus{ObservedGeneration: 1, UpdatedReplicas: 1, ReadyReplicas: 1, AvailableReplicas: 1}}
			deployment.Generation = 1
			if testCase.mutate != nil {
				testCase.mutate(deployment)
			}
			if actual := metricsDeploymentReady(deployment); actual != testCase.ready {
				t.Fatalf("metricsDeploymentReady() = %v, want %v", actual, testCase.ready)
			}
		})
	}
}
