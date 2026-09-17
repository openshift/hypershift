package nodepool

import (
	"context"
	"fmt"
	"reflect"
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
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	testingclock "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/blang/semver"
	"github.com/go-logr/zapr"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap/zaptest"
)

type secretJanitorTestFixture struct {
	ctx           context.Context
	client        client.WithWatch
	hostedCluster *hyperv1.HostedCluster
	nodePool      *hyperv1.NodePool
	reconciler    *secretJanitor
}

func newSecretJanitorTestFixture(t *testing.T, platform hyperv1.PlatformType) *secretJanitorTestFixture {
	t.Helper()
	ctx := ctrl.LoggerInto(t.Context(), zapr.NewLogger(zaptest.NewLogger(t)))
	theTime, err := time.Parse(time.RFC3339Nano, "2006-01-02T15:04:05.999999999Z")
	if err != nil {
		t.Fatalf("could not parse time: %v", err)
	}
	fakeClock := testingclock.NewFakeClock(theTime)

	pullSecret, ignitionServerCACert, machineConfig, ignitionConfig, ignitionConfig2, ignitionConfig3 := setupTestObjects()
	hostedCluster := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-name", Namespace: "myns"},
		Spec: hyperv1.HostedClusterSpec{
			PullSecret: corev1.LocalObjectReference{Name: pullSecret.Name},
			Platform:   hyperv1.PlatformSpec{Type: platform},
		},
		Status: hyperv1.HostedClusterStatus{
			IgnitionEndpoint: "https://ignition.cluster-name.myns.devcluster.openshift.com",
		},
	}
	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{Name: "nodepool-name", Namespace: "myns"},
		Spec: hyperv1.NodePoolSpec{
			ClusterName: hostedCluster.Name,
			Platform:    hyperv1.NodePoolPlatform{Type: platform},
			Release:     hyperv1.Release{Image: "fake-release-image"},
			Config:      []corev1.LocalObjectReference{{Name: "machineconfig-1"}},
		},
		// Keep the NodePool version at 4.18 so token generation does not hide
		// the behavior under test behind a release change.
		Status: hyperv1.NodePoolStatus{Version: semver.MustParse("4.18.0").String()},
	}

	c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(
		nodePool,
		hostedCluster,
		pullSecret,
		machineConfig,
		ignitionConfig,
		ignitionConfig2,
		ignitionConfig3,
		ignitionServerCACert,
	).Build()
	r := &secretJanitor{
		NodePoolReconciler: &NodePoolReconciler{
			Client:          c,
			ReleaseProvider: &fakereleaseprovider.FakeReleaseProvider{Version: semver.MustParse("4.18.0").String()},
			ImageMetadataProvider: &fakeimagemetadataprovider.FakeRegistryClientImageMetadataProvider{Result: &dockerv1client.DockerImageConfig{Config: &docker10.DockerConfig{
				Labels: map[string]string{},
			}}},
		},
		now: fakeClock.Now,
	}
	return &secretJanitorTestFixture{ctx: ctx, client: c, hostedCluster: hostedCluster, nodePool: nodePool, reconciler: r}
}

func TestSecretJanitor_Reconcile(t *testing.T) {
	ctx := ctrl.LoggerInto(t.Context(), zapr.NewLogger(zaptest.NewLogger(t)))
	mockCtrl := gomock.NewController(t)

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
		//We need the np.Status.Version to stay at 4.18 so that the token doesn't get updated when bumping releases,
		// this protects us from possibly hiding other factors that might be causing the token to be updated
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

	ignitionConfig := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "core-machineconfig",
			Namespace: "myns-cluster-name",
			Labels: map[string]string{
				nodePoolCoreIgnitionConfigLabel: "true",
			},
		},
		Data: map[string]string{
			TokenSecretConfigKey: coreMachineConfig,
		},
	}
	ignitionConfig2 := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "core-machineconfig-2",
			Namespace: "myns-cluster-name",
			Labels: map[string]string{
				nodePoolCoreIgnitionConfigLabel: "true",
			},
		},
		Data: map[string]string{
			TokenSecretConfigKey: coreMachineConfig,
		},
	}
	ignitionConfig3 := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "core-machineconfig-3",
			Namespace: "myns-cluster-name",
			Labels: map[string]string{
				nodePoolCoreIgnitionConfigLabel: "true",
			},
		},
		Data: map[string]string{
			TokenSecretConfigKey: coreMachineConfig,
		},
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
		ignitionConfig,
		ignitionConfig2,
		ignitionConfig3,
		ignitionServerCACert,
	).Build()
	mockedReleaseProvider := releaseinfo.NewMockProviderWithRegistryOverrides(mockCtrl)
	//We need the ReleaseProvider to stay at 4.18 so that the token doesn't get updated when bumping releases,
	// this protects us from possibly hiding other factors that might be causing the token to be updated
	mockedReleaseProvider.EXPECT().Lookup(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(testutils.InitReleaseImageOrDie("4.18.0"), nil).AnyTimes()
	r := secretJanitor{
		NodePoolReconciler: &NodePoolReconciler{
			Client: c,
			//We need the ReleaseProvider to stay at 4.18 so that the token doesn't get updated when bumping releases,
			// this protects us from possibly hiding other factors that might be causing the token to be updated
			ReleaseProvider: &fakereleaseprovider.FakeReleaseProvider{Version: semver.MustParse("4.18.0").String()},
			ImageMetadataProvider: &fakeimagemetadataprovider.FakeRegistryClientImageMetadataProvider{Result: &dockerv1client.DockerImageConfig{Config: &docker10.DockerConfig{
				Labels: map[string]string{},
			}}},
		},

		now: fakeClock.Now,
	}

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
			if err := c.Create(ctx, testCase.input); err != nil {
				t.Errorf("failed to create object: %v", err)
			}

			key := client.ObjectKeyFromObject(testCase.input)
			if _, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: key}); err != nil {
				t.Errorf("failed to reconcile object: %v", err)
			}

			got := &corev1.Secret{}
			err := c.Get(ctx, client.ObjectKeyFromObject(testCase.input), got)
			if testCase.expected == nil {
				if !apierrors.IsNotFound(err) {
					t.Errorf("expected object to not exist, got error: %v", err)
				}
			} else {
				if err != nil {
					t.Errorf("failed to fetch object: %v", err)
				}
				if diff := cmp.Diff(got, testCase.expected, cmpopts.IgnoreFields(metav1.ObjectMeta{}, "ResourceVersion")); diff != "" {
					t.Errorf("got unexpected object after reconcile: %v", diff)
				}
			}
		})
	}

	t.Run("When the hosted cluster is not found it should clean up token secret", func(t *testing.T) {
		// Create a client with the nodePool but without the hostedCluster.
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
		// Create a client with the nodePool but without the hostedCluster.
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

}

func TestSecretJanitor_ReconcileKubeVirt(t *testing.T) {
	t.Run("When an outdated KubeVirt token Secret is reconciled, it should preserve existing behavior", func(t *testing.T) {
		fixture := newSecretJanitorTestFixture(t, hyperv1.KubevirtPlatform)
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "token-nodepool-name-jsadfkjh23",
				Namespace: "myns",
				Annotations: map[string]string{
					nodePoolAnnotation: client.ObjectKeyFromObject(fixture.nodePool).String(),
				},
			},
		}
		if err := fixture.client.Create(fixture.ctx, secret); err != nil {
			t.Fatalf("failed to create token Secret: %v", err)
		}

		if _, err := fixture.reconciler.Reconcile(fixture.ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(secret)}); err != nil {
			t.Fatalf("failed to reconcile: %v", err)
		}
		got := &corev1.Secret{}
		if err := fixture.client.Get(fixture.ctx, client.ObjectKeyFromObject(secret), got); err != nil {
			t.Fatalf("failed to get token Secret: %v", err)
		}
		expectedAnnotations := map[string]string{nodePoolAnnotation: client.ObjectKeyFromObject(fixture.nodePool).String()}
		if diff := cmp.Diff(got.Annotations, expectedAnnotations); diff != "" {
			t.Fatalf("unexpected annotations: %s", diff)
		}
	})

	for _, tc := range []struct {
		name          string
		kind          string
		oldSecretName string
	}{
		{name: "When a MachineSet changes userdata Secret and reaches zero replicas, it should clean up the old Secret", kind: "MachineSet", oldSecretName: "user-data-nodepool-name-old-machineset-event"},
		{name: "When a MachineDeployment changes userdata Secret and reaches zero replicas, it should clean up the old Secret", kind: "MachineDeployment", oldSecretName: "user-data-nodepool-name-old-machinedeployment-event"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newSecretJanitorTestFixture(t, hyperv1.KubevirtPlatform)
			testKubeVirtUserDataEventCleanup(t, fixture, tc.kind, tc.oldSecretName)
		})
	}

	t.Run("When a userdata Secret becomes referenced during reconciliation, it should not be deleted", func(t *testing.T) {
		fixture := newSecretJanitorTestFixture(t, hyperv1.KubevirtPlatform)
		const secretName = "user-data-nodepool-name-final-recheck"
		userdataSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretName,
				Namespace: "myns-cluster-name",
				Annotations: map[string]string{
					nodePoolAnnotation: client.ObjectKeyFromObject(fixture.nodePool).String(),
				},
			},
		}
		if err := fixture.client.Create(fixture.ctx, userdataSecret); err != nil {
			t.Fatalf("failed to create userdata Secret: %v", err)
		}

		machineListCalls := 0
		fixture.reconciler.Client = interceptor.NewClient(fixture.client, interceptor.Funcs{
			List: func(ctx context.Context, underlying client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				if _, ok := list.(*capiv1.MachineList); ok {
					machineListCalls++
					if machineListCalls == 2 {
						machine := &capiv1.Machine{
							ObjectMeta: metav1.ObjectMeta{Name: "machine-final-recheck", Namespace: "myns-cluster-name"},
							Spec:       capiv1.MachineSpec{Bootstrap: capiv1.Bootstrap{DataSecretName: ptr.To(secretName)}},
						}
						if err := underlying.Create(ctx, machine); err != nil {
							return err
						}
					}
				}
				return underlying.List(ctx, list, opts...)
			},
		})

		if _, err := fixture.reconciler.Reconcile(fixture.ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(userdataSecret)}); err != nil {
			t.Fatalf("failed to reconcile: %v", err)
		}
		if err := fixture.client.Get(fixture.ctx, client.ObjectKeyFromObject(userdataSecret), &corev1.Secret{}); err != nil {
			t.Fatalf("expected referenced userdata Secret to remain: %v", err)
		}
	})
}

func testKubeVirtUserDataEventCleanup(t *testing.T, fixture *secretJanitorTestFixture, kind, oldSecretName string) {
	const (
		controlPlaneNamespace = "myns-cluster-name"
		currentSecretName     = "user-data-nodepool-name-64587037"
	)
	oldSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Name: oldSecretName, Namespace: controlPlaneNamespace,
		Annotations: map[string]string{nodePoolAnnotation: client.ObjectKeyFromObject(fixture.nodePool).String()},
	}}
	currentSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Name: currentSecretName, Namespace: controlPlaneNamespace,
		Annotations: map[string]string{nodePoolAnnotation: client.ObjectKeyFromObject(fixture.nodePool).String()},
	}}
	if err := fixture.client.Create(fixture.ctx, oldSecret); err != nil {
		t.Fatalf("failed to create old userdata Secret: %v", err)
	}
	if err := fixture.client.Create(fixture.ctx, currentSecret); err != nil {
		t.Fatalf("failed to create current userdata Secret: %v", err)
	}

	oldObject, newObject := newKubeVirtCAPIUserDataUpdate(kind, fixture.nodePool, oldSecretName, currentSecretName)
	if err := fixture.client.Create(fixture.ctx, oldObject); err != nil {
		t.Fatalf("failed to create old %s: %v", kind, err)
	}
	newObject.SetResourceVersion(oldObject.GetResourceVersion())
	if err := fixture.client.Update(fixture.ctx, newObject); err != nil {
		t.Fatalf("failed to update %s: %v", kind, err)
	}

	updateEvent := event.UpdateEvent{ObjectOld: oldObject, ObjectNew: newObject}
	if !predicate.Or(capiDeletionOnlyPredicate(), capiUserDataStateChangedPredicate()).Update(updateEvent) {
		t.Fatal("expected userdata and replica update to be accepted")
	}
	queue := workqueue.NewTypedRateLimitingQueue[reconcile.Request](workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]())
	defer queue.ShutDown()
	handler.EnqueueRequestsFromMapFunc(fixture.reconciler.enqueueKubeVirtUserDataSecret).Update(fixture.ctx, updateEvent, queue)

	expectedRequests := map[reconcile.Request]bool{
		{NamespacedName: types.NamespacedName{Namespace: controlPlaneNamespace, Name: oldSecretName}}:     true,
		{NamespacedName: types.NamespacedName{Namespace: controlPlaneNamespace, Name: currentSecretName}}: true,
	}
	gotRequests := map[reconcile.Request]bool{}
	for queue.Len() > 0 {
		request, shutdown := queue.Get()
		if shutdown {
			t.Fatal("queue shut down before all requests were received")
		}
		gotRequests[request] = true
		queue.Done(request)
	}
	if diff := cmp.Diff(expectedRequests, gotRequests); diff != "" {
		t.Fatalf("unexpected Secret requests: %s", diff)
	}
	for request := range gotRequests {
		if _, err := fixture.reconciler.Reconcile(fixture.ctx, request); err != nil {
			t.Fatalf("failed to reconcile %s: %v", request.NamespacedName, err)
		}
	}
	if err := fixture.client.Get(fixture.ctx, client.ObjectKeyFromObject(oldSecret), &corev1.Secret{}); !apierrors.IsNotFound(err) {
		t.Fatalf("expected old userdata Secret to be deleted, got error: %v", err)
	}
	if err := fixture.client.Get(fixture.ctx, client.ObjectKeyFromObject(currentSecret), &corev1.Secret{}); err != nil {
		t.Fatalf("expected current userdata Secret to remain: %v", err)
	}
}

func newKubeVirtCAPIUserDataUpdate(kind string, nodePool *hyperv1.NodePool, oldSecretName, currentSecretName string) (client.Object, client.Object) {
	controlPlaneNamespace := "myns-cluster-name"
	annotations := map[string]string{nodePoolAnnotation: client.ObjectKeyFromObject(nodePool).String()}
	oneReplica := int32(1)
	zeroReplicas := int32(0)
	switch kind {
	case "MachineSet":
		oldObject := &capiv1.MachineSet{
			ObjectMeta: metav1.ObjectMeta{Name: "machineset-userdata-event", Namespace: controlPlaneNamespace, Annotations: annotations},
			Spec: capiv1.MachineSetSpec{Replicas: &oneReplica, Template: capiv1.MachineTemplateSpec{Spec: capiv1.MachineSpec{
				Bootstrap: capiv1.Bootstrap{DataSecretName: ptr.To(oldSecretName)},
			}}},
		}
		newObject := oldObject.DeepCopy()
		newObject.Spec.Replicas = &zeroReplicas
		newObject.Status.Replicas = &zeroReplicas
		newObject.Spec.Template.Spec.Bootstrap.DataSecretName = ptr.To(currentSecretName)
		return oldObject, newObject
	case "MachineDeployment":
		oldObject := &capiv1.MachineDeployment{
			ObjectMeta: metav1.ObjectMeta{Name: "machinedeployment-userdata-event", Namespace: controlPlaneNamespace, Annotations: annotations},
			Spec: capiv1.MachineDeploymentSpec{Replicas: &oneReplica, Template: capiv1.MachineTemplateSpec{Spec: capiv1.MachineSpec{
				Bootstrap: capiv1.Bootstrap{DataSecretName: ptr.To(oldSecretName)},
			}}},
		}
		newObject := oldObject.DeepCopy()
		newObject.Spec.Replicas = &zeroReplicas
		newObject.Status.Replicas = &zeroReplicas
		newObject.Spec.Template.Spec.Bootstrap.DataSecretName = ptr.To(currentSecretName)
		return oldObject, newObject
	default:
		panic(fmt.Sprintf("unsupported CAPI object kind %q", kind))
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
	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{Name: "nodepool", Namespace: pullSecret.Namespace},
		Spec:       hyperv1.NodePoolSpec{ClusterName: "cluster"},
	}
	userDataSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "user-data-nodepool-old", Namespace: "test-cluster"},
	}
	oldMachine := &capiv1.Machine{
		ObjectMeta: metav1.ObjectMeta{Name: "old-machine", Namespace: userDataSecret.Namespace},
		Spec:       capiv1.MachineSpec{Bootstrap: capiv1.Bootstrap{DataSecretName: ptr.To(userDataSecret.Name)}},
	}

	testCases := []struct {
		name                 string
		hc                   *hyperv1.HostedCluster
		releaseProvider      *releaseinfo.MockProviderWithRegistryOverrides
		releaseMockedVersion string
		objects              []client.Object
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
			objects:         []client.Object{oldMachine},
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
			objects := []client.Object{pullSecret, nodePool, userDataSecret}
			objects = append(objects, tc.objects...)
			c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(objects...).Build()

			r := &NodePoolReconciler{
				Client:          c,
				ReleaseProvider: tc.releaseProvider,
			}
			if len(tc.releaseMockedVersion) > 0 {
				releaseImage := testutils.InitReleaseImageOrDie(tc.releaseMockedVersion)
				tc.releaseProvider.EXPECT().Lookup(gomock.Any(), gomock.Any(), gomock.Any()).Return(releaseImage, nil).AnyTimes()
			}

			shouldKeepOldUserData, err := r.shouldKeepOldUserData(t.Context(), tc.hc, nodePool, userDataSecret)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(shouldKeepOldUserData).To(Equal(tc.expected))
		})
	}
}

func TestUserDataSecretInUse(t *testing.T) {
	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{Name: "nodepool", Namespace: "test"},
		Spec:       hyperv1.NodePoolSpec{ClusterName: "cluster"},
	}
	const (
		controlPlaneNamespace = "test-cluster"
		secretName            = "user-data-nodepool-old"
	)

	positiveReplicas := int32(1)
	zeroReplicas := int32(0)
	now := metav1.Now()

	testCases := []struct {
		name            string
		objects         []client.Object
		listErrorFor    client.ObjectList
		expected        bool
		expectedErrText string
	}{
		{
			name:     "When no bootstrap consumer references the Secret, it should report the Secret is unused",
			expected: false,
		},
		{
			name: "When a Machine references the Secret without a NodePool annotation, it should retain the Secret",
			objects: []client.Object{&capiv1.Machine{
				ObjectMeta: metav1.ObjectMeta{Name: "machine", Namespace: controlPlaneNamespace},
				Spec:       capiv1.MachineSpec{Bootstrap: capiv1.Bootstrap{DataSecretName: ptr.To(secretName)}},
			}},
			expected: true,
		},
		{
			name: "When a Machine is terminating but still references the Secret, it should retain the Secret",
			objects: []client.Object{&capiv1.Machine{
				ObjectMeta: metav1.ObjectMeta{Name: "machine", Namespace: controlPlaneNamespace, DeletionTimestamp: &now, Finalizers: []string{"test"}},
				Spec:       capiv1.MachineSpec{Bootstrap: capiv1.Bootstrap{DataSecretName: ptr.To(secretName)}},
			}},
			expected: true,
		},
		{
			name: "When an active MachineSet template references the Secret, it should retain the Secret",
			objects: []client.Object{&capiv1.MachineSet{
				ObjectMeta: metav1.ObjectMeta{Name: "machineset", Namespace: controlPlaneNamespace},
				Spec: capiv1.MachineSetSpec{
					Replicas: &positiveReplicas,
					Template: capiv1.MachineTemplateSpec{Spec: capiv1.MachineSpec{Bootstrap: capiv1.Bootstrap{DataSecretName: ptr.To(secretName)}}},
				},
			}},
			expected: true,
		},
		{
			name: "When a MachineSet template references the Secret but its existing replicas are unknown, it should retain the Secret",
			objects: []client.Object{&capiv1.MachineSet{
				ObjectMeta: metav1.ObjectMeta{Name: "machineset", Namespace: controlPlaneNamespace},
				Spec: capiv1.MachineSetSpec{
					Replicas: &zeroReplicas,
					Template: capiv1.MachineTemplateSpec{Spec: capiv1.MachineSpec{Bootstrap: capiv1.Bootstrap{DataSecretName: ptr.To(secretName)}}},
				},
			}},
			expected: true,
		},
		{
			name: "When a deleting MachineSet template references the Secret, it should retain the Secret",
			objects: []client.Object{&capiv1.MachineSet{
				ObjectMeta: metav1.ObjectMeta{Name: "machineset", Namespace: controlPlaneNamespace, DeletionTimestamp: &now, Finalizers: []string{"test"}},
				Spec: capiv1.MachineSetSpec{
					Replicas: &zeroReplicas,
					Template: capiv1.MachineTemplateSpec{Spec: capiv1.MachineSpec{Bootstrap: capiv1.Bootstrap{DataSecretName: ptr.To(secretName)}}},
				},
			}},
			expected: true,
		},
		{
			name: "When an active MachineDeployment template references the Secret, it should retain the Secret",
			objects: []client.Object{&capiv1.MachineDeployment{
				ObjectMeta: metav1.ObjectMeta{Name: "machinedeployment", Namespace: controlPlaneNamespace},
				Spec: capiv1.MachineDeploymentSpec{
					Replicas: &positiveReplicas,
					Template: capiv1.MachineTemplateSpec{Spec: capiv1.MachineSpec{Bootstrap: capiv1.Bootstrap{DataSecretName: ptr.To(secretName)}}},
				},
			}},
			expected: true,
		},
		{
			name:            "When listing Machines fails, it should retain the Secret and return an error",
			listErrorFor:    &capiv1.MachineList{},
			expected:        true,
			expectedErrText: "failed to list Machines",
		},
		{
			name:            "When listing MachineSets fails, it should retain the Secret and return an error",
			listErrorFor:    &capiv1.MachineSetList{},
			expected:        true,
			expectedErrText: "failed to list MachineSets",
		},
		{
			name:            "When listing MachineDeployments fails, it should retain the Secret and return an error",
			listErrorFor:    &capiv1.MachineDeploymentList{},
			expected:        true,
			expectedErrText: "failed to list MachineDeployments",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			builder := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(tc.objects...)
			if tc.listErrorFor != nil {
				builder = builder.WithInterceptorFuncs(interceptor.Funcs{
					List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
						if reflect.TypeOf(list) == reflect.TypeOf(tc.listErrorFor) {
							return fmt.Errorf("simulated list error")
						}
						return c.List(ctx, list, opts...)
					},
				})
			}
			r := &NodePoolReconciler{Client: builder.Build()}

			got, err := r.userDataSecretInUse(t.Context(), secretName, nodePool)
			g.Expect(got).To(Equal(tc.expected), "unexpected user-data Secret usage result: %s", tc.name)
			if tc.expectedErrText == "" {
				g.Expect(err).ToNot(HaveOccurred(), "unexpected error checking user-data Secret usage: %s", tc.name)
			} else {
				g.Expect(err).To(MatchError(ContainSubstring(tc.expectedErrText)), "unexpected error checking user-data Secret usage: %s", tc.name)
			}
		})
	}
}

func TestBootstrapTemplateCanCreateMachine(t *testing.T) {
	positiveReplicas := int32(1)
	zeroReplicas := int32(0)
	now := metav1.Now()

	testCases := []struct {
		name           string
		deletionTime   *metav1.Time
		specReplicas   *int32
		statusReplicas *int32
		expectedInUse  bool
	}{
		{
			name:          "When the template is terminating, it should remain protected",
			deletionTime:  &now,
			specReplicas:  &zeroReplicas,
			expectedInUse: true,
		},
		{
			name:          "When desired replicas are unknown, it should remain protected",
			expectedInUse: true,
		},
		{
			name:          "When desired replicas are positive, it should remain protected",
			specReplicas:  &positiveReplicas,
			expectedInUse: true,
		},
		{
			name:           "When desired replicas are zero but existing replicas are positive, it should remain protected",
			specReplicas:   &zeroReplicas,
			statusReplicas: &positiveReplicas,
			expectedInUse:  true,
		},
		{
			name:          "When desired replicas are zero but existing replicas are unknown, it should remain protected",
			specReplicas:  &zeroReplicas,
			expectedInUse: true,
		},
		{
			name:           "When desired and existing replicas are zero, it should report no active consumer",
			specReplicas:   &zeroReplicas,
			statusReplicas: &zeroReplicas,
			expectedInUse:  false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(bootstrapTemplateCanCreateMachine(tc.deletionTime, tc.specReplicas, tc.statusReplicas)).To(Equal(tc.expectedInUse), "unexpected template consumer state: %s", tc.name)
		})
	}
}

func newCAPIUserDataObject(kind, namespace, secretName, infrastructureKind string, annotations map[string]string) client.Object {
	bootstrap := capiv1.Bootstrap{}
	if secretName != "" {
		bootstrap.DataSecretName = ptr.To(secretName)
	}
	machineSpec := capiv1.MachineSpec{
		Bootstrap:         bootstrap,
		InfrastructureRef: capiv1.ContractVersionedObjectReference{Kind: infrastructureKind},
	}
	objectMeta := metav1.ObjectMeta{Namespace: namespace, Annotations: annotations}
	switch kind {
	case "Machine":
		objectMeta.Name = "machine"
		return &capiv1.Machine{ObjectMeta: objectMeta, Spec: machineSpec}
	case "MachineSet":
		objectMeta.Name = "machineset"
		return &capiv1.MachineSet{ObjectMeta: objectMeta, Spec: capiv1.MachineSetSpec{Template: capiv1.MachineTemplateSpec{Spec: machineSpec}}}
	case "MachineDeployment":
		objectMeta.Name = "machinedeployment"
		return &capiv1.MachineDeployment{ObjectMeta: objectMeta, Spec: capiv1.MachineDeploymentSpec{Template: capiv1.MachineTemplateSpec{Spec: machineSpec}}}
	default:
		panic(fmt.Sprintf("unsupported CAPI object kind %q", kind))
	}
}

func TestEnqueueKubeVirtUserDataSecret(t *testing.T) {
	const (
		nodePoolNamespace = "myns"
		machineNamespace  = "myns-cluster"
	)

	testCases := []struct {
		name         string
		platformType hyperv1.PlatformType
		kind         string
		secretName   string
		expected     []reconcile.Request
	}{
		{
			name:         "When a KubeVirt Machine references a user-data Secret, it should enqueue that Secret",
			platformType: hyperv1.KubevirtPlatform,
			kind:         "Machine",
			secretName:   "user-data-nodepool-old",
			expected:     []reconcile.Request{{NamespacedName: types.NamespacedName{Namespace: machineNamespace, Name: "user-data-nodepool-old"}}},
		},
		{
			name:         "When a KubeVirt MachineSet references a user-data Secret, it should enqueue that Secret",
			platformType: hyperv1.KubevirtPlatform,
			kind:         "MachineSet",
			secretName:   "user-data-nodepool-old",
			expected:     []reconcile.Request{{NamespacedName: types.NamespacedName{Namespace: machineNamespace, Name: "user-data-nodepool-old"}}},
		},
		{
			name:         "When a KubeVirt MachineDeployment references a user-data Secret, it should enqueue that Secret",
			platformType: hyperv1.KubevirtPlatform,
			kind:         "MachineDeployment",
			secretName:   "user-data-nodepool-old",
			expected:     []reconcile.Request{{NamespacedName: types.NamespacedName{Namespace: machineNamespace, Name: "user-data-nodepool-old"}}},
		},
		{
			name:         "When the NodePool is Azure, it should not enqueue the referenced Secret",
			platformType: hyperv1.AzurePlatform,
			kind:         "Machine",
			secretName:   "user-data-nodepool-old",
		},
		{
			name:         "When a Machine references a token Secret, it should not enqueue a Secret",
			platformType: hyperv1.KubevirtPlatform,
			kind:         "Machine",
			secretName:   "token-nodepool-old",
		},
		{
			name:         "When a Machine has no bootstrap Secret, it should not enqueue a Secret",
			platformType: hyperv1.KubevirtPlatform,
			kind:         "Machine",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			nodePool := &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "nodepool",
					Namespace: nodePoolNamespace,
				},
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{Type: tc.platformType},
				},
			}
			capiObject := newCAPIUserDataObject(tc.kind, machineNamespace, tc.secretName, "", map[string]string{
				nodePoolAnnotation: client.ObjectKeyFromObject(nodePool).String(),
			})
			c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(nodePool).Build()
			r := &NodePoolReconciler{Client: c}

			NewWithT(t).Expect(r.enqueueKubeVirtUserDataSecret(t.Context(), capiObject)).To(Equal(tc.expected), "unexpected Secret reconcile requests: %s", tc.name)
		})
	}
}

func TestEnqueueKubeVirtUserDataSecretWhenNodePoolLookupFails(t *testing.T) {
	const (
		nodePoolNamespace = "myns"
		machineNamespace  = "myns-cluster"
		secretName        = "user-data-nodepool-old"
	)

	testCases := []struct {
		name               string
		kind               string
		infrastructureKind string
		expected           []reconcile.Request
	}{
		{
			name:               "When a KubeVirt Machine references a user-data Secret, it should enqueue the Secret",
			kind:               "Machine",
			infrastructureKind: "KubevirtMachine",
			expected: []reconcile.Request{{NamespacedName: types.NamespacedName{
				Namespace: machineNamespace,
				Name:      secretName,
			}}},
		},
		{
			name:               "When a KubeVirt MachineSet references a user-data Secret, it should enqueue the Secret",
			kind:               "MachineSet",
			infrastructureKind: "KubevirtMachineTemplate",
			expected: []reconcile.Request{{NamespacedName: types.NamespacedName{
				Namespace: machineNamespace,
				Name:      secretName,
			}}},
		},
		{
			name:               "When a KubeVirt MachineDeployment references a user-data Secret, it should enqueue the Secret",
			kind:               "MachineDeployment",
			infrastructureKind: "KubevirtMachineTemplate",
			expected: []reconcile.Request{{NamespacedName: types.NamespacedName{
				Namespace: machineNamespace,
				Name:      secretName,
			}}},
		},
		{
			name:               "When a non-KubeVirt Machine references a user-data Secret, it should not enqueue the Secret",
			kind:               "Machine",
			infrastructureKind: "AWSMachineTemplate",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			capiObject := newCAPIUserDataObject(tc.kind, machineNamespace, secretName, tc.infrastructureKind, map[string]string{
				nodePoolAnnotation: nodePoolNamespace + "/nodepool",
			})
			c := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithInterceptorFuncs(interceptor.Funcs{
					Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
						return fmt.Errorf("simulated NodePool lookup failure")
					},
				}).
				Build()
			r := &NodePoolReconciler{Client: c}

			NewWithT(t).Expect(r.enqueueKubeVirtUserDataSecret(t.Context(), capiObject)).To(Equal(tc.expected), "unexpected Secret reconcile requests after NodePool lookup failure: %s", tc.name)
		})
	}
}

func TestCAPIUserDataStateChangedPredicate(t *testing.T) {
	positiveReplicas := int32(1)
	zeroReplicas := int32(0)
	now := metav1.Now()

	newMachine := func() *capiv1.Machine {
		return &capiv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "machine", Namespace: "test-cluster"}}
	}
	newMachineSet := func() *capiv1.MachineSet {
		return &capiv1.MachineSet{ObjectMeta: metav1.ObjectMeta{Name: "machineset", Namespace: "test-cluster"}}
	}
	newMachineDeployment := func() *capiv1.MachineDeployment {
		return &capiv1.MachineDeployment{ObjectMeta: metav1.ObjectMeta{Name: "machinedeployment", Namespace: "test-cluster"}}
	}

	testCases := []struct {
		name      string
		oldObject client.Object
		newObject client.Object
		expected  bool
	}{
		{
			name: "When a Machine bootstrap Secret changes, it should accept the update",
			oldObject: func() client.Object {
				machine := newMachine()
				machine.Spec.Bootstrap.DataSecretName = ptr.To("user-data-old")
				return machine
			}(),
			newObject: func() client.Object {
				machine := newMachine()
				machine.Spec.Bootstrap.DataSecretName = ptr.To("user-data-new")
				return machine
			}(),
			expected: true,
		},
		{
			name:      "When a Machine starts deletion, it should accept the update",
			oldObject: newMachine(),
			newObject: func() client.Object { machine := newMachine(); machine.DeletionTimestamp = &now; return machine }(),
			expected:  true,
		},
		{
			name: "When an unrelated Machine field changes, it should ignore the update",
			oldObject: func() client.Object {
				machine := newMachine()
				machine.Labels = map[string]string{"key": "old"}
				return machine
			}(),
			newObject: func() client.Object {
				machine := newMachine()
				machine.Labels = map[string]string{"key": "new"}
				return machine
			}(),
			expected: false,
		},
		{
			name: "When a MachineSet bootstrap Secret changes, it should accept the update",
			oldObject: func() client.Object {
				machineSet := newMachineSet()
				machineSet.Spec.Template.Spec.Bootstrap.DataSecretName = ptr.To("user-data-old")
				return machineSet
			}(),
			newObject: func() client.Object {
				machineSet := newMachineSet()
				machineSet.Spec.Template.Spec.Bootstrap.DataSecretName = ptr.To("user-data-new")
				return machineSet
			}(),
			expected: true,
		},
		{
			name: "When a MachineSet desired replicas change, it should accept the update",
			oldObject: func() client.Object {
				machineSet := newMachineSet()
				machineSet.Spec.Replicas = &positiveReplicas
				return machineSet
			}(),
			newObject: func() client.Object {
				machineSet := newMachineSet()
				machineSet.Spec.Replicas = &zeroReplicas
				return machineSet
			}(),
			expected: true,
		},
		{
			name: "When a MachineSet observed replicas change, it should accept the update",
			oldObject: func() client.Object {
				machineSet := newMachineSet()
				machineSet.Status.Replicas = &positiveReplicas
				return machineSet
			}(),
			newObject: func() client.Object {
				machineSet := newMachineSet()
				machineSet.Status.Replicas = &zeroReplicas
				return machineSet
			}(),
			expected: true,
		},
		{
			name: "When an unrelated MachineSet field changes, it should ignore the update",
			oldObject: func() client.Object {
				machineSet := newMachineSet()
				machineSet.Labels = map[string]string{"key": "old"}
				return machineSet
			}(),
			newObject: func() client.Object {
				machineSet := newMachineSet()
				machineSet.Labels = map[string]string{"key": "new"}
				return machineSet
			}(),
			expected: false,
		},
		{
			name: "When a MachineDeployment desired replicas change, it should accept the update",
			oldObject: func() client.Object {
				machineDeployment := newMachineDeployment()
				machineDeployment.Spec.Replicas = &positiveReplicas
				return machineDeployment
			}(),
			newObject: func() client.Object {
				machineDeployment := newMachineDeployment()
				machineDeployment.Spec.Replicas = &zeroReplicas
				return machineDeployment
			}(),
			expected: true,
		},
		{
			name:      "When a MachineDeployment starts deletion, it should accept the update",
			oldObject: newMachineDeployment(),
			newObject: func() client.Object {
				machineDeployment := newMachineDeployment()
				machineDeployment.DeletionTimestamp = &now
				return machineDeployment
			}(),
			expected: true,
		},
		{
			name:      "When a MachineDeployment has no relevant changes, it should ignore the update",
			oldObject: newMachineDeployment(),
			newObject: newMachineDeployment(),
			expected:  false,
		},
	}

	p := capiUserDataStateChangedPredicate()
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			NewWithT(t).Expect(p.Update(event.UpdateEvent{ObjectOld: tc.oldObject, ObjectNew: tc.newObject})).To(Equal(tc.expected), "unexpected predicate result: %s", tc.name)
		})
	}
}

func TestCAPIUserDataStateChangedPredicateCreate(t *testing.T) {
	p := capiUserDataStateChangedPredicate()
	testCases := []struct {
		name   string
		object client.Object
	}{
		{
			name:   "When a Machine is created, it should accept the event",
			object: &capiv1.Machine{},
		},
		{
			name:   "When a MachineSet is created, it should accept the event",
			object: &capiv1.MachineSet{},
		},
		{
			name:   "When a MachineDeployment is created, it should accept the event",
			object: &capiv1.MachineDeployment{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			NewWithT(t).Expect(p.Create(event.CreateEvent{Object: tc.object})).To(BeTrue(), "create predicate rejected event: %s", tc.name)
		})
	}
}
