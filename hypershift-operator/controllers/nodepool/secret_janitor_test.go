package nodepool

import (
	"context"
	"errors"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/ignitionserver"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/releaseinfo"
	fakereleaseprovider "github.com/openshift/hypershift/support/releaseinfo/fake"
	"github.com/openshift/hypershift/support/releaseinfo/testutils"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/dockerv1client"
	"github.com/openshift/hypershift/support/util/fakeimagemetadataprovider"

	"github.com/openshift/api/image/docker10"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	testingclock "k8s.io/utils/clock/testing"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/blang/semver"
	"github.com/go-logr/zapr"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type janitorTestFixture struct {
	nodePool        *hyperv1.NodePool
	hostedCluster   *hyperv1.HostedCluster
	pullSecret      *corev1.Secret
	machineConfig   *corev1.ConfigMap
	ignitionConfigs []*corev1.ConfigMap
	ignitionCACert  *corev1.Secret
	client          client.Client
	reconciler      secretJanitor
	fakeClock       *testingclock.FakeClock
}

func newJanitorTestFixture(t *testing.T) *janitorTestFixture {
	t.Helper()
	theTime, err := time.Parse(time.RFC3339Nano, "2006-01-02T15:04:05.999999999Z")
	if err != nil {
		t.Fatalf("could not parse time: %v", err)
	}
	fakeClock := testingclock.NewFakeClock(theTime)

	pullSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "pull-secret", Namespace: "myns"},
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: []byte("whatever"),
		},
	}

	hostedCluster := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-name", Namespace: "myns"},
		Spec: hyperv1.HostedClusterSpec{
			PullSecret: corev1.LocalObjectReference{Name: pullSecret.Name},
		},
		Status: hyperv1.HostedClusterStatus{
			IgnitionEndpoint: "https://ignition.cluster-name.myns.devcluster.openshift.com",
		},
	}

	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{Name: "nodepool-name", Namespace: "myns"},
		Spec: hyperv1.NodePoolSpec{
			ClusterName: hostedCluster.Name,
			Release:     hyperv1.Release{Image: "fake-release-image"},
			Config: []corev1.LocalObjectReference{
				{Name: "machineconfig-1"},
			},
		},
		Status: hyperv1.NodePoolStatus{Version: semver.MustParse("4.18.0").String()},
	}

	coreMachineConfig := `
apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  labels:
    machineconfiguration.openshift.io/role: master
  name: config-1
spec:
  config:
    ignition:
      version: 3.2.0
    storage:
      files:
      - contents:
        source: "[Service]\nType=oneshot\nExecStart=/usr/bin/echo Hello World\n\n[Install]\nWantedBy=multi-user.target"
        filesystem: root
        mode: 493
        path: /usr/local/bin/file1.sh
`

	machineConfig := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "machineconfig-1",
			Namespace: "myns",
		},
		Data: map[string]string{
			TokenSecretConfigKey: coreMachineConfig,
		},
	}

	makeIgnitionConfig := func(name string) *corev1.ConfigMap {
		return &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: "myns-cluster-name",
				Labels: map[string]string{
					nodePoolCoreIgnitionConfigLabel: "true",
				},
			},
			Data: map[string]string{
				TokenSecretConfigKey: coreMachineConfig,
			},
		}
	}
	ignitionConfigs := []*corev1.ConfigMap{
		makeIgnitionConfig("core-machineconfig"),
		makeIgnitionConfig("core-machineconfig-2"),
		makeIgnitionConfig("core-machineconfig-3"),
	}

	ignitionServerCACert := ignitionserver.IgnitionCACertSecret("myns-cluster-name")
	ignitionServerCACert.Data = map[string][]byte{
		corev1.TLSCertKey: []byte("test-ignition-ca-cert"),
	}

	c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(
		nodePool,
		hostedCluster,
		pullSecret,
		machineConfig,
		ignitionConfigs[0],
		ignitionConfigs[1],
		ignitionConfigs[2],
		ignitionServerCACert,
	).Build()

	r := secretJanitor{
		NodePoolReconciler: &NodePoolReconciler{
			Client:          c,
			ReleaseProvider: &fakereleaseprovider.FakeReleaseProvider{Version: semver.MustParse("4.18.0").String()},
			ImageMetadataProvider: &fakeimagemetadataprovider.FakeRegistryClientImageMetadataProvider{Result: &dockerv1client.DockerImageConfig{Config: &docker10.DockerConfig{
				Labels: map[string]string{},
			}}},
		},
		now: fakeClock.Now,
	}

	return &janitorTestFixture{
		nodePool:        nodePool,
		hostedCluster:   hostedCluster,
		pullSecret:      pullSecret,
		machineConfig:   machineConfig,
		ignitionConfigs: ignitionConfigs,
		ignitionCACert:  ignitionServerCACert,
		client:          c,
		reconciler:      r,
		fakeClock:       fakeClock,
	}
}

func TestSecretJanitor_Reconcile(t *testing.T) {
	f := newJanitorTestFixture(t)
	c := f.client
	r := f.reconciler
	nodePool := f.nodePool
	hostedCluster := f.hostedCluster
	pullSecret := f.pullSecret
	machineConfig := f.machineConfig
	ignitionServerCACert := f.ignitionCACert
	fakeClock := f.fakeClock

	for _, testCase := range []struct {
		name     string
		input    *corev1.Secret
		expected *corev1.Secret
	}{
		{
			name: "When secret is unrelated, it should leave it untouched",
			input: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "whatever",
					Namespace: "myns",
				},
			},
			expected: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "whatever",
					Namespace: "myns",
				},
			},
		},
		{
			name: "When secret is related but not known, it should leave it untouched",
			input: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "related",
					Namespace: "myns",
					Annotations: map[string]string{
						nodePoolAnnotation: client.ObjectKeyFromObject(nodePool).String(),
					},
				},
			},
			expected: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "related",
					Namespace: "myns",
					Annotations: map[string]string{
						nodePoolAnnotation: client.ObjectKeyFromObject(nodePool).String(),
					},
				},
			},
		},
		{
			name: "When secret is related and has correct hash, it should leave it untouched",
			input: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "token-nodepool-name-64587037",
					Namespace: "myns",
					Annotations: map[string]string{
						nodePoolAnnotation: client.ObjectKeyFromObject(nodePool).String(),
					},
				},
			},
			expected: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "token-nodepool-name-64587037",
					Namespace: "myns",
					Annotations: map[string]string{
						nodePoolAnnotation: client.ObjectKeyFromObject(nodePool).String(),
					},
				},
			},
		},
		{
			name: "When token secret has incorrect hash, it should set it for expiry",
			input: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "token-nodepool-name-jsadfkjh23",
					Namespace: "myns",
					Annotations: map[string]string{
						nodePoolAnnotation: client.ObjectKeyFromObject(nodePool).String(),
					},
				},
			},
			expected: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "token-nodepool-name-jsadfkjh23",
					Namespace: "myns",
					Annotations: map[string]string{
						nodePoolAnnotation: client.ObjectKeyFromObject(nodePool).String(),
						"hypershift.openshift.io/ignition-token-expiration-timestamp": "2006-01-02T17:04:05Z",
					},
				},
			},
		},
		{
			name: "When ignition user data secret has incorrect hash, it should delete it",
			input: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "user-data-nodepool-name-jsadfkjh23",
					Namespace: "myns",
					Annotations: map[string]string{
						nodePoolAnnotation: client.ObjectKeyFromObject(nodePool).String(),
					},
				},
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			logCore, logs := observer.New(zap.InfoLevel)
			ctx := ctrl.LoggerInto(t.Context(), zapr.NewLogger(zap.New(logCore)))
			if err := c.Create(ctx, testCase.input); err != nil {
				t.Errorf("failed to create object: %v", err)
			}

			key := client.ObjectKeyFromObject(testCase.input)
			if _, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: key}); err != nil {
				t.Errorf("failed to reconcile object: %v", err)
			}

			assertJanitorResult(t, c, ctx, testCase.input, testCase.expected, key, logs)
		})
	}

	t.Run("When deleting stale user data fails, it should return the error without logging success", func(t *testing.T) {
		g := NewWithT(t)
		logCore, logs := observer.New(zap.InfoLevel)
		ctx := ctrl.LoggerInto(t.Context(), zapr.NewLogger(zap.New(logCore)))
		deleteErr := errors.New("injected secret deletion failure")
		userDataSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "user-data-nodepool-name-oldhash",
				Namespace: "myns",
				Annotations: map[string]string{
					nodePoolAnnotation: client.ObjectKeyFromObject(nodePool).String(),
				},
			},
		}
		key := client.ObjectKeyFromObject(userDataSecret)
		deleteCalled := false
		failingClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(
			nodePool, hostedCluster, pullSecret, machineConfig,
			f.ignitionConfigs[0], f.ignitionConfigs[1], f.ignitionConfigs[2], ignitionServerCACert, userDataSecret,
		).WithInterceptorFuncs(interceptor.Funcs{
			Delete: func(_ context.Context, _ client.WithWatch, obj client.Object, _ ...client.DeleteOption) error {
				g.Expect(client.ObjectKeyFromObject(obj)).To(Equal(key))
				deleteCalled = true
				return deleteErr
			},
		}).Build()
		failingReconciler := secretJanitor{
			NodePoolReconciler: &NodePoolReconciler{
				Client:                failingClient,
				ReleaseProvider:       r.ReleaseProvider,
				ImageMetadataProvider: r.ImageMetadataProvider,
			},
			now: fakeClock.Now,
		}

		_, err := failingReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		g.Expect(err).To(MatchError(deleteErr))
		g.Expect(deleteCalled).To(BeTrue())
		g.Expect(failingClient.Get(ctx, key, &corev1.Secret{})).To(Succeed())
		g.Expect(logs.FilterMessage("Deleted secret").Len()).To(BeZero())
	})

	t.Run("When the hosted cluster is not found it should clean up token secret", func(t *testing.T) {
		ctx := t.Context()
		noHCClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(nodePool).Build()
		noHCReconciler := secretJanitor{
			NodePoolReconciler: &NodePoolReconciler{
				Client: noHCClient,
			},
			now: fakeClock.Now,
		}

		tokenSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "token-nodepool-name-oldhash",
				Namespace: "myns",
				Annotations: map[string]string{
					nodePoolAnnotation: client.ObjectKeyFromObject(nodePool).String(),
				},
			},
		}
		if err := noHCClient.Create(ctx, tokenSecret); err != nil {
			t.Fatalf("failed to create token secret: %v", err)
		}

		key := client.ObjectKeyFromObject(tokenSecret)
		if _, err := noHCReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key}); err != nil {
			t.Fatalf("failed to reconcile: %v", err)
		}

		got := &corev1.Secret{}
		if err := noHCClient.Get(ctx, key, got); err != nil {
			t.Fatalf("failed to get token secret: %v", err)
		}
		expectedAnnotations := map[string]string{
			nodePoolAnnotation: client.ObjectKeyFromObject(nodePool).String(),
			hyperv1.IgnitionServerTokenExpirationTimestampAnnotation: "2006-01-02T17:04:05Z",
		}
		if diff := cmp.Diff(got.Annotations, expectedAnnotations); diff != "" {
			t.Errorf("unexpected annotations: %v", diff)
		}
	})

	t.Run("When the hosted cluster is not found it should clean up userdata secret", func(t *testing.T) {
		ctx := t.Context()
		noHCClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(nodePool).Build()
		noHCReconciler := secretJanitor{
			NodePoolReconciler: &NodePoolReconciler{
				Client: noHCClient,
			},
			now: fakeClock.Now,
		}

		userdataSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "user-data-nodepool-name-oldhash",
				Namespace: "myns",
				Annotations: map[string]string{
					nodePoolAnnotation: client.ObjectKeyFromObject(nodePool).String(),
				},
			},
		}
		if err := noHCClient.Create(ctx, userdataSecret); err != nil {
			t.Fatalf("failed to create userdata secret: %v", err)
		}

		key := client.ObjectKeyFromObject(userdataSecret)
		if _, err := noHCReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key}); err != nil {
			t.Fatalf("failed to reconcile: %v", err)
		}

		got := &corev1.Secret{}
		err := noHCClient.Get(ctx, key, got)
		if !apierrors.IsNotFound(err) {
			t.Errorf("expected userdata secret to be deleted, got error: %v", err)
		}
	})

	t.Run("When the hosted cluster is being deleted it should clean up token secret", func(t *testing.T) {
		ctx := t.Context()
		now := metav1.Now()
		deletingHC := hostedCluster.DeepCopy()
		deletingHC.DeletionTimestamp = &now
		deletingHC.Finalizers = []string{"test-finalizer"}

		deletingHCClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(
			nodePool,
			deletingHC,
		).Build()
		deletingHCReconciler := secretJanitor{
			NodePoolReconciler: &NodePoolReconciler{
				Client: deletingHCClient,
			},
			now: fakeClock.Now,
		}

		tokenSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "token-nodepool-name-oldhash",
				Namespace: "myns",
				Annotations: map[string]string{
					nodePoolAnnotation: client.ObjectKeyFromObject(nodePool).String(),
				},
			},
		}
		if err := deletingHCClient.Create(ctx, tokenSecret); err != nil {
			t.Fatalf("failed to create token secret: %v", err)
		}

		key := client.ObjectKeyFromObject(tokenSecret)
		if _, err := deletingHCReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key}); err != nil {
			t.Fatalf("failed to reconcile: %v", err)
		}

		got := &corev1.Secret{}
		if err := deletingHCClient.Get(ctx, key, got); err != nil {
			t.Fatalf("failed to get token secret: %v", err)
		}
		expectedAnnotations := map[string]string{
			nodePoolAnnotation: client.ObjectKeyFromObject(nodePool).String(),
			hyperv1.IgnitionServerTokenExpirationTimestampAnnotation: "2006-01-02T17:04:05Z",
		}
		if diff := cmp.Diff(got.Annotations, expectedAnnotations); diff != "" {
			t.Errorf("unexpected annotations: %v", diff)
		}
	})

	t.Run("When the hosted cluster is being deleted it should clean up userdata secret", func(t *testing.T) {
		ctx := t.Context()
		now := metav1.Now()
		deletingHC := hostedCluster.DeepCopy()
		deletingHC.DeletionTimestamp = &now
		deletingHC.Finalizers = []string{"test-finalizer"}

		deletingHCClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(
			nodePool,
			deletingHC,
		).Build()
		deletingHCReconciler := secretJanitor{
			NodePoolReconciler: &NodePoolReconciler{
				Client: deletingHCClient,
			},
			now: fakeClock.Now,
		}

		userdataSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "user-data-nodepool-name-oldhash",
				Namespace: "myns",
				Annotations: map[string]string{
					nodePoolAnnotation: client.ObjectKeyFromObject(nodePool).String(),
				},
			},
		}
		if err := deletingHCClient.Create(ctx, userdataSecret); err != nil {
			t.Fatalf("failed to create userdata secret: %v", err)
		}

		key := client.ObjectKeyFromObject(userdataSecret)
		if _, err := deletingHCReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key}); err != nil {
			t.Fatalf("failed to reconcile: %v", err)
		}

		got := &corev1.Secret{}
		err := deletingHCClient.Get(ctx, key, got)
		if !apierrors.IsNotFound(err) {
			t.Errorf("expected userdata secret to be deleted, got error: %v", err)
		}
	})

	t.Run("When management-side drift occurred, janitor should keep deployed token secret", func(t *testing.T) {
		ctx := t.Context()
		deployedHash := "oldhash456"
		driftNodePool := nodePool.DeepCopy()
		driftNodePool.Annotations = map[string]string{
			nodePoolAnnotationCurrentConfigVersion: deployedHash,
		}

		driftClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(
			driftNodePool,
			hostedCluster,
			pullSecret,
			machineConfig,
			f.ignitionConfigs[0], f.ignitionConfigs[1], f.ignitionConfigs[2],
			ignitionServerCACert,
		).Build()
		driftReconciler := secretJanitor{
			NodePoolReconciler: &NodePoolReconciler{
				Client:          driftClient,
				ReleaseProvider: &fakereleaseprovider.FakeReleaseProvider{Version: semver.MustParse("4.18.0").String()},
				ImageMetadataProvider: &fakeimagemetadataprovider.FakeRegistryClientImageMetadataProvider{Result: &dockerv1client.DockerImageConfig{Config: &docker10.DockerConfig{
					Labels: map[string]string{},
				}}},
			},
			now: fakeClock.Now,
		}

		deployedTokenSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "token-nodepool-name-" + deployedHash,
				Namespace: "myns",
				Annotations: map[string]string{
					nodePoolAnnotation: client.ObjectKeyFromObject(nodePool).String(),
				},
			},
		}
		if err := driftClient.Create(ctx, deployedTokenSecret); err != nil {
			t.Fatalf("failed to create deployed token secret: %v", err)
		}

		key := client.ObjectKeyFromObject(deployedTokenSecret)
		if _, err := driftReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key}); err != nil {
			t.Fatalf("failed to reconcile: %v", err)
		}

		got := &corev1.Secret{}
		if err := driftClient.Get(ctx, key, got); err != nil {
			t.Fatalf("failed to get deployed token secret: %v", err)
		}
		if _, hasExpiration := got.Annotations[hyperv1.IgnitionServerTokenExpirationTimestampAnnotation]; hasExpiration {
			t.Errorf("deployed token secret should not be expired, but has expiration annotation")
		}
	})

	t.Run("When management-side drift occurred, janitor should keep deployed userdata secret", func(t *testing.T) {
		ctx := t.Context()
		deployedHash := "oldhash789"
		driftNodePool := nodePool.DeepCopy()
		driftNodePool.Annotations = map[string]string{
			nodePoolAnnotationCurrentConfigVersion: deployedHash,
		}

		driftClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(
			driftNodePool,
			hostedCluster,
			pullSecret,
			machineConfig,
			f.ignitionConfigs[0], f.ignitionConfigs[1], f.ignitionConfigs[2],
			ignitionServerCACert,
		).Build()
		driftReconciler := secretJanitor{
			NodePoolReconciler: &NodePoolReconciler{
				Client:          driftClient,
				ReleaseProvider: &fakereleaseprovider.FakeReleaseProvider{Version: semver.MustParse("4.18.0").String()},
				ImageMetadataProvider: &fakeimagemetadataprovider.FakeRegistryClientImageMetadataProvider{Result: &dockerv1client.DockerImageConfig{Config: &docker10.DockerConfig{
					Labels: map[string]string{},
				}}},
			},
			now: fakeClock.Now,
		}

		deployedUserdataSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "user-data-nodepool-name-" + deployedHash,
				Namespace: "myns",
				Annotations: map[string]string{
					nodePoolAnnotation: client.ObjectKeyFromObject(nodePool).String(),
				},
			},
		}
		if err := driftClient.Create(ctx, deployedUserdataSecret); err != nil {
			t.Fatalf("failed to create deployed userdata secret: %v", err)
		}

		key := client.ObjectKeyFromObject(deployedUserdataSecret)
		if _, err := driftReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key}); err != nil {
			t.Fatalf("failed to reconcile: %v", err)
		}

		// Secret should still exist (not deleted)
		got := &corev1.Secret{}
		if err := driftClient.Get(ctx, key, got); err != nil {
			t.Fatalf("deployed userdata secret should exist but got error: %v", err)
		}
	})
}

func assertJanitorResult(t *testing.T, c client.Client, ctx context.Context, input, expected *corev1.Secret, key client.ObjectKey, logs *observer.ObservedLogs) {
	t.Helper()
	got := &corev1.Secret{}
	err := c.Get(ctx, client.ObjectKeyFromObject(input), got)
	if expected == nil {
		if !apierrors.IsNotFound(err) {
			t.Errorf("expected object to not exist, got error: %v", err)
		}
		deletedLogs := logs.FilterMessage("Deleted secret").All()
		if len(deletedLogs) != 1 {
			t.Fatalf("expected one deletion log, got %d", len(deletedLogs))
		}
		if got := deletedLogs[0].ContextMap()["secret"]; got != key.String() {
			t.Errorf("expected secret log field %q, got %v", key.String(), got)
		}
	} else {
		if err != nil {
			t.Errorf("failed to fetch object: %v", err)
		}
		if diff := cmp.Diff(got, expected, cmpopts.IgnoreFields(metav1.ObjectMeta{}, "ResourceVersion")); diff != "" {
			t.Errorf("got unexpected object after reconcile: %v", diff)
		}
	}
}

func TestShouldKeepOldUserData(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	pullSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "pull-secret", Namespace: "test"},
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: []byte("whatever"),
		},
	}

	testCases := []struct {
		name                 string
		hc                   *hyperv1.HostedCluster
		releaseProvider      *releaseinfo.MockProviderWithRegistryOverrides
		releaseMockedVersion string
		expected             bool
	}{
		{
			name: "When hosted cluster is not aws or kubevirt, it should NOT keep old user data",
			hc: &hyperv1.HostedCluster{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Namespace: pullSecret.Namespace,
				},
				Spec: hyperv1.HostedClusterSpec{
					PullSecret: corev1.LocalObjectReference{
						Name: pullSecret.Name,
					},
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AgentPlatform,
					},
					Release: hyperv1.Release{
						Image: "fake-release-image",
					},
				},
				Status: hyperv1.HostedClusterStatus{},
			},
			releaseProvider: releaseinfo.NewMockProviderWithRegistryOverrides(mockCtrl),
			expected:        false,
		},
		{
			name: "When hosted cluster is kubevirt, it should keep old user data",
			hc: &hyperv1.HostedCluster{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Namespace: pullSecret.Namespace,
				},
				Spec: hyperv1.HostedClusterSpec{
					PullSecret: corev1.LocalObjectReference{
						Name: pullSecret.Name,
					},
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.KubevirtPlatform,
					},
					Release: hyperv1.Release{
						Image: "fake-release-image",
					},
				},
				Status: hyperv1.HostedClusterStatus{},
			},
			releaseProvider: releaseinfo.NewMockProviderWithRegistryOverrides(mockCtrl),
			expected:        true,
		},
		{
			name: "When hosted cluster is less than 4.16, it should keep user data",
			hc: &hyperv1.HostedCluster{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Namespace: pullSecret.Namespace,
				},
				Spec: hyperv1.HostedClusterSpec{
					PullSecret: corev1.LocalObjectReference{
						Name: pullSecret.Name,
					},
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
					},
					Release: hyperv1.Release{
						Image: "fake-release-image",
					},
				},
				Status: hyperv1.HostedClusterStatus{},
			},
			releaseProvider:      releaseinfo.NewMockProviderWithRegistryOverrides(mockCtrl),
			releaseMockedVersion: "4.15.0",
			expected:             true,
		},
		{
			name: "When hosted cluster is equal or greater than 4.16, it should NOT keep user data",
			hc: &hyperv1.HostedCluster{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Namespace: pullSecret.Namespace,
				},
				Spec: hyperv1.HostedClusterSpec{
					PullSecret: corev1.LocalObjectReference{
						Name: pullSecret.Name,
					},
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
					},
					Release: hyperv1.Release{
						Image: "fake-release-image",
					},
				},
			},
			releaseProvider:      releaseinfo.NewMockProviderWithRegistryOverrides(mockCtrl),
			releaseMockedVersion: "4.16.0",
			expected:             false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(
				pullSecret,
			).Build()

			r := &NodePoolReconciler{
				Client:          c,
				ReleaseProvider: tc.releaseProvider,
			}
			if len(tc.releaseMockedVersion) > 0 {
				releaseImage := testutils.InitReleaseImageOrDie(tc.releaseMockedVersion)
				tc.releaseProvider.EXPECT().Lookup(gomock.Any(), gomock.Any(), gomock.Any()).Return(releaseImage, nil).AnyTimes()
			}

			shouldKeepOldUserData, err := r.shouldKeepOldUserData(t.Context(), tc.hc)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(shouldKeepOldUserData).To(Equal(tc.expected))
		})
	}
}
