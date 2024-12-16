package nodepool

import (
	"context"
	"testing"
	"time"

	"github.com/blang/semver"
	"github.com/go-logr/zapr"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	. "github.com/onsi/gomega"
	"github.com/openshift/api/image/docker10"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/ignitionserver"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/releaseinfo"
	fakereleaseprovider "github.com/openshift/hypershift/support/releaseinfo/fake"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/dockerv1client"
	"github.com/openshift/hypershift/support/util/fakeimagemetadataprovider"
	"go.uber.org/zap/zaptest"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	testingclock "k8s.io/utils/clock/testing"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestSecretJanitor_Reconcile(t *testing.T) {
	ctx := ctrl.LoggerInto(context.Background(), zapr.NewLogger(zaptest.NewLogger(t)))

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
			PullSecret: hyperv1.ReloadableLocalObjectReference{Name: pullSecret.Name},
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
			name: "unrelated secret untouched",
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
			name: "related but not known secret untouched",
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
			name: "related known secret with correct hash untouched",
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
			name: "related token secret with incorrect hash set for expiry",
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
			name: "related ignition user data secret with incorrect hash deleted",
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
}

func TestShouldKeepOldUserData(t *testing.T) {
	pullSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "pull-secret", Namespace: "test"},
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: []byte("whatever"),
		},
	}

	testCases := []struct {
		name            string
		hc              *hyperv1.HostedCluster
		releaseProvider releaseinfo.Provider
		expected        bool
	}{
		{
			name: "when hosted cluster is not aws it should NOT keep old user data",
			hc: &hyperv1.HostedCluster{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Namespace: pullSecret.Namespace,
				},
				Spec: hyperv1.HostedClusterSpec{
					PullSecret: hyperv1.ReloadableLocalObjectReference{
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
			expected: false,
		},
		{
			name: "when hosted cluster is less than 4.16 it should keep user data",
			hc: &hyperv1.HostedCluster{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Namespace: pullSecret.Namespace,
				},
				Spec: hyperv1.HostedClusterSpec{
					PullSecret: hyperv1.ReloadableLocalObjectReference{
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
			releaseProvider: &fakereleaseprovider.FakeReleaseProvider{Version: semver.MustParse("4.15.0").String()},
			expected:        true,
		},
		{
			name: "when hosted cluster is equal or greater than 4.16 it should NOT keep user data",
			hc: &hyperv1.HostedCluster{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Namespace: pullSecret.Namespace,
				},
				Spec: hyperv1.HostedClusterSpec{
					PullSecret: hyperv1.ReloadableLocalObjectReference{
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
			releaseProvider: &fakereleaseprovider.FakeReleaseProvider{Version: semver.MustParse("4.16.0").String()},
			expected:        false,
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

			shouldKeepOldUserData, err := r.shouldKeepOldUserData(context.Background(), tc.hc)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(shouldKeepOldUserData).To(Equal(tc.expected))
		})
	}
}
