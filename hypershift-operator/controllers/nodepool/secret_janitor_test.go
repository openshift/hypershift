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
	"k8s.io/utils/ptr"

	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
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
	"go.uber.org/zap/zaptest"
	"go.uber.org/zap/zaptest/observer"
)

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
			logCore, logs := observer.New(zap.InfoLevel)
			ctx := ctrl.LoggerInto(t.Context(), zapr.NewLogger(zap.New(logCore)))
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
				if diff := cmp.Diff(got, testCase.expected, cmpopts.IgnoreFields(metav1.ObjectMeta{}, "ResourceVersion")); diff != "" {
					t.Errorf("got unexpected object after reconcile: %v", diff)
				}
			}
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
			ignitionConfig, ignitionConfig2, ignitionConfig3, ignitionServerCACert, userDataSecret,
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

func TestSecretJanitorReconcileKeepsReferencedUserDataSecrets(t *testing.T) {
	ctx := ctrl.LoggerInto(t.Context(), zapr.NewLogger(zaptest.NewLogger(t)))
	mockCtrl := gomock.NewController(t)

	fakeClock := testingclock.NewFakeClock(time.Now())

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
			IgnitionEndpoint: "https://ignition.cluster-cluster-name.myns.devcluster.openshift.com",
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

	ignitionConfigs := []client.Object{}
	for _, name := range []string{"core-machineconfig", "core-machineconfig-2", "core-machineconfig-3"} {
		ignitionConfigs = append(ignitionConfigs, &corev1.ConfigMap{
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
		})
	}

	ignitionServerCACert := ignitionserver.IgnitionCACertSecret("myns-cluster-name")
	ignitionServerCACert.Data = map[string][]byte{
		corev1.TLSCertKey: []byte("test-ignition-ca-cert"),
	}

	mockedReleaseProvider := releaseinfo.NewMockProviderWithRegistryOverrides(mockCtrl)
	mockedReleaseProvider.EXPECT().Lookup(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(testutils.InitReleaseImageOrDie("4.18.0"), nil).AnyTimes()

	// A user data Secret whose name does not match the expected set: without
	// the reference guard the janitor deletes it synchronously.
	userDataSecret := func() *corev1.Secret {
		return &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "user-data-nodepool-name-oldhash",
				Namespace: "myns-cluster-name",
				Annotations: map[string]string{
					nodePoolAnnotation: client.ObjectKeyFromObject(nodePool).String(),
				},
			},
		}
	}

	// A Machine created outside of a rollout (e.g. by the cluster autoscaler)
	// referencing the old-hash Secret as bootstrap data.
	machine := func() *capiv1.Machine {
		return &capiv1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "nodepool-name-2hphc-abcde",
				Namespace: "myns-cluster-name",
				Annotations: map[string]string{
					nodePoolAnnotation: client.ObjectKeyFromObject(nodePool).String(),
				},
			},
			Spec: capiv1.MachineSpec{
				Bootstrap: capiv1.Bootstrap{
					DataSecretName: ptr.To("user-data-nodepool-name-oldhash"),
				},
			},
		}
	}

	// The MachineDeployment template still pointing at the old-hash Secret,
	// as it does on management-side-only config changes.
	machineDeployment := func() *capiv1.MachineDeployment {
		return &capiv1.MachineDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      nodePool.GetName(),
				Namespace: "myns-cluster-name",
				Annotations: map[string]string{
					nodePoolAnnotation: client.ObjectKeyFromObject(nodePool).String(),
				},
			},
			Spec: capiv1.MachineDeploymentSpec{
				Template: capiv1.MachineTemplateSpec{
					Spec: capiv1.MachineSpec{
						Bootstrap: capiv1.Bootstrap{
							DataSecretName: ptr.To("user-data-nodepool-name-oldhash"),
						},
					},
				},
			},
		}
	}

	// A token Secret whose name does not match the expected set: it is paired
	// with the user data Secret of the same config generation.
	tokenSecret := func() *corev1.Secret {
		return &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "token-nodepool-name-oldhash",
				Namespace: "myns-cluster-name",
				Annotations: map[string]string{
					nodePoolAnnotation: client.ObjectKeyFromObject(nodePool).String(),
				},
			},
		}
	}

	testCases := []struct {
		name          string
		objects       []client.Object
		reconcileName string
		kept          bool
		expectExpiry  bool
	}{
		{
			name:          "When the user data Secret is referenced by a Machine, it should be kept",
			objects:       []client.Object{userDataSecret(), machine()},
			reconcileName: "user-data-nodepool-name-oldhash",
			kept:          true,
		},
		{
			name:          "When the user data Secret is referenced by the MachineDeployment, it should be kept",
			objects:       []client.Object{userDataSecret(), machineDeployment()},
			reconcileName: "user-data-nodepool-name-oldhash",
			kept:          true,
		},
		{
			name:          "When the user data Secret is unreferenced, it should be deleted",
			objects:       []client.Object{userDataSecret()},
			reconcileName: "user-data-nodepool-name-oldhash",
		},
		{
			name:          "When the paired user data Secret of a token Secret is referenced by a Machine, the token Secret should be kept",
			objects:       []client.Object{tokenSecret(), machine()},
			reconcileName: "token-nodepool-name-oldhash",
			kept:          true,
		},
		{
			name:          "When the paired user data Secret of a token Secret is unreferenced, the token Secret should be annotated to expire",
			objects:       []client.Object{tokenSecret()},
			reconcileName: "token-nodepool-name-oldhash",
			kept:          true,
			expectExpiry:  true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			objects := append([]client.Object{
				nodePool,
				hostedCluster,
				pullSecret,
				machineConfig,
				ignitionServerCACert,
			}, ignitionConfigs...)
			objects = append(objects, tc.objects...)

			c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(objects...).Build()
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

			key := client.ObjectKey{Namespace: "myns-cluster-name", Name: tc.reconcileName}
			result, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			if err != nil {
				t.Fatalf("failed to reconcile: %v", err)
			}

			got := &corev1.Secret{}
			err = c.Get(ctx, key, got)
			if !tc.kept {
				if !apierrors.IsNotFound(err) {
					t.Errorf("expected unreferenced secret to be deleted, got error: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("expected secret to be kept, got error: %v", err)
			}
			_, hasExpiry := got.Annotations[hyperv1.IgnitionServerTokenExpirationTimestampAnnotation]
			if hasExpiry != tc.expectExpiry {
				t.Errorf("expected expiration annotation presence: %t, got: %t", tc.expectExpiry, hasExpiry)
			}
			if !tc.expectExpiry && result.RequeueAfter != 10*time.Minute {
				t.Errorf("expected a 10 minute requeue while the Secret is still referenced, got: %v", result.RequeueAfter)
			}
		})
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
