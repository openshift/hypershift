//go:build e2ev2

package tests

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/pkg/manifests"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	configv1 "github.com/openshift/api/config/v1"

	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/blang/semver"
)

func TestHealthConfiguredNodePoolIntent(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		expected  map[string]int32
		wantError bool
	}{
		{name: "When independent intent is missing, it should fail before polling", wantError: true},
		{name: "When zero pools are explicitly configured, it should accept the contract", expected: map[string]int32{}},
		{name: "When a configured name is empty, it should reject the contract", expected: map[string]int32{"": 1}, wantError: true},
		{name: "When configured replicas are negative, it should reject the contract", expected: map[string]int32{"pool": -1}, wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if err := healthConfiguredNodePoolIntent(testCase.expected); (err != nil) != testCase.wantError {
				t.Fatalf("intent error = %v, want error %v", err, testCase.wantError)
			}
		})
	}
}

func TestHealthInitialConfiguredWorkersReady(t *testing.T) {
	t.Run("When private resources appear and converge after transient failures, it should read fresh management observations without guest access", func(t *testing.T) {
		scheme := runtime.NewScheme()
		if err := hyperv1.AddToScheme(scheme); err != nil {
			t.Fatal(err)
		}
		client := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&hyperv1.NodePool{}).Build()
		tc := &internal.TestContext{Context: t.Context(), MgmtClient: client, ClusterName: "cluster", ClusterNamespace: "clusters", ExpectedNodePools: map[string]int32{"pool": 1}}
		if err := healthInitialConfiguredWorkersReady(tc); err == nil {
			t.Fatal("missing hosted cluster must fail the observation")
		}
		cluster := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Name: "cluster", Namespace: "clusters"}, Spec: hyperv1.HostedClusterSpec{Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform, AWS: &hyperv1.AWSPlatformSpec{EndpointAccess: hyperv1.Private}}}}
		if err := client.Create(t.Context(), cluster); err != nil {
			t.Fatal(err)
		}
		if err := healthInitialConfiguredWorkersReady(tc); err == nil {
			t.Fatal("missing expected pool must fail the observation")
		}
		pool := &hyperv1.NodePool{ObjectMeta: metav1.ObjectMeta{Name: "pool", Namespace: "clusters"}, Spec: hyperv1.NodePoolSpec{ClusterName: "cluster", Replicas: ptr.To(int32(1))}}
		if err := client.Create(t.Context(), pool); err != nil {
			t.Fatal(err)
		}
		if err := healthInitialConfiguredWorkersReady(tc); err == nil {
			t.Fatal("unconverged replica status must fail the observation")
		}
		pool.Status.Replicas = 1
		if err := client.Status().Update(t.Context(), pool); err != nil {
			t.Fatal(err)
		}
		if err := healthInitialConfiguredWorkersReady(tc); err != nil {
			t.Fatalf("fresh converged private observation must succeed without any kubeconfig or guest client: %v", err)
		}
	})
}

func TestHealthInitialAPIReady(t *testing.T) {
	t.Run("When a private kubeconfig appears after a transient failure, it should parse fresh management data without contacting the guest", func(t *testing.T) {
		scheme := runtime.NewScheme()
		if err := hyperv1.AddToScheme(scheme); err != nil {
			t.Fatal(err)
		}
		if err := corev1.AddToScheme(scheme); err != nil {
			t.Fatal(err)
		}
		cluster := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Name: "cluster", Namespace: "clusters"}, Spec: hyperv1.HostedClusterSpec{Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform, AWS: &hyperv1.AWSPlatformSpec{EndpointAccess: hyperv1.Private}}}, Status: hyperv1.HostedClusterStatus{KubeConfig: &corev1.LocalObjectReference{Name: "admin-kubeconfig"}}}
		client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cluster).Build()
		tc := &internal.TestContext{Context: t.Context(), MgmtClient: client, ClusterName: "cluster", ClusterNamespace: "clusters"}
		if err := healthInitialAPIReady(tc); err == nil {
			t.Fatal("missing kubeconfig secret must fail readiness")
		}
		secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "admin-kubeconfig", Namespace: "clusters"}, Data: map[string][]byte{"kubeconfig": []byte("apiVersion: v1\nkind: Config\nclusters:\n- name: cluster\n  cluster:\n    server: https://unreachable.invalid:6443\ncontexts:\n- name: admin\n  context:\n    cluster: cluster\n    user: admin\ncurrent-context: admin\nusers:\n- name: admin\n  user: {}\n")}}
		if err := client.Create(t.Context(), secret); err != nil {
			t.Fatal(err)
		}
		if err := healthInitialAPIReady(tc); err != nil {
			t.Fatalf("private readiness must parse the newly available kubeconfig without contacting its unreachable endpoint: %v", err)
		}
	})
}

func TestHealthInitialClientReady(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		version     string
		failure     bool
		wantRequest bool
	}{
		{name: "When version is older than 4.15, it should preserve the client-construction path", version: "4.14.0"},
		{name: "When version is 4.15 or newer, it should perform a real SelfSubjectReview request", version: "4.15.0", wantRequest: true},
		{name: "When SelfSubjectReview is rejected, it should propagate readiness failure", version: "4.22.0", wantRequest: true, failure: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			requested := false
			client := fake.NewClientBuilder().WithInterceptorFuncs(interceptor.Funcs{Create: func(_ context.Context, _ crclient.WithWatch, object crclient.Object, _ ...crclient.CreateOption) error {
				if _, correct := object.(*authenticationv1.SelfSubjectReview); !correct {
					t.Fatalf("unexpected readiness object %T", object)
				}
				requested = true
				if testCase.failure {
					return errors.New("request denied")
				}
				return nil
			}}).Build()
			err := healthInitialClientReady(t.Context(), client, semver.MustParse(testCase.version))
			if requested != testCase.wantRequest || (err != nil) != testCase.failure {
				t.Fatalf("request = %v, error = %v", requested, err)
			}
		})
	}
}

func TestHealthInitialDataPlaneCompleted(t *testing.T) {
	image := "quay.io/openshift-release-dev/ocp-release:4.22.0-x86_64"
	for _, testCase := range []struct {
		name        string
		zeroWorkers bool
		mutate      func(*hyperv1.HostedCluster)
		wantError   bool
	}{
		{name: "When configured workers have completed matching rollout, it should pass"},
		{name: "When configured workers are zero, it should not require CVO completion", zeroWorkers: true, mutate: func(cluster *hyperv1.HostedCluster) { cluster.Status = hyperv1.HostedClusterStatus{} }},
		{name: "When configured workers lack version status, it should fail", mutate: func(cluster *hyperv1.HostedCluster) { cluster.Status.Version = nil }, wantError: true},
		{name: "When latest history is stale, it should fail", mutate: func(cluster *hyperv1.HostedCluster) { cluster.Status.Version.History[0].Image = "old-image" }, wantError: true},
		{name: "When latest history is partial, it should fail", mutate: func(cluster *hyperv1.HostedCluster) { cluster.Status.Version.History[0].State = configv1.PartialUpdate }, wantError: true},
		{name: "When Available is absent, it should fail", mutate: func(cluster *hyperv1.HostedCluster) { cluster.Status.Conditions = nil }, wantError: true},
		{name: "When Progressing is true, it should fail", mutate: func(cluster *hyperv1.HostedCluster) { cluster.Status.Conditions[1].Status = metav1.ConditionTrue }, wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			cluster := &hyperv1.HostedCluster{Status: hyperv1.HostedClusterStatus{Version: &hyperv1.ClusterVersionStatus{Desired: configv1.Release{Image: image}, History: []configv1.UpdateHistory{{Image: image, State: configv1.CompletedUpdate}}}, Conditions: []metav1.Condition{{Type: string(hyperv1.HostedClusterAvailable), Status: metav1.ConditionTrue}, {Type: string(hyperv1.HostedClusterProgressing), Status: metav1.ConditionFalse}}}}
			if testCase.mutate != nil {
				testCase.mutate(cluster)
			}
			replicas := int32(1)
			if testCase.zeroWorkers {
				replicas = 0
			}
			if err := healthInitialDataPlaneCompleted(cluster, map[string]int32{"pool": replicas}); (err != nil) != testCase.wantError {
				t.Fatalf("initial rollout error = %v, want error %v", err, testCase.wantError)
			}
		})
	}
}

func TestHealthControlPlaneVersionCompleted(t *testing.T) {
	canonical := "quay.io/openshift-release-dev/ocp-release@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	requested := "quay.io/openshift-release-dev/ocp-release:4.22.0-x86_64"
	for _, testCase := range []struct {
		name      string
		desired   string
		history   []hyperv1.ControlPlaneUpdateHistory
		wantError bool
	}{
		{name: "When tagged release resolves to completed digest status, it should accept it", desired: canonical, history: []hyperv1.ControlPlaneUpdateHistory{{Image: canonical, State: configv1.CompletedUpdate}}},
		{name: "When desired status retains a mutable tag, it should reject it", desired: requested, history: []hyperv1.ControlPlaneUpdateHistory{{Image: canonical, State: configv1.CompletedUpdate}}, wantError: true},
		{name: "When latest history is stale, it should reject older completed entries", desired: canonical, history: []hyperv1.ControlPlaneUpdateHistory{{Image: "quay.io/openshift-release-dev/ocp-release@sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", State: configv1.CompletedUpdate}, {Image: canonical, State: configv1.CompletedUpdate}}, wantError: true},
		{name: "When latest history is partial, it should reject it", desired: canonical, history: []hyperv1.ControlPlaneUpdateHistory{{Image: canonical, State: configv1.PartialUpdate}}, wantError: true},
		{name: "When history is empty, it should reject it", desired: canonical, wantError: true},
		{name: "When desired image is empty, it should reject it", history: []hyperv1.ControlPlaneUpdateHistory{{Image: canonical, State: configv1.CompletedUpdate}}, wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			hostedCluster := &hyperv1.HostedCluster{}
			hostedCluster.Status.ControlPlaneVersion.Desired.Image = testCase.desired
			hostedCluster.Status.ControlPlaneVersion.History = testCase.history
			identity := releaseImageIdentity{Requested: requested, Canonical: canonical}
			if err := healthControlPlaneVersionCompleted(hostedCluster, identity); (err != nil) != testCase.wantError {
				t.Fatalf("control plane version error = %v, want error %v", err, testCase.wantError)
			}
		})
	}
}

func TestHealthControlPlaneVersionApplicable(t *testing.T) {
	for _, testCase := range []struct {
		name, previous, latest, override, target string
		previousVersion, latestVersion, observed string
		wantApplicable, wantError                bool
	}{
		{name: "When independent previous is 4.21 and observed history is 4.23, it should not enable CPV", previous: "previous", latest: "latest", previousVersion: "4.21.0", latestVersion: "4.23.0", observed: "4.23.0"},
		{name: "When independent previous is 4.22 and history is absent, it should enable CPV", previous: "previous", previousVersion: "4.22.0", wantApplicable: true},
		{name: "When independent latest is 4.23 and history is malformed, it should enable CPV", latest: "latest", latestVersion: "4.23.0", observed: "malformed", wantApplicable: true},
		{name: "When independent previous is 4.22 and history contradicts it, it should retain the independent boundary", previous: "previous", previousVersion: "4.22.0", observed: "4.14.0", wantApplicable: true},
		{name: "When proven ordinary CP phase selects latest, it should use latest instead of previous", previous: "previous", latest: "latest", override: "latest", previousVersion: "4.21.0", latestVersion: "4.22.0", observed: "4.21.0", wantApplicable: true},
		{name: "When AutoNode has target but previous gates, it should not switch to latest eligibility", previous: "previous", latest: "latest", target: "latest", previousVersion: "4.21.0", latestVersion: "4.23.0", observed: "4.23.0"},
		{name: "When configured release metadata is unresolved, it should fail without observed fallback", latest: "latest", observed: "4.23.0", wantError: true},
		{name: "When configured release has malformed metadata, it should retain the preparation error instead of falling back", previous: "previous", observed: "4.23.0", wantError: true},
		{name: "When configured override is unsupported, it should fail even with populated latest cache", latest: "latest", override: "other", latestVersion: "4.23.0", observed: "4.23.0", wantError: true},
		{name: "When configured target is unsupported, it should fail before applying an older release boundary", previous: "previous", latest: "latest", target: "other", previousVersion: "4.21.0", observed: "4.23.0", wantError: true},
		{name: "When target is configured without latest, it should fail rather than treat the invocation as standalone", target: "latest", observed: "4.23.0", wantError: true},
		{name: "When standalone observed release is 4.21, it should preserve native ineligibility", observed: "4.21.0"},
		{name: "When standalone observed release is 4.22, it should preserve native eligibility", observed: "4.22.0", wantApplicable: true},
		{name: "When standalone observed history is malformed, it should preserve the native error", observed: "malformed", wantError: true},
		{name: "When standalone observed history is absent, it should preserve the native error", wantError: true},
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
			tc := &internal.TestContext{Context: t.Context(), MgmtClient: client, ClusterName: hc.Name, ClusterNamespace: hc.Namespace, ConditionDiagnosticReleaseVersions: map[string]semver.Version{}, ConditionDiagnosticReleaseErrors: map[string]error{"previous": errors.New("malformed independent metadata")}}
			if testCase.previousVersion != "" {
				tc.ConditionDiagnosticReleaseVersions["previous"] = semver.MustParse(testCase.previousVersion)
			}
			if testCase.latestVersion != "" {
				tc.ConditionDiagnosticReleaseVersions["latest"] = semver.MustParse(testCase.latestVersion)
			}
			resetFailureConditionPhase(tc)
			applicable, err := healthControlPlaneVersionApplicable(tc)
			if applicable != testCase.wantApplicable || (err != nil) != testCase.wantError {
				t.Fatalf("applicable=%v error=%v", applicable, err)
			}
			configured := testCase.previous != "" || testCase.latest != "" || testCase.override != "" || testCase.target != ""
			if configured && reads != 0 || !configured && reads != 1 {
				t.Fatalf("eligibility consulted the wrong origin: management reads=%d configured=%v", reads, configured)
			}
			if testCase.previous != "" && testCase.previousVersion == "" && !strings.Contains(err.Error(), "malformed independent metadata") {
				t.Fatalf("independent metadata error lost: %v", err)
			}
		})
	}
}

func TestWaitForHealthControlPlaneVersion(t *testing.T) {
	const image = "quay.io/openshift-release-dev/ocp-release@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	for _, testCase := range []struct {
		name          string
		completeAfter int
		canceled      bool
		wantError     bool
	}{
		{name: "When initial CPV completes after transient observations, it should retry until completion", completeAfter: 3},
		{name: "When initial CPV stays incomplete, it should retain the bounded assertion failure", wantError: true},
		{name: "When the caller is canceled, it should stop waiting", canceled: true, wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			reads := 0
			client := fake.NewClientBuilder().WithInterceptorFuncs(interceptor.Funcs{Get: func(ctx context.Context, client crclient.WithWatch, key crclient.ObjectKey, object crclient.Object, opts ...crclient.GetOption) error {
				cluster := object.(*hyperv1.HostedCluster)
				reads++
				cluster.Spec.Release.Image = image
				cluster.Status.ControlPlaneVersion.Desired.Image = image
				state := configv1.PartialUpdate
				if testCase.completeAfter > 0 && reads >= testCase.completeAfter {
					state = configv1.CompletedUpdate
				}
				cluster.Status.ControlPlaneVersion.History = []hyperv1.ControlPlaneUpdateHistory{{Image: image, State: state}}
				return nil
			}}).Build()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if testCase.canceled {
				cancel()
			}
			tc := &internal.TestContext{Context: ctx, MgmtClient: client, ClusterNamespace: "clusters", ClusterName: "cluster"}
			err := waitForHealthControlPlaneVersion(tc, 100*time.Millisecond, time.Millisecond)
			if (err != nil) != testCase.wantError {
				t.Fatalf("unexpected CPV wait: %v", err)
			}
			if !testCase.wantError && reads < testCase.completeAfter {
				t.Fatalf("CPV was not retried: %d", reads)
			}
			if testCase.canceled && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", err)
			}
		})
	}
}

func TestHealthControlPlaneVersionCheck(t *testing.T) {
	canonical := "quay.io/openshift-release-dev/ocp-release@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	for _, testCase := range []struct {
		name, target, requested string
		cpc                     bool
		status                  metav1.ConditionStatus
		apiError, complete      bool
		wantError               bool
	}{
		{name: "When target is not yet requested, it should defer missing CPV and malformed image identity", target: canonical, requested: "invalid image"},
		{name: "When target CPO is missing, it should defer missing CPV", target: canonical, requested: canonical},
		{name: "When target CPO has not rolled, it should defer missing CPV", target: canonical, requested: canonical, cpc: true, status: metav1.ConditionFalse},
		{name: "When target CPO rolled and CPV is missing, it should enforce immutable CPV", target: canonical, requested: canonical, cpc: true, status: metav1.ConditionTrue, wantError: true},
		{name: "When target CPO rolled and CPV completed, it should pass immutable CPV", target: canonical, requested: canonical, cpc: true, status: metav1.ConditionTrue, complete: true},
		{name: "When CPO lookup fails, it should preserve the API error", target: canonical, requested: canonical, apiError: true, wantError: true},
		{name: "When no target is configured and CPV is missing, it should enforce normal CPV health", requested: canonical, wantError: true},
		{name: "When standalone CPV completed, it should retain the strong immutable check", requested: canonical, complete: true},
		{name: "When no target is configured and image is malformed, it should retain the image error", requested: "invalid image", complete: true, wantError: true},
		{name: "When no target is configured and a tag cannot be resolved, it should retain the registry prerequisite error", requested: "quay.io/openshift-release-dev/ocp-release:4.22.0-x86_64", wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := hyperv1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			hc := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Name: "cluster", Namespace: "clusters"}, Spec: hyperv1.HostedClusterSpec{Release: hyperv1.Release{Image: testCase.requested}}}
			if testCase.complete {
				hc.Status.ControlPlaneVersion.Desired.Image = canonical
				hc.Status.ControlPlaneVersion.History = []hyperv1.ControlPlaneUpdateHistory{{Image: canonical, State: configv1.CompletedUpdate}}
			}
			builder := fake.NewClientBuilder().WithScheme(scheme).WithObjects(hc)
			if testCase.cpc {
				builder.WithObjects(&hyperv1.ControlPlaneComponent{ObjectMeta: metav1.ObjectMeta{Name: "control-plane-operator", Namespace: manifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name)}, Status: hyperv1.ControlPlaneComponentStatus{Conditions: []metav1.Condition{{Type: string(hyperv1.ControlPlaneComponentRolloutComplete), Status: testCase.status}}}})
			}
			cpcReads := 0
			client := builder.WithInterceptorFuncs(interceptor.Funcs{Get: func(ctx context.Context, client crclient.WithWatch, key crclient.ObjectKey, object crclient.Object, opts ...crclient.GetOption) error {
				if _, cpc := object.(*hyperv1.ControlPlaneComponent); cpc {
					cpcReads++
					if testCase.apiError {
						return errors.New("CPO read unavailable")
					}
				}
				return client.Get(ctx, key, object, opts...)
			}}).Build()
			tc := &internal.TestContext{Context: t.Context(), MgmtClient: client, ClusterName: hc.Name, ClusterNamespace: hc.Namespace, ConditionDiagnosticUpgradeTarget: testCase.target}
			err := healthControlPlaneVersionCheck(tc)(t.Context())
			if (err != nil) != testCase.wantError {
				t.Fatalf("CPV check error=%v", err)
			}
			if testCase.apiError && !strings.Contains(err.Error(), "CPO read unavailable") {
				t.Fatalf("CPO API error lost: %v", err)
			}
			if (testCase.target == "" || testCase.target != testCase.requested) && cpcReads != 0 {
				t.Fatal("inapplicable CPO lookup occurred")
			}
		})
	}
	t.Run("When target readiness regresses after enforcement, it should keep the latch and fetch fresh HC on every observation", func(t *testing.T) {
		hc := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Name: "cluster", Namespace: "clusters"}, Spec: hyperv1.HostedClusterSpec{Release: hyperv1.Release{Image: canonical}}}
		clusterReads, cpcReads := 0, 0
		client := fake.NewClientBuilder().WithInterceptorFuncs(interceptor.Funcs{Get: func(ctx context.Context, client crclient.WithWatch, key crclient.ObjectKey, object crclient.Object, opts ...crclient.GetOption) error {
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
				t.Fatalf("unexpected lookup %T", object)
			}
			return nil
		}}).Build()
		tc := &internal.TestContext{Context: t.Context(), MgmtClient: client, ClusterName: hc.Name, ClusterNamespace: hc.Namespace, ConditionDiagnosticUpgradeTarget: canonical}
		check := healthControlPlaneVersionCheck(tc)
		if err := check(t.Context()); err == nil {
			t.Fatal("ready target with missing CPV passed")
		}
		hc.Spec.Release.Image = "quay.io/openshift-release-dev/ocp-release@sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
		if err := check(t.Context()); err == nil || !strings.Contains(err.Error(), "control plane desired image") {
			t.Fatalf("regressed readiness bypassed CPV: %v", err)
		}
		hc.Spec.Release.Image = canonical
		hc.Status.ControlPlaneVersion.Desired.Image = canonical
		hc.Status.ControlPlaneVersion.History = []hyperv1.ControlPlaneUpdateHistory{{Image: canonical, State: configv1.CompletedUpdate}}
		if err := check(t.Context()); err != nil || clusterReads != 3 || cpcReads != 1 {
			t.Fatalf("fresh enforced CPV error=%v cluster reads=%d CPO reads=%d", err, clusterReads, cpcReads)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := check(ctx); !errors.Is(err, context.Canceled) || clusterReads != 3 {
			t.Fatalf("cancellation did not prevent API access: %v reads=%d", err, clusterReads)
		}
	})
}

func TestHealthControlPlaneVersionTimeout(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		pools     map[string]int32
		expected  time.Duration
		wantError bool
		phase     string
	}{
		{name: "When standalone intent is absent, it should use the ordinary ten minute budget without discovery", expected: 10 * time.Minute},
		{name: "When explicit initial intent has no pools, it should use the zero worker twenty minute budget", pools: map[string]int32{}, expected: 20 * time.Minute},
		{name: "When all configured replicas are zero, it should use twenty minutes", pools: map[string]int32{"zone-a": 0, "zone-b": 0}, expected: 20 * time.Minute},
		{name: "When positive workers are configured but none are observed, it should retain ten minutes from independent intent", pools: map[string]int32{"zone-a": 1}, expected: 10 * time.Minute},
		{name: "When zero and positive pools coexist, it should retain ten minutes", pools: map[string]int32{"zone-a": 0, "zone-b": 1}, expected: 10 * time.Minute},
		{name: "When a configured count is negative, it should reject invalid intent", pools: map[string]int32{"zone-a": -1}, wantError: true},
		{name: "When a configured pool name is empty, it should reject invalid intent", pools: map[string]int32{"": 1}, wantError: true},
		{name: "When successful after has initially zero workers, it should use ten minutes", pools: map[string]int32{}, phase: "after", expected: 10 * time.Minute},
		{name: "When successful after has initially positive workers, it should use ten minutes", pools: map[string]int32{"pool": 1}, phase: "after", expected: 10 * time.Minute},
		{name: "When worker phase is unsupported, it should fail instead of using an initial budget", phase: "before", wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("E2E_HEALTH_WORKER_PHASE", testCase.phase)
			timeout, err := healthControlPlaneVersionTimeout(testCase.pools)
			if timeout != testCase.expected || (err != nil) != testCase.wantError {
				t.Fatalf("timeout=%s error=%v", timeout, err)
			}
		})
	}
}

func TestHealthConditionInputs(t *testing.T) {
	for _, testCase := range []struct {
		name          string
		expected      map[string]int32
		previous      string
		latest        string
		override      string
		target        string
		legacyPrivate bool
		legacyWorkers int32
		wantVersion   string
		wantWorkers   bool
		wantTimeout   time.Duration
		wantError     bool
		phase         string
	}{
		{name: "When positive workers are configured but HC and Nodes are absent, it should retain positive polarity and ten minutes", expected: map[string]int32{"zone-a": 1}, previous: "4.21.0", wantVersion: "4.21.0", wantWorkers: true, wantTimeout: 10 * time.Minute},
		{name: "When configured intent is explicitly empty, it should use no worker polarity and twenty minutes", expected: map[string]int32{}, previous: "4.22.0", wantVersion: "4.22.0", wantTimeout: 20 * time.Minute},
		{name: "When all configured zones have zero replicas, it should use twenty minutes", expected: map[string]int32{"zone-a": 0, "zone-b": 0}, previous: "4.23.0", wantVersion: "4.23.0", wantTimeout: 20 * time.Minute},
		{name: "When any configured zone is positive, it should retain positive polarity and ten minutes", expected: map[string]int32{"zone-a": 0, "zone-b": 2}, previous: "4.23.0", wantVersion: "4.23.0", wantWorkers: true, wantTimeout: 10 * time.Minute},
		{name: "When proven ordinary CP phase selects latest, it should use latest condition gates", expected: map[string]int32{}, previous: "4.21.0", latest: "4.23.0", override: "latest", target: "latest", wantVersion: "4.23.0", wantTimeout: 20 * time.Minute},
		{name: "When AutoNode has a target without release override, it should retain previous condition gates", expected: map[string]int32{}, previous: "4.21.0", latest: "4.23.0", target: "latest", wantVersion: "4.21.0", wantTimeout: 20 * time.Minute},
		{name: "When configured metadata is unavailable, it should fail without observed fallback", expected: map[string]int32{}, wantError: true},
		{name: "When a configured count is negative, it should fail before discovery", expected: map[string]int32{"pool": -1}, previous: "4.23.0", wantError: true},
		{name: "When a configured pool name is empty, it should fail before discovery", expected: map[string]int32{"": 1}, previous: "4.23.0", wantError: true},
		{name: "When standalone private intent is absent with desired workers, it should retain discovered polarity and observed version", legacyPrivate: true, legacyWorkers: 1, wantVersion: "4.22.0", wantWorkers: true, wantTimeout: 10 * time.Minute},
		{name: "When standalone private intent is absent without desired workers, it should retain twenty minutes", legacyPrivate: true, wantVersion: "4.22.0", wantTimeout: 20 * time.Minute},
		{name: "When private before initially zero now has workers, it should retain zero polarity and twenty minutes", legacyPrivate: true, expected: map[string]int32{}, legacyWorkers: 1, wantVersion: "4.22.0", wantTimeout: 20 * time.Minute},
		{name: "When private after initially zero now has workers, it should assert current positive polarity and ten minutes", legacyPrivate: true, expected: map[string]int32{}, legacyWorkers: 1, phase: "after", wantVersion: "4.22.0", wantWorkers: true, wantTimeout: 10 * time.Minute},
		{name: "When private before initially positive now has zero workers, it should retain positive polarity and ten minutes", legacyPrivate: true, expected: map[string]int32{"pool": 1}, wantVersion: "4.22.0", wantWorkers: true, wantTimeout: 10 * time.Minute},
		{name: "When private after initially positive now has zero workers, it should assert zero polarity with ten minutes", legacyPrivate: true, expected: map[string]int32{"pool": 1}, phase: "after", wantVersion: "4.22.0", wantTimeout: 10 * time.Minute},
		{name: "When the worker phase is unsupported, it should fail without discovery", expected: map[string]int32{}, phase: "unknown", wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("E2E_HEALTH_WORKER_PHASE", testCase.phase)
			t.Setenv("E2E_PREVIOUS_RELEASE_IMAGE", "previous")
			t.Setenv("E2E_LATEST_RELEASE_IMAGE", "latest")
			t.Setenv("E2E_CONDITION_DIAGNOSTIC_RELEASE_IMAGE", testCase.override)
			t.Setenv("E2E_CONDITION_DIAGNOSTIC_UPGRADE_TARGET", testCase.target)
			client := fake.NewClientBuilder().Build()
			if testCase.legacyPrivate {
				t.Setenv("E2E_PREVIOUS_RELEASE_IMAGE", "")
				t.Setenv("E2E_LATEST_RELEASE_IMAGE", "")
				scheme := runtime.NewScheme()
				if err := hyperv1.AddToScheme(scheme); err != nil {
					t.Fatal(err)
				}
				hc := failureConditionFixture(t, "4.22.0", testCase.legacyWorkers > 0)
				hc.Spec.Platform.AWS = &hyperv1.AWSPlatformSpec{EndpointAccess: hyperv1.Private}
				hc.Status.Conditions = append(hc.Status.Conditions, metav1.Condition{Type: string(hyperv1.AWSEndpointAvailable), Status: metav1.ConditionTrue}, metav1.Condition{Type: string(hyperv1.AWSEndpointServiceAvailable), Status: metav1.ConditionTrue})
				pool := &hyperv1.NodePool{ObjectMeta: metav1.ObjectMeta{Name: "pool", Namespace: hc.Namespace}, Spec: hyperv1.NodePoolSpec{ClusterName: hc.Name, Replicas: ptr.To(testCase.legacyWorkers)}}
				client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(hc, pool).Build()
			}
			tc := &internal.TestContext{Context: t.Context(), MgmtClient: client, ClusterName: "cluster", ClusterNamespace: "clusters", ExpectedNodePools: testCase.expected, ConditionDiagnosticReleaseVersions: map[string]semver.Version{}}
			if testCase.previous != "" {
				tc.ConditionDiagnosticReleaseVersions["previous"] = semver.MustParse(testCase.previous)
			}
			if testCase.latest != "" {
				tc.ConditionDiagnosticReleaseVersions["latest"] = semver.MustParse(testCase.latest)
			}
			resetFailureConditionPhase(tc)
			version, workers, timeout, err := healthConditionInputs(tc)
			if (err != nil) != testCase.wantError {
				t.Fatalf("condition input error=%v", err)
			}
			if !testCase.wantError && (version.String() != testCase.wantVersion || workers != testCase.wantWorkers || timeout != testCase.wantTimeout) {
				t.Fatalf("version=%s workers=%v timeout=%s", version, workers, timeout)
			}
			if testCase.phase == "after" {
				if err := healthConditionsCheck(tc, version, workers)(t.Context()); err != nil {
					t.Fatalf("actual after leaf rejected current private worker conditions: %v", err)
				}
			}
		})
	}
}

func TestHealthConditionInputsPublicDiscovery(t *testing.T) {
	for _, count := range []int{0, 1} {
		for _, phase := range []string{"standalone", "before", "after"} {
			t.Run(fmt.Sprintf("When public %s returns %d current Nodes, it should preserve phase worker polarity and timing", phase, count), func(t *testing.T) {
				t.Setenv("E2E_HEALTH_WORKER_PHASE", "")
				if phase == "after" {
					t.Setenv("E2E_HEALTH_WORKER_PHASE", "after")
				}
				for _, input := range []string{"E2E_PREVIOUS_RELEASE_IMAGE", "E2E_LATEST_RELEASE_IMAGE", "E2E_CONDITION_DIAGNOSTIC_RELEASE_IMAGE", "E2E_CONDITION_DIAGNOSTIC_UPGRADE_TARGET"} {
					t.Setenv(input, "")
				}
				server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
					writer.Header().Set("Content-Type", "application/json")
					var response interface{}
					switch request.URL.Path {
					case "/api":
						response = metav1.APIVersions{Versions: []string{"v1"}}
					case "/apis":
						response = metav1.APIGroupList{}
					case "/api/v1":
						response = metav1.APIResourceList{GroupVersion: "v1", APIResources: []metav1.APIResource{{Name: "nodes", Kind: "Node"}}}
					case "/api/v1/nodes":
						response = &corev1.NodeList{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "NodeList"}, Items: make([]corev1.Node, count)}
					default:
						http.NotFound(writer, request)
						return
					}
					if err := json.NewEncoder(writer).Encode(response); err != nil {
						t.Errorf("mock guest response: %v", err)
					}
				}))
				defer server.Close()
				scheme := runtime.NewScheme()
				if err := hyperv1.AddToScheme(scheme); err != nil {
					t.Fatal(err)
				}
				if err := corev1.AddToScheme(scheme); err != nil {
					t.Fatal(err)
				}
				hc := failureConditionFixture(t, "4.23.0", count > 0)
				hc.Status.KubeConfig = &corev1.LocalObjectReference{Name: "admin-kubeconfig"}
				hc.Spec.Platform.AWS = &hyperv1.AWSPlatformSpec{EndpointAccess: hyperv1.Public}
				secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "admin-kubeconfig", Namespace: hc.Namespace}, Data: map[string][]byte{"kubeconfig": []byte(fmt.Sprintf("apiVersion: v1\nkind: Config\nclusters:\n- name: cluster\n  cluster:\n    server: %s\ncontexts:\n- name: admin\n  context:\n    cluster: cluster\n    user: admin\ncurrent-context: admin\nusers:\n- name: admin\n  user: {}\n", server.URL))}}
				client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(hc, secret).Build()
				tc := &internal.TestContext{Context: t.Context(), MgmtClient: client, ClusterName: hc.Name, ClusterNamespace: hc.Namespace}
				if phase != "standalone" {
					tc.ExpectedNodePools = map[string]int32{"pool": int32(1 - count)}
				}
				version, workers, timeout, err := healthConditionInputs(tc)
				wantWorkers := count > 0
				if phase == "before" {
					wantWorkers = !wantWorkers
				}
				wantTimeout := 10 * time.Minute
				if phase != "after" && !wantWorkers {
					wantTimeout = 20 * time.Minute
				}
				if err != nil || version.String() != "4.23.0" || workers != wantWorkers || timeout != wantTimeout {
					t.Fatalf("standalone public version=%s workers=%v timeout=%s error=%v", version, workers, timeout, err)
				}
				conditionErr := healthConditionsCheck(tc, version, workers)(t.Context())
				if (conditionErr != nil) != (phase == "before") {
					t.Fatalf("actual condition callback must distinguish initial from changed current workers: %v", conditionErr)
				}
			})
		}
	}
}

func TestHealthInitialAPIReadyIndependentRelease(t *testing.T) {
	for _, testCase := range []struct {
		name, version, observed string
		wantRequest, wantError  bool
	}{
		{name: "When independent 4.14 contradicts observed 4.23, it should retain the pre SSR boundary", version: "4.14.0", observed: "4.23.0"},
		{name: "When independent 4.15 has missing observed history, it should assert real SSR readiness", version: "4.15.0", wantRequest: true},
		{name: "When independent 4.15 has malformed observed history, it should assert real SSR readiness", version: "4.15.0", observed: "malformed", wantRequest: true},
		{name: "When independent metadata is unresolved, it should fail without observed fallback", observed: "4.23.0", wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("E2E_PREVIOUS_RELEASE_IMAGE", "previous")
			t.Setenv("E2E_LATEST_RELEASE_IMAGE", "")
			t.Setenv("E2E_CONDITION_DIAGNOSTIC_RELEASE_IMAGE", "")
			t.Setenv("E2E_CONDITION_DIAGNOSTIC_UPGRADE_TARGET", "")
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				var response interface{}
				switch request.URL.Path {
				case "/api":
					response = metav1.APIVersions{Versions: []string{"v1"}}
				case "/apis":
					response = metav1.APIGroupList{Groups: []metav1.APIGroup{{Name: "authentication.k8s.io", Versions: []metav1.GroupVersionForDiscovery{{GroupVersion: "authentication.k8s.io/v1", Version: "v1"}}, PreferredVersion: metav1.GroupVersionForDiscovery{GroupVersion: "authentication.k8s.io/v1", Version: "v1"}}}}
				case "/api/v1":
					response = metav1.APIResourceList{GroupVersion: "v1"}
				case "/apis/authentication.k8s.io/v1":
					response = metav1.APIResourceList{GroupVersion: "authentication.k8s.io/v1", APIResources: []metav1.APIResource{{Name: "selfsubjectreviews", Kind: "SelfSubjectReview"}}}
				case "/apis/authentication.k8s.io/v1/selfsubjectreviews":
					if request.Method != http.MethodPost {
						t.Errorf("SSR readiness must POST, got %s", request.Method)
					}
					requests.Add(1)
					response = authenticationv1.SelfSubjectReview{TypeMeta: metav1.TypeMeta{APIVersion: "authentication.k8s.io/v1", Kind: "SelfSubjectReview"}}
				default:
					http.NotFound(writer, request)
					return
				}
				if err := json.NewEncoder(writer).Encode(response); err != nil {
					t.Errorf("mock SSR response: %v", err)
				}
			}))
			defer server.Close()
			scheme := runtime.NewScheme()
			if err := hyperv1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			if err := corev1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			hc := failureConditionFixture(t, "4.23.0", false)
			hc.Spec.Platform.AWS = &hyperv1.AWSPlatformSpec{EndpointAccess: hyperv1.Public}
			hc.Status.Version = nil
			if testCase.observed != "" {
				hc.Status.Version = &hyperv1.ClusterVersionStatus{History: []configv1.UpdateHistory{{Version: testCase.observed}}}
			}
			hc.Status.KubeConfig = &corev1.LocalObjectReference{Name: "admin-kubeconfig"}
			secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "admin-kubeconfig", Namespace: hc.Namespace}, Data: map[string][]byte{"kubeconfig": []byte(fmt.Sprintf("apiVersion: v1\nkind: Config\nclusters:\n- name: cluster\n  cluster:\n    server: %s\ncontexts:\n- name: admin\n  context:\n    cluster: cluster\n    user: admin\ncurrent-context: admin\nusers:\n- name: admin\n  user: {}\n", server.URL))}}
			tc := &internal.TestContext{Context: t.Context(), MgmtClient: fake.NewClientBuilder().WithScheme(scheme).WithObjects(hc, secret).Build(), ClusterName: hc.Name, ClusterNamespace: hc.Namespace, ConditionDiagnosticReleaseVersions: map[string]semver.Version{}}
			if testCase.version != "" {
				tc.ConditionDiagnosticReleaseVersions["previous"] = semver.MustParse(testCase.version)
			}
			resetFailureConditionPhase(tc)
			err := healthInitialAPIReady(tc)
			if (err != nil) != testCase.wantError || (requests.Load() > 0) != testCase.wantRequest {
				t.Fatalf("SSR requests=%d readiness error=%v", requests.Load(), err)
			}
		})
	}
}

func TestHealthAfterWorkers(t *testing.T) {
	for _, testCase := range []struct {
		name, phase      string
		after, wantError bool
	}{
		{name: "When phase is absent, it should preserve initial or standalone compatibility"},
		{name: "When phase is after, it should select current workers", phase: "after", after: true},
		{name: "When phase is unknown, it should reject the configured input", phase: "unknown", wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("E2E_HEALTH_WORKER_PHASE", testCase.phase)
			after, err := healthAfterWorkers()
			if after != testCase.after || (err != nil) != testCase.wantError {
				t.Fatalf("after=%v error=%v", after, err)
			}
		})
	}
}

func TestHealthObservedWorkers(t *testing.T) {
	for _, testCase := range []struct {
		name                                            string
		pools                                           []hyperv1.NodePool
		missing, readError, listError, canceled, public bool
		wantWorkers, wantError                          bool
	}{
		{name: "When private current pools are absent, it should report no workers"},
		{name: "When a positive pool belongs to another cluster, it should not infer workers", pools: []hyperv1.NodePool{{Spec: hyperv1.NodePoolSpec{ClusterName: "other", Replicas: ptr.To(int32(1))}}}},
		{name: "When a matching private pool now desires workers, it should report them without guest access", pools: []hyperv1.NodePool{{Spec: hyperv1.NodePoolSpec{ClusterName: "cluster", Replicas: ptr.To(int32(1))}}}, wantWorkers: true},
		{name: "When matching private autoscaling has positive maximum with zero minimum, it should retain pinned semantics", pools: []hyperv1.NodePool{{Spec: hyperv1.NodePoolSpec{ClusterName: "cluster", AutoScaling: &hyperv1.NodePoolAutoScaling{Min: ptr.To(int32(0)), Max: 3}}}}, wantWorkers: true},
		{name: "When matching private autoscaling has zero maximum, it should report no workers", pools: []hyperv1.NodePool{{Spec: hyperv1.NodePoolSpec{ClusterName: "cluster", AutoScaling: &hyperv1.NodePoolAutoScaling{Max: 0}}}}},
		{name: "When HC is missing, it should preserve observation failure", missing: true, wantError: true},
		{name: "When HC read fails, it should preserve API error", readError: true, wantError: true},
		{name: "When private pool listing fails, it should preserve API error", listError: true, wantError: true},
		{name: "When public kubeconfig is missing, it should fail instead of treating missing workers as zero", public: true, wantError: true},
		{name: "When observation context is canceled, it should avoid all management calls", canceled: true, wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := hyperv1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			hc := failureConditionFixture(t, "4.23.0", false)
			hc.Spec.Platform.AWS = &hyperv1.AWSPlatformSpec{EndpointAccess: hyperv1.Private}
			if testCase.public {
				hc.Spec.Platform.AWS.EndpointAccess = hyperv1.Public
			}
			reads := 0
			builder := fake.NewClientBuilder().WithScheme(scheme)
			if !testCase.missing {
				builder.WithObjects(hc)
			}
			client := builder.WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, client crclient.WithWatch, key crclient.ObjectKey, object crclient.Object, opts ...crclient.GetOption) error {
					reads++
					if testCase.readError {
						return errors.New("current worker HC read failed")
					}
					return client.Get(ctx, key, object, opts...)
				},
				List: func(_ context.Context, _ crclient.WithWatch, list crclient.ObjectList, _ ...crclient.ListOption) error {
					reads++
					if testCase.listError {
						return errors.New("current worker pool listing failed")
					}
					list.(*hyperv1.NodePoolList).Items = testCase.pools
					return nil
				},
			}).Build()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if testCase.canceled {
				cancel()
			}
			tc := &internal.TestContext{Context: ctx, MgmtClient: client, ClusterName: hc.Name, ClusterNamespace: hc.Namespace}
			workers, err := healthObservedWorkers(tc)
			if workers != testCase.wantWorkers || (err != nil) != testCase.wantError {
				t.Fatalf("current workers=%v error=%v", workers, err)
			}
			if testCase.canceled && (reads != 0 || !errors.Is(err, context.Canceled)) {
				t.Fatalf("canceled observation reads=%d error=%v", reads, err)
			}
		})
	}
}

func TestHealthConditionsCheck(t *testing.T) {
	for _, testCase := range []struct {
		name, version, observed string
		workers, target         bool
		missing                 hyperv1.ConditionType
		wrong                   metav1.ConditionStatus
		wantError               bool
	}{
		{name: "When independent 4.14 has absent history, it should preserve introduction masks", version: "4.14.0"},
		{name: "When independent 4.20 has malformed history, it should preserve introduction masks", version: "4.20.0", observed: "malformed"},
		{name: "When independent 4.21 has older observed history, it should require data plane connection", version: "4.21.0", observed: "4.14.0", workers: true, missing: hyperv1.DataPlaneConnectionAvailable, wantError: true},
		{name: "When independent 4.22 lacks control plane connection, it should fail despite observed history", version: "4.22.0", workers: true, missing: hyperv1.ControlPlaneConnectionAvailable, wantError: true},
		{name: "When independent 4.23 lacks reconciliation, it should fail", version: "4.23.0", workers: true, missing: hyperv1.ConfigOperatorReconciliationSucceeded, wantError: true},
		{name: "When ordinary CP or AutoNode target exists, it should apply only the two temporary masks", version: "4.23.0", target: true},
		{name: "When target mode lacks a mandatory connection, it should still fail", version: "4.23.0", workers: true, target: true, missing: hyperv1.DataPlaneConnectionAvailable, wantError: true},
		{name: "When a mandatory condition is False, it should fail", version: "4.23.0", workers: true, missing: hyperv1.HostedClusterAvailable, wrong: metav1.ConditionFalse, wantError: true},
		{name: "When a mandatory condition is Unknown, it should fail", version: "4.23.0", workers: true, missing: hyperv1.HostedClusterAvailable, wrong: metav1.ConditionUnknown, wantError: true},
		{name: "When no target exists and a masked upgrade field is absent, it should enforce steady state", version: "4.23.0", missing: hyperv1.ControlPlaneConnectionAvailable, wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			hc := failureConditionFixture(t, testCase.version, testCase.workers)
			hc.Status.Version = nil
			if testCase.observed != "" {
				hc.Status.Version = &hyperv1.ClusterVersionStatus{History: []configv1.UpdateHistory{{Version: testCase.observed}}}
			}
			conditions := hc.Status.Conditions[:0]
			for _, condition := range hc.Status.Conditions {
				if testCase.target && (condition.Type == string(hyperv1.ControlPlaneConnectionAvailable) || condition.Type == string(hyperv1.ConfigOperatorReconciliationSucceeded)) {
					continue
				}
				if condition.Type == string(testCase.missing) {
					if testCase.wrong == "" {
						continue
					}
					condition.Status = testCase.wrong
				}
				conditions = append(conditions, condition)
			}
			hc.Status.Conditions = conditions
			scheme := runtime.NewScheme()
			if err := hyperv1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			tc := &internal.TestContext{Context: t.Context(), MgmtClient: fake.NewClientBuilder().WithScheme(scheme).WithObjects(hc).Build(), ClusterName: hc.Name, ClusterNamespace: hc.Namespace}
			if testCase.target {
				tc.ConditionDiagnosticUpgradeTarget = "latest"
			}
			if err := healthConditionsCheck(tc, semver.MustParse(testCase.version), testCase.workers)(t.Context()); (err != nil) != testCase.wantError {
				t.Fatalf("condition health error=%v", err)
			}
		})
	}
	t.Run("When fresh platform and KMS requirements change after an API failure, it should recompute expectations and respect cancellation", func(t *testing.T) {
		hc := failureConditionFixture(t, "4.23.0", true)
		reads := 0
		client := fake.NewClientBuilder().WithInterceptorFuncs(interceptor.Funcs{Get: func(ctx context.Context, client crclient.WithWatch, key crclient.ObjectKey, object crclient.Object, opts ...crclient.GetOption) error {
			reads++
			if reads == 1 {
				return errors.New("transient condition read failure")
			}
			hc.DeepCopyInto(object.(*hyperv1.HostedCluster))
			return nil
		}}).Build()
		tc := &internal.TestContext{Context: t.Context(), MgmtClient: client, ClusterName: hc.Name, ClusterNamespace: hc.Namespace}
		check := healthConditionsCheck(tc, semver.MustParse("4.23.0"), true)
		if err := check(t.Context()); err == nil || !strings.Contains(err.Error(), "transient condition read failure") {
			t.Fatalf("API error was not preserved: %v", err)
		}
		if err := check(t.Context()); err != nil {
			t.Fatal(err)
		}
		hc.Spec.Platform.AWS = &hyperv1.AWSPlatformSpec{EndpointAccess: hyperv1.Private}
		if err := check(t.Context()); err == nil || !strings.Contains(err.Error(), string(hyperv1.AWSEndpointAvailable)) {
			t.Fatalf("fresh endpoint requirements were frozen: %v", err)
		}
		hc.Spec.Platform.AWS.EndpointAccess = hyperv1.Public
		hc.Spec.SecretEncryption = &hyperv1.SecretEncryptionSpec{KMS: &hyperv1.KMSSpec{AWS: &hyperv1.AWSKMSSpec{}}}
		if err := check(t.Context()); err == nil || !strings.Contains(err.Error(), string(hyperv1.ValidAWSKMSConfig)) {
			t.Fatalf("fresh KMS expected status was frozen: %v", err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := check(ctx); !errors.Is(err, context.Canceled) || reads != 4 {
			t.Fatalf("canceled poll performed an API lookup: error=%v reads=%d", err, reads)
		}
	})
}

func TestHealthExpectedConditions(t *testing.T) {
	for _, testCase := range []struct {
		name, version string
		workers       bool
		expected      map[hyperv1.ConditionType]metav1.ConditionStatus
		absent        []hyperv1.ConditionType
	}{
		{name: "When workers exist on 4.23, it should require healthy connections and reconciliation", version: "4.23.0", workers: true, expected: map[hyperv1.ConditionType]metav1.ConditionStatus{hyperv1.ClusterVersionAvailable: metav1.ConditionTrue, hyperv1.ClusterVersionSucceeding: metav1.ConditionTrue, hyperv1.ClusterVersionProgressing: metav1.ConditionFalse, hyperv1.DataPlaneConnectionAvailable: metav1.ConditionTrue, hyperv1.ControlPlaneConnectionAvailable: metav1.ConditionTrue, hyperv1.ConfigOperatorReconciliationSucceeded: metav1.ConditionTrue}},
		{name: "When no workers exist on 4.23, it should require baseline zero-worker polarities", version: "4.23.0", expected: map[hyperv1.ConditionType]metav1.ConditionStatus{hyperv1.ClusterVersionAvailable: metav1.ConditionFalse, hyperv1.ClusterVersionSucceeding: metav1.ConditionFalse, hyperv1.ClusterVersionProgressing: metav1.ConditionTrue, hyperv1.DataPlaneConnectionAvailable: metav1.ConditionUnknown, hyperv1.ControlPlaneConnectionAvailable: metav1.ConditionUnknown}, absent: []hyperv1.ConditionType{hyperv1.ValidKubeVirtInfraNetworkMTU, hyperv1.ValidKubeVirtInfraNetworkPolicyRBAC}},
		{name: "When release predates 4.21, it should not require connection conditions", version: "4.20.0", absent: []hyperv1.ConditionType{hyperv1.DataPlaneConnectionAvailable, hyperv1.ControlPlaneConnectionAvailable, hyperv1.ConfigOperatorReconciliationSucceeded}},
		{name: "When release is 4.21, it should require only data plane connection", version: "4.21.0", expected: map[hyperv1.ConditionType]metav1.ConditionStatus{hyperv1.DataPlaneConnectionAvailable: metav1.ConditionUnknown}, absent: []hyperv1.ConditionType{hyperv1.ControlPlaneConnectionAvailable, hyperv1.ConfigOperatorReconciliationSucceeded}},
		{name: "When release is 4.22, it should not require 4.23 reconciliation", version: "4.22.0", expected: map[hyperv1.ConditionType]metav1.ConditionStatus{hyperv1.ControlPlaneConnectionAvailable: metav1.ConditionUnknown}, absent: []hyperv1.ConditionType{hyperv1.ConfigOperatorReconciliationSucceeded}},
		{name: "When release predates 4.15, it should omit KubeVirt network MTU", version: "4.14.0", workers: true, absent: []hyperv1.ConditionType{hyperv1.ValidKubeVirtInfraNetworkMTU}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			hc := &hyperv1.HostedCluster{}
			hc.Spec.Platform.Type = hyperv1.AWSPlatform
			actual := healthExpectedConditions(hc, testCase.workers, semver.MustParse(testCase.version))
			for condition, status := range testCase.expected {
				if actual[condition] != status {
					t.Errorf("condition %s = %s, want %s", condition, actual[condition], status)
				}
			}
			for _, condition := range append(testCase.absent, hyperv1.KubeVirtNodesLiveMigratable) {
				if _, exists := actual[condition]; exists {
					t.Errorf("unexpected condition %s", condition)
				}
			}
			if actual[hyperv1.HostedClusterAvailable] != metav1.ConditionTrue {
				t.Error("zero-worker overrides must preserve control plane availability")
			}
		})
	}
}

func TestHealthHasWorkers(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		private     bool
		poolCluster string
		replicas    *int32
		autoscaling *hyperv1.NodePoolAutoScaling
		autoNode    bool
		nodes       []corev1.Node
		expected    bool
	}{
		{name: "When no pools or nodes exist, it should report no workers"},
		{name: "When a public pool desires workers without actual nodes, it should report no workers", poolCluster: "cluster", replicas: ptr.To(int32(2))},
		{name: "When public nodes exist, it should report workers", nodes: []corev1.Node{{}}, expected: true},
		{name: "When a private pool belongs to another cluster, it should ignore it", private: true, poolCluster: "other", replicas: ptr.To(int32(2))},
		{name: "When a private pool has zero replicas, it should report no workers", private: true, poolCluster: "cluster", replicas: ptr.To(int32(0))},
		{name: "When a private pool has positive replicas, it should report workers", private: true, poolCluster: "cluster", replicas: ptr.To(int32(1)), expected: true},
		{name: "When private autoscaling has positive max, it should expect workers even with zero min", private: true, poolCluster: "cluster", autoscaling: &hyperv1.NodePoolAutoScaling{Min: ptr.To(int32(0)), Max: 3}, expected: true},
		{name: "When private autoscaling has zero max, it should not expect workers", private: true, poolCluster: "cluster", autoscaling: &hyperv1.NodePoolAutoScaling{Min: ptr.To(int32(0)), Max: 0}},
		{name: "When AutoNode is configured without actual nodes, it should not infer public workers", autoNode: true},
		{name: "When AutoNode has actual nodes, it should report public workers", autoNode: true, nodes: []corev1.Node{{}}, expected: true},
		{name: "When private AutoNode has no NodePools, it should preserve baseline NodePool evidence", private: true, autoNode: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			hostedCluster := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Name: "cluster"}}
			hostedCluster.Spec.Platform.Type = hyperv1.AWSPlatform
			hostedCluster.Spec.Platform.AWS = &hyperv1.AWSPlatformSpec{EndpointAccess: hyperv1.Public}
			if testCase.private {
				hostedCluster.Spec.Platform.AWS.EndpointAccess = hyperv1.Private
			}
			if testCase.autoNode {
				hostedCluster.Spec.AutoNode.Provisioner.Name = hyperv1.ProvisionerKarpenter
			}
			var pools []hyperv1.NodePool
			if testCase.poolCluster != "" {
				pools = []hyperv1.NodePool{{Spec: hyperv1.NodePoolSpec{ClusterName: testCase.poolCluster, Replicas: testCase.replicas, AutoScaling: testCase.autoscaling}}}
			}
			if actual := healthHasWorkers(hostedCluster, pools, testCase.nodes); actual != testCase.expected {
				t.Fatalf("healthHasWorkers() = %v, want %v", actual, testCase.expected)
			}
		})
	}
}

func TestHealthNodePoolCounts(t *testing.T) {
	readyNode := corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker", Labels: map[string]string{hyperv1.NodePoolLabel: "pool"}}, Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}}}
	staticPool := hyperv1.NodePool{ObjectMeta: metav1.ObjectMeta{Name: "pool"}, Spec: hyperv1.NodePoolSpec{ClusterName: "cluster", Replicas: ptr.To(int32(1))}, Status: hyperv1.NodePoolStatus{Replicas: 1}}
	for _, testCase := range []struct {
		name      string
		private   bool
		autoNode  bool
		pools     []hyperv1.NodePool
		nodes     []corev1.Node
		wantError bool
	}{
		{name: "When zero workers are configured, it should accept zero nodes"},
		{name: "When an unrelated pool exists, it should not add expected workers", pools: []hyperv1.NodePool{{Spec: hyperv1.NodePoolSpec{ClusterName: "other", Replicas: ptr.To(int32(5))}}}},
		{name: "When a static pool and its ready node agree, it should pass", pools: []hyperv1.NodePool{staticPool}, nodes: []corev1.Node{readyNode}},
		{name: "When a desired worker is missing, it should reject the cluster", pools: []hyperv1.NodePool{staticPool}, wantError: true},
		{name: "When an extra node exists without a pool, it should reject the cluster", nodes: []corev1.Node{readyNode}, wantError: true},
		{name: "When a node has the wrong pool label, it should reject the cluster", pools: []hyperv1.NodePool{staticPool}, nodes: []corev1.Node{{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{hyperv1.NodePoolLabel: "other"}}}}, wantError: true},
		{name: "When a node is not ready, it should reject the cluster", pools: []hyperv1.NodePool{staticPool}, nodes: []corev1.Node{{ObjectMeta: readyNode.ObjectMeta}}, wantError: true},
		{name: "When private replicas agree, it should not require guest nodes", private: true, pools: []hyperv1.NodePool{staticPool}},
		{name: "When private status replicas differ, it should reject the pool", private: true, pools: []hyperv1.NodePool{{Spec: staticPool.Spec}}, wantError: true},
		{name: "When autoscaling status is within bounds, it should match actual nodes to status", pools: []hyperv1.NodePool{{ObjectMeta: staticPool.ObjectMeta, Spec: hyperv1.NodePoolSpec{ClusterName: "cluster", AutoScaling: &hyperv1.NodePoolAutoScaling{Min: ptr.To(int32(0)), Max: 3}}, Status: hyperv1.NodePoolStatus{Replicas: 1}}}, nodes: []corev1.Node{readyNode}},
		{name: "When autoscaling status is outside bounds, it should reject the pool", private: true, pools: []hyperv1.NodePool{{Spec: hyperv1.NodePoolSpec{ClusterName: "cluster", AutoScaling: &hyperv1.NodePoolAutoScaling{Min: ptr.To(int32(2)), Max: 3}}, Status: hyperv1.NodePoolStatus{Replicas: 1}}}, wantError: true},
		{name: "When AutoNode has ready unpooled nodes, it should accept them without inventing NodePools", autoNode: true, nodes: []corev1.Node{readyNode}},
		{name: "When AutoNode has an unready node, it should still reject it", autoNode: true, nodes: []corev1.Node{{}}, wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			hostedCluster := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Name: "cluster"}}
			hostedCluster.Spec.Platform.Type = hyperv1.AWSPlatform
			hostedCluster.Spec.Platform.AWS = &hyperv1.AWSPlatformSpec{EndpointAccess: hyperv1.Public}
			if testCase.private {
				hostedCluster.Spec.Platform.AWS.EndpointAccess = hyperv1.Private
			}
			if testCase.autoNode {
				hostedCluster.Spec.AutoNode.Provisioner.Name = hyperv1.ProvisionerKarpenter
			}
			if err := healthNodePoolCounts(hostedCluster, testCase.pools, testCase.nodes); (err != nil) != testCase.wantError {
				t.Fatalf("node count error = %v, want error %v", err, testCase.wantError)
			}
		})
	}
}

func TestHealthConfiguredNodePoolCounts(t *testing.T) {
	readyNode := corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker", Labels: map[string]string{hyperv1.NodePoolLabel: "pool"}}, Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}}}
	staticPool := hyperv1.NodePool{ObjectMeta: metav1.ObjectMeta{Name: "pool"}, Spec: hyperv1.NodePoolSpec{ClusterName: "cluster", Replicas: ptr.To(int32(1))}, Status: hyperv1.NodePoolStatus{Replicas: 1}}
	for _, testCase := range []struct {
		name                string
		expected            map[string]int32
		pools               []hyperv1.NodePool
		nodes               []corev1.Node
		private             bool
		autoNode            bool
		azure               bool
		wantError           bool
		checkCurrentDesired bool
	}{
		{name: "When independent intent is absent, it should fail rather than silently pass", wantError: true},
		{name: "When every expected pool and node is missing, it should reject empty observations", expected: map[string]int32{"pool": 1}, wantError: true},
		{name: "When a configured zero-replica pool is missing, it should reject missing creation intent", expected: map[string]int32{"pool": 0}, wantError: true},
		{name: "When explicit empty intent has no pools or nodes, it should accept zero workers", expected: map[string]int32{}},
		{name: "When an explicitly configured zero-replica pool exists, it should accept zero workers", expected: map[string]int32{"pool": 0}, pools: []hyperv1.NodePool{{ObjectMeta: staticPool.ObjectMeta, Spec: hyperv1.NodePoolSpec{ClusterName: "cluster", Replicas: ptr.To(int32(0))}}}},
		{name: "When configured pools and ready workers agree, it should pass", expected: map[string]int32{"pool": 1}, pools: []hyperv1.NodePool{staticPool}, nodes: []corev1.Node{readyNode}},
		{name: "When Azure configured pools and ready workers agree, it should preserve nonAWS consumers", azure: true, expected: map[string]int32{"pool": 1}, pools: []hyperv1.NodePool{staticPool}, nodes: []corev1.Node{readyNode}},
		{name: "When a second configured zone pool is missing, it should reject even with the first pool ready", expected: map[string]int32{"pool": 1, "pool-zone2": 1}, pools: []hyperv1.NodePool{staticPool}, nodes: []corev1.Node{readyNode}, wantError: true},
		{name: "When a relevant unexpected pool exists, it should reject the pool set", expected: map[string]int32{}, pools: []hyperv1.NodePool{staticPool}, nodes: []corev1.Node{readyNode}, wantError: true},
		{name: "When an expected pool belongs to another cluster, it should reject the association", expected: map[string]int32{"pool": 1}, pools: []hyperv1.NodePool{{ObjectMeta: staticPool.ObjectMeta, Spec: hyperv1.NodePoolSpec{ClusterName: "other", Replicas: ptr.To(int32(1))}, Status: staticPool.Status}}, nodes: []corev1.Node{readyNode}, wantError: true},
		{name: "When an unrelated pool exists, it should ignore it without weakening expected membership", expected: map[string]int32{}, pools: []hyperv1.NodePool{{ObjectMeta: metav1.ObjectMeta{Name: "other-pool"}, Spec: hyperv1.NodePoolSpec{ClusterName: "other", Replicas: ptr.To(int32(1))}}}},
		{name: "When initial replica intent differs from current spec, it should reject only the initial root contract", expected: map[string]int32{"pool": 2}, pools: []hyperv1.NodePool{staticPool}, nodes: []corev1.Node{readyNode}, wantError: true, checkCurrentDesired: true},
		{name: "When replica status diverges, it should reject the pool", expected: map[string]int32{"pool": 1}, pools: []hyperv1.NodePool{{ObjectMeta: staticPool.ObjectMeta, Spec: staticPool.Spec}}, nodes: []corev1.Node{readyNode}, wantError: true},
		{name: "When the node has the wrong pool label, it should reject matching global counts", expected: map[string]int32{"pool": 1}, pools: []hyperv1.NodePool{staticPool}, nodes: []corev1.Node{{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{hyperv1.NodePoolLabel: "other"}}, Status: readyNode.Status}}, wantError: true},
		{name: "When the node lacks a pool label, it should reject matching global counts", expected: map[string]int32{"pool": 1}, pools: []hyperv1.NodePool{staticPool}, nodes: []corev1.Node{{ObjectMeta: metav1.ObjectMeta{Name: "worker"}, Status: readyNode.Status}}, wantError: true},
		{name: "When a configured node is unready, it should reject the cluster", expected: map[string]int32{"pool": 1}, pools: []hyperv1.NodePool{staticPool}, nodes: []corev1.Node{{ObjectMeta: readyNode.ObjectMeta}}, wantError: true},
		{name: "When an extra public node exists, it should reject excess cardinality", expected: map[string]int32{}, nodes: []corev1.Node{readyNode}, wantError: true},
		{name: "When a private configured pool agrees, it should not require guest node observations", private: true, expected: map[string]int32{"pool": 1}, pools: []hyperv1.NodePool{staticPool}},
		{name: "When all private configured pools are missing, it should fail without guest access", private: true, expected: map[string]int32{"pool": 1}, wantError: true},
		{name: "When AutoNode initially has no configured pools or nodes, it should accept the independent zero count", autoNode: true, expected: map[string]int32{}},
		{name: "When AutoNode already has dynamic nodes before main, it should reject an unexpected initial total but retain current-state coverage", autoNode: true, expected: map[string]int32{}, nodes: []corev1.Node{readyNode}, wantError: true, checkCurrentDesired: true},
		{name: "When AutoNode initially has exactly its configured workers, it should accept the independent total", autoNode: true, expected: map[string]int32{"pool": 1}, pools: []hyperv1.NodePool{staticPool}, nodes: []corev1.Node{readyNode}},
		{name: "When AutoNode has an extra unpooled ready worker before main, it should reject excess initial cardinality", autoNode: true, expected: map[string]int32{"pool": 1}, pools: []hyperv1.NodePool{staticPool}, nodes: []corev1.Node{readyNode, {ObjectMeta: metav1.ObjectMeta{Name: "dynamic"}, Status: readyNode.Status}}, wantError: true, checkCurrentDesired: true},
		{name: "When multiple initial zone pools have their configured workers, it should accept the summed independent total", autoNode: true, expected: map[string]int32{"pool": 1, "pool-zone2": 1}, pools: []hyperv1.NodePool{staticPool, {ObjectMeta: metav1.ObjectMeta{Name: "pool-zone2"}, Spec: staticPool.Spec, Status: staticPool.Status}}, nodes: []corev1.Node{readyNode, {ObjectMeta: metav1.ObjectMeta{Name: "zone2-worker", Labels: map[string]string{hyperv1.NodePoolLabel: "pool-zone2"}}, Status: readyNode.Status}}},
		{name: "When AutoNode has unready dynamic nodes, it should still reject them", autoNode: true, expected: map[string]int32{}, nodes: []corev1.Node{{}}, wantError: true},
		{name: "When configured replica count is negative, it should reject invalid intent", expected: map[string]int32{"pool": -1}, wantError: true},
		{name: "When configured pool name is empty, it should reject invalid intent", expected: map[string]int32{"": 0}, wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			hostedCluster := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Name: "cluster"}}
			hostedCluster.Spec.Platform.Type = hyperv1.AWSPlatform
			hostedCluster.Spec.Platform.AWS = &hyperv1.AWSPlatformSpec{EndpointAccess: hyperv1.Public}
			if testCase.private {
				hostedCluster.Spec.Platform.AWS.EndpointAccess = hyperv1.Private
			}
			if testCase.azure {
				hostedCluster.Spec.Platform.Type = hyperv1.AzurePlatform
			}
			if testCase.autoNode {
				hostedCluster.Spec.AutoNode.Provisioner.Name = hyperv1.ProvisionerKarpenter
			}
			if err := healthConfiguredNodePoolCounts(hostedCluster, testCase.pools, testCase.nodes, testCase.expected); (err != nil) != testCase.wantError {
				t.Fatalf("configured count error = %v, want error %v", err, testCase.wantError)
			}
			if testCase.checkCurrentDesired {
				if err := healthNodePoolCounts(hostedCluster, testCase.pools, testCase.nodes); err != nil {
					t.Fatalf("ordinary post-mutation consistency must use current desired replicas: %v", err)
				}
			}
		})
	}
}
