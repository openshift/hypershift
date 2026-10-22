//go:build e2ev2

package main

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperapi "github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/test/e2e/v2/lifecycle"

	configv1 "github.com/openshift/api/config/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestLoadEnvConfigReleaseImages(t *testing.T) {
	for _, test := range []struct {
		name, latest, previous string
	}{
		{name: "When latest and previous are absent, it should defer discovery to the context-bound run"},
		{name: "When latest is supplied, it should preserve latest and default previous", latest: discoveredReleaseImage},
		{name: "When previous is supplied without latest, it should preserve previous until discovery", previous: "quay.io/openshift-release-dev/ocp-release:4.20.20-multi"},
	} {
		t.Run(test.name, func(t *testing.T) {
			for variable, value := range map[string]string{
				"SHARED_DIR": t.TempDir(), "ARTIFACT_DIR": t.TempDir(), "PROW_JOB_ID": "12345", "HYPERSHIFT_PLATFORM": "aws",
				"RELEASE_IMAGE_LATEST": test.latest, "OCP_IMAGE_N1": test.previous,
			} {
				t.Setenv(variable, value)
			}
			cfg := loadEnvConfig()
			wantPrevious := test.previous
			if wantPrevious == "" {
				wantPrevious = test.latest
			}
			if cfg.releaseImage != test.latest || cfg.n1Image != wantPrevious || os.Getenv("RELEASE_IMAGE_LATEST") != test.latest {
				t.Fatalf("environment release loading differs: latest=%q previous=%q", cfg.releaseImage, cfg.n1Image)
			}
		})
	}
}

func TestReportClusterResults(t *testing.T) {
	for _, test := range []struct {
		name      string
		results   map[string]error
		wantError bool
	}{
		{name: "When all commands succeed, it should allow the next phase", results: map[string]error{"public": nil}},
		{name: "When a selected command fails, it should stop the next phase", results: map[string]error{"public": errors.New("failed")}, wantError: true},
		{name: "When an unlisted result fails, it should preserve the aggregate failure", results: map[string]error{"public": nil, "upgrade": errors.New("failed")}, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			named := []namedSpec{{ClusterSpec: lifecycle.ClusterSpec{Variant: "public"}, name: "release-fixture"}}
			if err := reportClusterCreateResults(named, test.results); (err != nil) != test.wantError {
				t.Fatalf("creation aggregate differs: %v", err)
			}
			if err := reportClusterAvailabilityResults(named, test.results); (err != nil) != test.wantError {
				t.Fatalf("availability aggregate differs: %v", err)
			}
		})
	}
}

type nonStreamingWatchClient struct {
	crclient.WithWatch
}

func (nonStreamingWatchClient) IsWatchListSemanticsUnSupported() bool {
	return true
}

func readyZeroWorkerFixture() (*hyperv1.HostedCluster, *appsv1.Deployment) {
	cluster := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "ha", Namespace: "clusters"},
		Spec:       hyperv1.HostedClusterSpec{ControllerAvailabilityPolicy: hyperv1.HighlyAvailable},
		Status: hyperv1.HostedClusterStatus{
			KubeConfig: &corev1.LocalObjectReference{Name: "ha-admin-kubeconfig"},
			Conditions: []metav1.Condition{{Type: string(hyperv1.HostedClusterAvailable), Status: metav1.ConditionTrue}},
			Version:    &hyperv1.ClusterVersionStatus{History: []configv1.UpdateHistory{{State: configv1.PartialUpdate}}},
		},
	}
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "kube-apiserver", Namespace: "clusters-ha", Generation: 2},
		Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](3)},
		Status:     appsv1.DeploymentStatus{ObservedGeneration: 2, Replicas: 3, UpdatedReplicas: 3, ReadyReplicas: 3, AvailableReplicas: 3},
	}
	return cluster, deployment
}

func TestZeroWorkerControlPlaneReady(t *testing.T) {
	for _, test := range []struct {
		name      string
		mutate    func(*hyperv1.HostedCluster, *appsv1.Deployment)
		wantReady bool
	}{
		{name: "When all three HA API replicas are ready, it should accept a Partial CVO history", wantReady: true},
		{name: "When CVO history is absent, it should still accept the usable zero-worker control plane", mutate: func(cluster *hyperv1.HostedCluster, _ *appsv1.Deployment) { cluster.Status.Version = nil }, wantReady: true},
		{name: "When HC Available is absent, it should keep waiting", mutate: func(cluster *hyperv1.HostedCluster, _ *appsv1.Deployment) { cluster.Status.Conditions = nil }},
		{name: "When HC Available is false, it should keep waiting", mutate: func(cluster *hyperv1.HostedCluster, _ *appsv1.Deployment) {
			cluster.Status.Conditions[0].Status = metav1.ConditionFalse
		}},
		{name: "When kubeconfig reference is absent, it should keep waiting", mutate: func(cluster *hyperv1.HostedCluster, _ *appsv1.Deployment) { cluster.Status.KubeConfig = nil }},
		{name: "When kubeconfig reference is empty, it should keep waiting", mutate: func(cluster *hyperv1.HostedCluster, _ *appsv1.Deployment) { cluster.Status.KubeConfig.Name = "" }},
		{name: "When desired API replicas are absent, it should reject vacuous readiness", mutate: func(_ *hyperv1.HostedCluster, deployment *appsv1.Deployment) { deployment.Spec.Replicas = nil }},
		{name: "When only one API replica is desired on HA, it should keep waiting", mutate: func(_ *hyperv1.HostedCluster, deployment *appsv1.Deployment) {
			deployment.Spec.Replicas = ptr.To[int32](1)
		}},
		{name: "When deployment status is stale, it should keep waiting", mutate: func(_ *hyperv1.HostedCluster, deployment *appsv1.Deployment) {
			deployment.Status.ObservedGeneration = 1
		}},
		{name: "When an API replica is not updated, it should keep waiting", mutate: func(_ *hyperv1.HostedCluster, deployment *appsv1.Deployment) { deployment.Status.UpdatedReplicas = 2 }},
		{name: "When an API replica is not Ready, it should keep waiting", mutate: func(_ *hyperv1.HostedCluster, deployment *appsv1.Deployment) { deployment.Status.ReadyReplicas = 2 }},
		{name: "When an API replica is unavailable, it should keep waiting", mutate: func(_ *hyperv1.HostedCluster, deployment *appsv1.Deployment) {
			deployment.Status.AvailableReplicas = 2
			deployment.Status.UnavailableReplicas = 1
		}},
		{name: "When old API replicas remain, it should keep waiting", mutate: func(_ *hyperv1.HostedCluster, deployment *appsv1.Deployment) { deployment.Status.Replicas = 4 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			cluster, deployment := readyZeroWorkerFixture()
			if test.mutate != nil {
				test.mutate(cluster, deployment)
			}
			if zeroWorkerControlPlaneReady(cluster, deployment) != test.wantReady {
				t.Fatal("unexpected zero-worker readiness")
			}
		})
	}
}

func TestPollZeroWorkerControlPlane(t *testing.T) {
	for _, resource := range []string{"HostedCluster", "Deployment"} {
		for _, testCase := range []struct {
			name      string
			readError error
			terminal  bool
			stop      string
		}{
			{name: "When the resource is absent, it should wait for readiness", readError: apierrors.NewNotFound(schema.GroupResource{Resource: resource}, "ha")},
			{name: "When the API times out, it should retry", readError: apierrors.NewTimeoutError("timeout", 1)},
			{name: "When the server times out, it should retry", readError: apierrors.NewServerTimeout(schema.GroupResource{Resource: resource}, "get", 1)},
			{name: "When throttled, it should retry", readError: apierrors.NewTooManyRequests("throttled", 1)},
			{name: "When unavailable, it should retry", readError: apierrors.NewServiceUnavailable("unavailable")},
			{name: "When the API reports an internal error, it should retry", readError: apierrors.NewInternalError(errors.New("internal failure"))},
			{name: "When a network timeout occurs, it should retry", readError: &net.DNSError{Err: "timeout", IsTimeout: true}},
			{name: "When the connection drops, it should retry", readError: io.ErrUnexpectedEOF},
			{name: "When the connection resets, it should retry", readError: syscall.ECONNRESET},
			{name: "When the connection is refused, it should retry", readError: syscall.ECONNREFUSED},
			{name: "When the API forbids access, it should fail immediately", readError: apierrors.NewForbidden(schema.GroupResource{Resource: resource}, "ha", errors.New("denied")), terminal: true},
			{name: "When authentication fails, it should fail immediately", readError: apierrors.NewUnauthorized("denied"), terminal: true},
			{name: "When the request is invalid, it should fail immediately", readError: apierrors.NewBadRequest("invalid"), terminal: true},
			{name: "When an unknown error occurs, it should fail immediately", readError: errors.New("unknown failure"), terminal: true},
			{name: "When errors persist, it should honor the deadline", readError: apierrors.NewServiceUnavailable("persistent"), stop: "deadline"},
			{name: "When canceled during retry, it should honor cancellation", readError: apierrors.NewServiceUnavailable("persistent"), stop: "cancel"},
		} {
			t.Run(resource+"/"+testCase.name, func(t *testing.T) {
				cluster, deployment := readyZeroWorkerFixture()
				timeout := 10 * time.Second
				if testCase.stop == "deadline" {
					timeout = 30 * time.Millisecond
				}
				var cancel context.CancelFunc
				reads := 0
				client := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(cluster, deployment).WithInterceptorFuncs(interceptor.Funcs{
					Get: func(ctx context.Context, client crclient.WithWatch, key crclient.ObjectKey, object crclient.Object, options ...crclient.GetOption) error {
						matches := resource == "HostedCluster"
						if _, isDeployment := object.(*appsv1.Deployment); isDeployment {
							matches = !matches
						}
						if matches {
							reads++
							if testCase.stop == "cancel" {
								cancel()
							}
							if reads == 1 || testCase.stop != "" {
								return testCase.readError
							}
						}
						return client.Get(ctx, key, object, options...)
					},
				}).Build()
				ctx, cancel := context.WithTimeout(t.Context(), timeout)
				defer cancel()
				err := pollZeroWorkerControlPlane(ctx, client, "clusters", "ha", time.Millisecond)
				if testCase.terminal {
					if !errors.Is(err, testCase.readError) || reads != 1 {
						t.Fatalf("terminal error lost or retried: reads=%d err=%v", reads, err)
					}
				} else if testCase.stop != "" {
					want := context.DeadlineExceeded
					if testCase.stop == "cancel" {
						want = context.Canceled
					}
					if !errors.Is(err, want) || !errors.Is(err, testCase.readError) || !strings.Contains(err.Error(), resource) {
						t.Fatalf("deadline/cancellation or diagnostic lost: %v", err)
					}
				} else if err != nil || reads != 2 {
					t.Fatalf("transient read prevented readiness: reads=%d err=%v", reads, err)
				}
			})
		}
	}
}

func TestWaitForVersionRollout(t *testing.T) {
	for _, test := range []struct {
		name          string
		replicas      *int
		defaultCount  *int
		renderedPools map[string]int32
		complete      bool
		apiReady      bool
		wantError     bool
	}{
		{name: "When explicit zero workers have a ready HA control plane, it should proceed despite Partial CVO", replicas: ptr.To(0), apiReady: true},
		{name: "When explicit zero workers lack ready API replicas, it should not proceed", replicas: ptr.To(0), wantError: true},
		{name: "When default workers have Partial CVO, it should retain the existing rollout wait", apiReady: true, wantError: true},
		{name: "When positive worker override has Partial CVO, it should retain the existing rollout wait", replicas: ptr.To(1), apiReady: true, wantError: true},
		{name: "When default workers complete CVO, it should proceed using the original predicate", complete: true},
		{name: "When positive worker override completes CVO, it should proceed using the original predicate", replicas: ptr.To(1), complete: true},
		{name: "When default zero workers have a ready control plane, it should not wait for impossible CVO completion", defaultCount: ptr.To(0), apiReady: true},
		{name: "When default zero workers lack ready API replicas, it should keep waiting", defaultCount: ptr.To(0), wantError: true},
		{name: "When positive fixture workers override default zero, it should still require CVO completion", defaultCount: ptr.To(0), replicas: ptr.To(1), apiReady: true, wantError: true},
		{name: "When rendered arguments remove default workers, it should use control plane readiness", renderedPools: map[string]int32{}, apiReady: true},
		{name: "When rendered arguments remove default workers but API is unready, it should keep waiting", renderedPools: map[string]int32{}, wantError: true},
		{name: "When rendered arguments add workers to zero intent, it should require CVO completion", replicas: ptr.To(0), renderedPools: map[string]int32{"ha": 1}, apiReady: true, wantError: true},
		{name: "When any rendered zone has workers, it should require CVO completion", replicas: ptr.To(0), renderedPools: map[string]int32{"zone-a": 0, "zone-b": 2}, apiReady: true, wantError: true},
		{name: "When rendered worker CVO is complete, it should proceed", replicas: ptr.To(0), renderedPools: map[string]int32{"ha": 1}, complete: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			cluster, deployment := readyZeroWorkerFixture()
			if test.complete {
				cluster.Status.Version.History[0].State = configv1.CompletedUpdate
			}
			if !test.apiReady {
				deployment.Status.ReadyReplicas = 1
			}
			client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cluster, deployment).Build()
			specs := []namedSpec{{ClusterSpec: lifecycle.ClusterSpec{Variant: "fixture", InitialNodePoolReplicas: test.replicas}, name: "ha"}}
			if test.renderedPools != nil {
				manifest := &lifecycle.ClusterManifest{Clusters: []lifecycle.ClusterEntry{{Name: "ha"}}}
				configureExpectedNodePoolRecorders(specs, manifest, t.TempDir())
				var objects []*unstructured.Unstructured
				for name, replicas := range test.renderedPools {
					pool := configuredPool(name, int64(replicas))
					if err := unstructured.SetNestedField(pool.Object, "ha", "spec", "clusterName"); err != nil {
						t.Fatal(err)
					}
					objects = append(objects, pool)
				}
				if err := recordAWSExpectedNodePools("clusters", specs[0], objects); err != nil {
					t.Fatal(err)
				}
			}
			results := waitForVersionRollout(t.Context(), nonStreamingWatchClient{client}, envConfig{namespace: "clusters", nodeCount: ptr.Deref(test.defaultCount, 2), waitTimeout: time.Second}, specs)
			if (results["fixture"] != nil) != test.wantError {
				t.Fatalf("unexpected consumer readiness result: %v", results["fixture"])
			}
		})
	}
}

func TestValidateScenarioInputs(t *testing.T) {
	t.Run("When the unchanged default plan is selected without opt-in inputs, it should not require KMS or external DNS", func(t *testing.T) {
		t.Setenv("E2E_AWS_KMS_KEY_ARN", "")
		t.Setenv("HYPERSHIFT_EXTERNAL_DNS_DOMAIN", "")
		platform := lifecycle.NewAWSPlatformConfig(lifecycle.AWSPlatformOptions{Region: "us-east-1", Zones: "us-east-1a"}, t.TempDir())
		plan := platform.DefaultTestPlan()
		specs := plan.FilterClusterSpecs(platform.ClusterSpecs("latest", "previous"))
		if err := validateScenarioInputs(envConfig{platform: platform}, specs); err != nil {
			t.Fatal(err)
		}
	})
	tests := []struct {
		name, variant, dns, kms, alias string
		wantError                      bool
	}{
		{name: "When only public is selected, it should not require opt-in inputs", variant: "public"},
		{name: "When private without routes is selected, it should not require DNS", variant: "private"},
		{name: "When private-route lacks DNS, it should fail before provisioning", variant: "private-route", wantError: true},
		{name: "When private-route has DNS, it should accept the input", variant: "private-route", dns: "e2e.hypershift.devcluster.openshift.com"},
		{name: "When custom-config lacks a KMS key, it should fail before provisioning", variant: "custom-config", wantError: true},
		{name: "When custom-config has a KMS key, it should accept the input", variant: "custom-config", kms: "arn:aws:kms:us-east-1:123456789012:key/00000000-0000-0000-0000-000000000000"},
		{name: "When custom-config has a KMS alias, it should accept it for pre-create resolution", variant: "custom-config", alias: "alias/e2e-etcd"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("E2E_AWS_KMS_KEY_ARN", test.kms)
			t.Setenv("E2E_AWS_KMS_KEY_ALIAS", test.alias)
			config := envConfig{platform: &lifecycle.AWSPlatformConfig{}, externalDNS: test.dns}
			err := validateScenarioInputs(config, []lifecycle.ClusterSpec{{Variant: test.variant, CreateArgs: []string{"--region=us-east-1"}, DestroyArgs: []string{"--region=us-east-1"}}})
			if (err != nil) != test.wantError {
				t.Fatalf("unexpected validation result: %v", err)
			}
		})
	}
}

func TestBuildCreateArgsAWSPrivateDNS(t *testing.T) {
	for _, test := range []struct{ name, variant, want string }{
		{name: "When private is selected, it should override global external DNS with an empty domain", variant: "private", want: "--external-dns-domain="},
		{name: "When private-route is selected, it should retain the configured external DNS domain", variant: "private-route", want: "--external-dns-domain=e2e.hypershift.devcluster.openshift.com"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("EXTRA_ARGS", "")
			platform := &lifecycle.AWSPlatformConfig{}
			config := envConfig{platform: platform, externalDNS: "e2e.hypershift.devcluster.openshift.com"}
			for _, spec := range platform.ClusterSpecs("latest", "previous") {
				if spec.Variant != test.variant {
					continue
				}
				var effective string
				for _, argument := range buildCreateArgs(config, namedSpec{ClusterSpec: spec, name: "private-fixture"}) {
					if strings.HasPrefix(argument, "--external-dns-domain=") {
						effective = argument
					}
				}
				if effective != test.want {
					t.Fatalf("effective DNS argument %q, want %q", effective, test.want)
				}
				return
			}
			t.Fatal("private fixture variant was not found")
		})
	}
}

func TestBuildCreateArgs(t *testing.T) {
	t.Parallel()

	override := 2
	cfg := envConfig{
		baseDomain:   "example.com",
		nodeCount:    6,
		namespace:    "clusters",
		releaseImage: "release-image",
		pullSecret:   "/tmp/pull-secret",
		platform:     lifecycle.NewAzurePlatformConfig(""),
	}

	tests := []struct {
		name         string
		override     *int
		defaultCount *int
		want         int
	}{
		{
			name:     "When ClusterSpec has an initial replica override, it should use that value",
			override: &override,
			want:     2,
		},
		{
			name: "When ClusterSpec has no initial replica override, it should use the global node count",
			want: 6,
		},
		{
			name:         "When the global node count is zero, it should render zero workers without a fixture override",
			defaultCount: ptr.To(0),
			want:         0,
		},
		{
			name:         "When a positive fixture overrides global zero, it should render the fixture worker count",
			override:     &override,
			defaultCount: ptr.To(0),
			want:         2,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ns := namedSpec{
				ClusterSpec: lifecycle.ClusterSpec{
					Variant:                 "public",
					CreateArgs:              []string{"--location=centralus", "--encryption-key-id=test-key"},
					DestroyArgs:             []string{"--cluster-grace-period=40m"},
					InitialNodePoolReplicas: tt.override,
				},
				name: "public-test",
			}
			config := cfg
			config.nodeCount = ptr.Deref(tt.defaultCount, cfg.nodeCount)
			args := buildCreateArgs(config, ns)
			if slices.Contains(args, ns.DestroyArgs[0]) {
				t.Errorf("create args %v contain destroy-only argument %q", args, ns.DestroyArgs[0])
			}
			if !slices.Equal(args[len(args)-len(ns.CreateArgs):], ns.CreateArgs) {
				t.Errorf("create args %v do not end with spec arguments %v", args, ns.CreateArgs)
			}
			want := "--node-pool-replicas=" + strconv.Itoa(tt.want)
			for _, arg := range args {
				if arg == want {
					return
				}
			}
			t.Errorf("create args %v do not contain %q", args, want)
		})
	}
}
