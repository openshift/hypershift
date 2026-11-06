package nodepool

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/ignitionserver"
	api "github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/globalconfig"
	karpenterutil "github.com/openshift/hypershift/support/karpenter"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/releaseinfo/testutils"
	"github.com/openshift/hypershift/support/testutil"
	supportutil "github.com/openshift/hypershift/support/util"

	configv1 "github.com/openshift/api/config/v1"
	imageapi "github.com/openshift/api/image/v1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	testingclock "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ignitionapi "github.com/coreos/ignition/v2/config/v3_2/types"
	"github.com/coreos/stream-metadata-go/stream"
	"github.com/go-logr/logr/testr"
	"github.com/go-logr/zapr"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestNewToken(t *testing.T) {
	hcName := "test-hc"
	hcNamespace := "namespace"
	controlplaneNamespace := "controlplane-namespace"
	pullSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pull-secret",
			Namespace: hcNamespace,
		},
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: []byte(`{"auths":{"example.com":{"auth":"dGVzdDp0ZXN0"}}}`),
		},
	}

	additionalTrustBundle := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "additional-trust-bundle",
			Namespace: hcNamespace,
		},
		Data: map[string]string{
			"ca-bundle.crt": "test-ca-bundle",
		},
	}

	ignitionServerCACert := ignitionserver.IgnitionCACertSecret(controlplaneNamespace)
	ignitionServerCACert.Data = map[string][]byte{
		corev1.TLSCertKey: []byte("test-ignition-ca-cert"),
	}

	testCases := []struct {
		name            string
		configGenerator *ConfigGenerator
		cpoCapabilities *CPOCapabilities
		fakeObjects     []crclient.Object
		expectedError   string
	}{
		{
			name: "when all input is given it should create token successfully",
			configGenerator: &ConfigGenerator{
				hostedCluster: &hyperv1.HostedCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name:      hcName,
						Namespace: hcNamespace,
					},
					Spec: hyperv1.HostedClusterSpec{
						PullSecret: corev1.LocalObjectReference{
							Name: pullSecret.GetName(),
						},
						AdditionalTrustBundle: &corev1.LocalObjectReference{
							Name: additionalTrustBundle.GetName(),
						},
						Configuration: &hyperv1.ClusterConfiguration{
							Proxy: &configv1.ProxySpec{
								HTTPProxy:  "http://proxy.example.com",
								HTTPSProxy: "https://proxy.example.com",
								NoProxy:    "example.com,10.0.0.0/8,192.168.0.0/16",
							},
						},
					},
					Status: hyperv1.HostedClusterStatus{
						IgnitionEndpoint: "https://example.com",
					},
				},
				nodePool:              &hyperv1.NodePool{},
				controlplaneNamespace: controlplaneNamespace,
				rolloutConfig:         &rolloutConfig{},
			},
			fakeObjects: []crclient.Object{
				pullSecret,
				additionalTrustBundle,
				ignitionServerCACert,
			},
			cpoCapabilities: &CPOCapabilities{},
			expectedError:   "",
		},
		{
			name: "When missing ignition endpoint it should fail",
			configGenerator: &ConfigGenerator{
				hostedCluster: &hyperv1.HostedCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name:      hcName,
						Namespace: hcNamespace,
					},
					Spec: hyperv1.HostedClusterSpec{
						PullSecret: corev1.LocalObjectReference{
							Name: pullSecret.GetName(),
						},
						AdditionalTrustBundle: &corev1.LocalObjectReference{
							Name: additionalTrustBundle.GetName(),
						},
					},
					Status: hyperv1.HostedClusterStatus{
						IgnitionEndpoint: "",
					},
				},
				nodePool:              &hyperv1.NodePool{},
				controlplaneNamespace: controlplaneNamespace,
				rolloutConfig:         &rolloutConfig{},
			},
			fakeObjects: []crclient.Object{
				pullSecret,
				additionalTrustBundle,
				ignitionServerCACert,
			},
			cpoCapabilities: &CPOCapabilities{},
			expectedError:   "ignition endpoint is not set",
		},
		{
			name: "When missing pullsecret it should fail",
			configGenerator: &ConfigGenerator{
				hostedCluster: &hyperv1.HostedCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name:      hcName,
						Namespace: hcNamespace,
					},
					Spec: hyperv1.HostedClusterSpec{
						PullSecret: corev1.LocalObjectReference{
							Name: pullSecret.GetName(),
						},
						AdditionalTrustBundle: &corev1.LocalObjectReference{
							Name: additionalTrustBundle.GetName(),
						},
					},
					Status: hyperv1.HostedClusterStatus{
						IgnitionEndpoint: "https://example.com",
					},
				},
				nodePool:              &hyperv1.NodePool{},
				controlplaneNamespace: controlplaneNamespace,
				rolloutConfig:         &rolloutConfig{},
			},
			fakeObjects: []crclient.Object{
				additionalTrustBundle,
				ignitionServerCACert,
			},
			cpoCapabilities: &CPOCapabilities{},
			expectedError:   "cannot get pull secret namespace/pull-secret: secrets \"pull-secret\" not found",
		},
		{
			name: "When missing additionalTrustBundle it should fail",
			configGenerator: &ConfigGenerator{
				hostedCluster: &hyperv1.HostedCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name:      hcName,
						Namespace: hcNamespace,
					},
					Spec: hyperv1.HostedClusterSpec{
						PullSecret: corev1.LocalObjectReference{
							Name: pullSecret.GetName(),
						},
						AdditionalTrustBundle: &corev1.LocalObjectReference{
							Name: additionalTrustBundle.GetName(),
						},
					},
					Status: hyperv1.HostedClusterStatus{
						IgnitionEndpoint: "https://example.com",
					},
				},
				nodePool:              &hyperv1.NodePool{},
				controlplaneNamespace: controlplaneNamespace,
				rolloutConfig:         &rolloutConfig{},
			},
			fakeObjects: []crclient.Object{
				pullSecret,
				ignitionServerCACert,
			},
			cpoCapabilities: &CPOCapabilities{},
			expectedError:   "cannot get additionalTrustBundle namespace/additional-trust-bundle: configmaps \"additional-trust-bundle\"",
		},
		{
			name: "When missing ignitionServerCACert it should fail",
			configGenerator: &ConfigGenerator{
				hostedCluster: &hyperv1.HostedCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name:      hcName,
						Namespace: hcNamespace,
					},
					Spec: hyperv1.HostedClusterSpec{
						PullSecret: corev1.LocalObjectReference{
							Name: pullSecret.GetName(),
						},
						AdditionalTrustBundle: &corev1.LocalObjectReference{
							Name: additionalTrustBundle.GetName(),
						},
					},
					Status: hyperv1.HostedClusterStatus{
						IgnitionEndpoint: "https://example.com",
					},
				},
				nodePool:              &hyperv1.NodePool{},
				controlplaneNamespace: controlplaneNamespace,
				rolloutConfig:         &rolloutConfig{},
			},
			fakeObjects: []crclient.Object{
				pullSecret,
				additionalTrustBundle,
			},
			cpoCapabilities: &CPOCapabilities{},
			expectedError:   "secrets \"ignition-server-ca-cert\" not found",
		},
		{
			name:            "When missing configGenerator it should fail",
			configGenerator: nil,
			fakeObjects: []crclient.Object{
				pullSecret,
				additionalTrustBundle,
				ignitionServerCACert,
			},
			cpoCapabilities: &CPOCapabilities{},
			expectedError:   "configGenerator can't be nil",
		},
		{
			name: "When missing capabilities it should fail",
			configGenerator: &ConfigGenerator{
				hostedCluster: &hyperv1.HostedCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name:      hcName,
						Namespace: hcNamespace,
					},
					Spec: hyperv1.HostedClusterSpec{
						PullSecret: corev1.LocalObjectReference{
							Name: pullSecret.GetName(),
						},
						AdditionalTrustBundle: &corev1.LocalObjectReference{
							Name: additionalTrustBundle.GetName(),
						},
					},
					Status: hyperv1.HostedClusterStatus{
						IgnitionEndpoint: "https://example.com",
					},
				},
				nodePool:              &hyperv1.NodePool{},
				controlplaneNamespace: controlplaneNamespace,
				rolloutConfig:         &rolloutConfig{},
			},
			fakeObjects: []crclient.Object{
				pullSecret,
				additionalTrustBundle,
				ignitionServerCACert,
			},
			cpoCapabilities: nil,
			expectedError:   "cpoCapabilities can't be nil",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(tc.fakeObjects...).Build()
			if tc.configGenerator != nil {
				tc.configGenerator.Client = fakeClient
			}

			token, err := NewToken(t.Context(), tc.configGenerator, tc.cpoCapabilities)
			if tc.expectedError != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tc.expectedError))
				return
			}
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(token).NotTo(BeNil())

			// Validate expected hashes against raw strings to guarantee expected output.
			expectedPullSecretHash := []byte(supportutil.HashSimple([]byte(`{"auths":{"example.com":{"auth":"dGVzdDp0ZXN0"}}}`)))
			expectedAdditionalTrustBundleHash := []byte(supportutil.HashSimple("test-ca-bundle"))
			expectedGlobalConfig, err := supportutil.HashStruct(tc.configGenerator.hostedCluster.Spec.Configuration)
			g.Expect(err).To(Not(HaveOccurred()))

			g.Expect(token.pullSecretHash).To(Equal(expectedPullSecretHash))
			g.Expect(token.additionalTrustBundleHash).To(Equal(expectedAdditionalTrustBundleHash))
			g.Expect(token.globalConfigHash).To(Equal([]byte(expectedGlobalConfig)))

			// Validate user data.
			g.Expect(token.userData.caCert).To(Equal([]byte("test-ignition-ca-cert")))
			g.Expect(token.userData.ignitionServerEndpoint).To(Equal("https://example.com"))
			expectedProxy := globalconfig.ProxyConfig()
			globalconfig.ReconcileProxyConfigWithStatusFromHostedCluster(expectedProxy, token.hostedCluster)
			g.Expect(token.userData.proxy.Status).To(Equal(configv1.ProxyStatus{
				HTTPProxy:  "http://proxy.example.com",
				HTTPSProxy: "https://proxy.example.com",
				NoProxy:    ".cluster.local,.local,.svc,10.0.0.0/8,127.0.0.1,192.168.0.0/16,example.com,localhost",
			}))
		})
	}
}

func TestTokenCleanupOutdated(t *testing.T) {
	controlplaneNamespace := "test-namespace"
	nodePoolName := "test-nodepool"
	outdatedHash := "outdated-hash"
	userdataSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlplaneNamespace,
			Name:      fmt.Sprintf("%s-%s-%s", UserDataSecrePrefix, nodePoolName, outdatedHash),
		},
	}

	tokenSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlplaneNamespace,
			Name:      fmt.Sprintf("%s-%s-%s", TokenSecretPrefix, nodePoolName, outdatedHash),
		},
	}

	tokenSecretWithTimestamp := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlplaneNamespace,
			Name:      fmt.Sprintf("%s-%s-%s", TokenSecretPrefix, nodePoolName, outdatedHash),
			Annotations: map[string]string{
				hyperv1.IgnitionServerTokenExpirationTimestampAnnotation: time.Now().Add(2 * time.Hour).Format(time.RFC3339),
			},
		},
	}

	testCases := []struct {
		name            string
		token           *Token
		fakeObjects     []crclient.Object
		expectedError   string
		expectDeleteLog bool
	}{
		{
			name: "When userdata and token secret are outdated, it should delete userdata secret and add expiration timestamp to token secret",
			token: &Token{
				ConfigGenerator: &ConfigGenerator{
					nodePool: &hyperv1.NodePool{
						ObjectMeta: metav1.ObjectMeta{
							Name:      nodePoolName,
							Namespace: "nodepool-namespace",
							Annotations: map[string]string{
								nodePoolAnnotationCurrentConfigVersion: outdatedHash,
							},
						},
						Spec: hyperv1.NodePoolSpec{
							Platform: hyperv1.NodePoolPlatform{
								Type: hyperv1.AzurePlatform,
							},
						},
					},
					controlplaneNamespace: controlplaneNamespace,
					rolloutConfig:         &rolloutConfig{},
				},
			},
			fakeObjects: []crclient.Object{
				userdataSecret,
				tokenSecret,
			},
			expectedError:   "",
			expectDeleteLog: true,
		},
		{
			name: "When none of the secrests exists it should succeed",
			token: &Token{
				ConfigGenerator: &ConfigGenerator{
					nodePool: &hyperv1.NodePool{
						ObjectMeta: metav1.ObjectMeta{
							Name: nodePoolName,
							Annotations: map[string]string{
								nodePoolAnnotationCurrentConfigVersion: outdatedHash,
							},
						},
						Spec: hyperv1.NodePoolSpec{
							Platform: hyperv1.NodePoolPlatform{
								Type: hyperv1.AzurePlatform,
							},
						},
					},
					controlplaneNamespace: controlplaneNamespace,
					rolloutConfig:         &rolloutConfig{},
				},
			},
			fakeObjects:   []crclient.Object{},
			expectedError: "",
		},
		{
			name: "When token secret exists, but already has an expiration timestamp annotation, it should succeed",
			token: &Token{
				ConfigGenerator: &ConfigGenerator{
					nodePool: &hyperv1.NodePool{
						ObjectMeta: metav1.ObjectMeta{
							Name: nodePoolName,
							Annotations: map[string]string{
								nodePoolAnnotationCurrentConfigVersion: outdatedHash,
							},
						},
						Spec: hyperv1.NodePoolSpec{
							Platform: hyperv1.NodePoolPlatform{
								Type: hyperv1.AzurePlatform,
							},
						},
					},
					controlplaneNamespace: controlplaneNamespace,
					rolloutConfig:         &rolloutConfig{},
				},
			},
			fakeObjects: []crclient.Object{
				tokenSecretWithTimestamp,
			},
			expectedError: "",
		},
		{
			name: "When platform is KubeVirt, it should preserve outdated userdata secret and add expiration timestamp to token secret",
			token: &Token{
				ConfigGenerator: &ConfigGenerator{
					nodePool: &hyperv1.NodePool{
						ObjectMeta: metav1.ObjectMeta{
							Name: nodePoolName,
							Annotations: map[string]string{
								nodePoolAnnotationCurrentConfigVersion: outdatedHash,
							},
						},
						Spec: hyperv1.NodePoolSpec{
							Platform: hyperv1.NodePoolPlatform{
								Type: hyperv1.KubevirtPlatform,
							},
						},
					},
					controlplaneNamespace: controlplaneNamespace,
				},
			},
			fakeObjects: []crclient.Object{
				userdataSecret.DeepCopy(),
				tokenSecret.DeepCopy(),
			},
			expectedError: "",
		},
		{
			name: "When platform is AWS, it should preserve outdated userdata secret and add expiration timestamp to token secret",
			token: &Token{
				ConfigGenerator: &ConfigGenerator{
					nodePool: &hyperv1.NodePool{
						ObjectMeta: metav1.ObjectMeta{
							Name: nodePoolName,
							Annotations: map[string]string{
								nodePoolAnnotationCurrentConfigVersion: outdatedHash,
							},
						},
						Spec: hyperv1.NodePoolSpec{
							Platform: hyperv1.NodePoolPlatform{
								Type: hyperv1.AWSPlatform,
							},
						},
					},
					controlplaneNamespace: controlplaneNamespace,
				},
			},
			fakeObjects: []crclient.Object{
				userdataSecret.DeepCopy(),
				tokenSecret.DeepCopy(),
			},
			expectedError: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(tc.fakeObjects...).Build()
			tc.token.Client = fakeClient

			logCore, logs := observer.New(zap.InfoLevel)
			ctx := ctrl.LoggerInto(t.Context(), zapr.NewLogger(zap.New(logCore)))
			err := tc.token.cleanupOutdated(ctx)
			deletedLogs := logs.FilterMessage("Deleted outdated Secret").All()
			if tc.expectDeleteLog {
				g.Expect(deletedLogs).To(HaveLen(1))
				g.Expect(deletedLogs[0].ContextMap()).To(HaveKeyWithValue("secret", "test-namespace/user-data-test-nodepool-outdated-hash"))
				g.Expect(deletedLogs[0].ContextMap()).To(HaveKeyWithValue("nodePool", "nodepool-namespace/test-nodepool"))
			} else {
				g.Expect(deletedLogs).To(BeEmpty())
			}
			if tc.expectedError != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tc.expectedError))
				return
			}
			g.Expect(err).NotTo(HaveOccurred())

			got := &corev1.Secret{}
			err = fakeClient.Get(t.Context(), crclient.ObjectKeyFromObject(userdataSecret), got)
			platformType := tc.token.nodePool.Spec.Platform.Type
			if platformType == hyperv1.AWSPlatform || platformType == hyperv1.KubevirtPlatform {
				g.Expect(err).ToNot(HaveOccurred(), "userdata secret should be preserved for %s platform", platformType)
			} else {
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "userdata secret should be deleted for %s platform", platformType)
			}

			got = &corev1.Secret{}
			err = fakeClient.Get(t.Context(), crclient.ObjectKeyFromObject(tokenSecret), got)
			if err != nil {
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
				return
			}
			g.Expect(got.Annotations).To(HaveKey(hyperv1.IgnitionServerTokenExpirationTimestampAnnotation))
		})
	}

	t.Run("Deletion reference guard", func(t *testing.T) {
		controlplaneNamespace := "test-namespace"
		nodePoolName := "test-nodepool"
		outdatedHash := "outdated-hash"
		outdatedUserDataName := fmt.Sprintf("%s-%s-%s", UserDataSecrePrefix, nodePoolName, outdatedHash)
		outdatedTokenName := fmt.Sprintf("%s-%s-%s", TokenSecretPrefix, nodePoolName, outdatedHash)

		userDataSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: controlplaneNamespace,
				Name:      outdatedUserDataName,
			},
		}
		tokenSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: controlplaneNamespace,
				Name:      outdatedTokenName,
			},
		}

		nodePool := func(platformType hyperv1.PlatformType) *hyperv1.NodePool {
			return &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      nodePoolName,
					Namespace: controlplaneNamespace,
					Annotations: map[string]string{
						nodePoolAnnotationCurrentConfigVersion: outdatedHash,
					},
				},
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: platformType,
					},
				},
			}
		}

		t.Run("When nothing references the outdated user data Secret and platform is Azure, it should be deleted", func(t *testing.T) {
			g := NewWithT(t)
			fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(userDataSecret.DeepCopy(), tokenSecret.DeepCopy()).Build()
			token := &Token{
				ConfigGenerator: &ConfigGenerator{
					Client:                fakeClient,
					nodePool:              nodePool(hyperv1.AzurePlatform),
					controlplaneNamespace: controlplaneNamespace,
					rolloutConfig:         &rolloutConfig{},
				},
			}
			g.Expect(token.cleanupOutdated(t.Context())).To(Succeed(), "cleanup of unreferenced outdated Secrets should succeed")
			got := &corev1.Secret{}
			err := fakeClient.Get(t.Context(), crclient.ObjectKey{Namespace: controlplaneNamespace, Name: outdatedUserDataName}, got)
			g.Expect(err).To(HaveOccurred(), "unreferenced outdated user data Secret should be gone")
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "expected NotFound for deleted unreferenced user data Secret, got: %v", err)
			// The unreferenced token Secret does get its expiration timestamp.
			gotToken := &corev1.Secret{}
			g.Expect(fakeClient.Get(t.Context(), crclient.ObjectKey{Namespace: controlplaneNamespace, Name: outdatedTokenName}, gotToken)).To(Succeed(), "unreferenced token Secret should still exist, only annotated")
			g.Expect(gotToken.Annotations).To(HaveKey(hyperv1.IgnitionServerTokenExpirationTimestampAnnotation), "unreferenced token Secret should be annotated to expire")
		})

		t.Run("When a Machine references the outdated user data Secret, it should be kept even on Azure", func(t *testing.T) {
			g := NewWithT(t)
			machine := &capiv1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-machine",
					Namespace: controlplaneNamespace,
					Annotations: map[string]string{
						nodePoolAnnotation: fmt.Sprintf("%s/%s", controlplaneNamespace, nodePoolName),
					},
				},
				Spec: capiv1.MachineSpec{
					Bootstrap: capiv1.Bootstrap{
						DataSecretName: &outdatedUserDataName,
					},
				},
			}
			fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(userDataSecret.DeepCopy(), tokenSecret.DeepCopy(), machine).Build()
			token := &Token{
				ConfigGenerator: &ConfigGenerator{
					Client:                fakeClient,
					nodePool:              nodePool(hyperv1.AzurePlatform),
					controlplaneNamespace: controlplaneNamespace,
					rolloutConfig:         &rolloutConfig{},
				},
			}
			g.Expect(token.cleanupOutdated(t.Context())).To(Succeed(), "cleanup should succeed when the outdated Secret is referenced")
			got := &corev1.Secret{}
			g.Expect(fakeClient.Get(t.Context(), crclient.ObjectKey{Namespace: controlplaneNamespace, Name: outdatedUserDataName}, got)).To(Succeed(), "Machine-referenced outdated user data Secret should be kept")
			g.Expect(got.Name).To(Equal(outdatedUserDataName), "kept Secret should be the referenced one")
			// The paired token Secret must not be expired either: Machines that
			// have not booted yet still present its token to fetch ignition.
			gotToken := &corev1.Secret{}
			g.Expect(fakeClient.Get(t.Context(), crclient.ObjectKey{Namespace: controlplaneNamespace, Name: outdatedTokenName}, gotToken)).To(Succeed(), "paired token Secret should be kept")
			g.Expect(gotToken.Annotations).ToNot(HaveKey(hyperv1.IgnitionServerTokenExpirationTimestampAnnotation), "token Secret paired with a referenced user data Secret must not be expired")
		})

		t.Run("When a referenced token Secret was already annotated to expire, it should have the annotation cleared", func(t *testing.T) {
			g := NewWithT(t)
			machine := &capiv1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-machine",
					Namespace: controlplaneNamespace,
					Annotations: map[string]string{
						nodePoolAnnotation: fmt.Sprintf("%s/%s", controlplaneNamespace, nodePoolName),
					},
				},
				Spec: capiv1.MachineSpec{
					Bootstrap: capiv1.Bootstrap{
						DataSecretName: &outdatedUserDataName,
					},
				},
			}
			// An earlier pass expired the token while nothing referenced the pair yet.
			expiringToken := tokenSecret.DeepCopy()
			expiringToken.Annotations = map[string]string{
				hyperv1.IgnitionServerTokenExpirationTimestampAnnotation: time.Now().Add(time.Hour).Format(time.RFC3339),
			}
			fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(userDataSecret.DeepCopy(), expiringToken, machine).Build()
			token := &Token{
				ConfigGenerator: &ConfigGenerator{
					Client:                fakeClient,
					nodePool:              nodePool(hyperv1.AzurePlatform),
					controlplaneNamespace: controlplaneNamespace,
					rolloutConfig:         &rolloutConfig{},
				},
			}
			g.Expect(token.cleanupOutdated(t.Context())).To(Succeed(), "cleanup should succeed for a referenced but previously expired token")
			gotToken := &corev1.Secret{}
			g.Expect(fakeClient.Get(t.Context(), crclient.ObjectKey{Namespace: controlplaneNamespace, Name: outdatedTokenName}, gotToken)).To(Succeed(), "referenced token Secret should be kept")
			g.Expect(gotToken.Annotations).ToNot(HaveKey(hyperv1.IgnitionServerTokenExpirationTimestampAnnotation),
				"a stale expiry must be cleared so the ignition server's token controller does not delete a still-referenced token")
		})

		t.Run("When the MachineDeployment references the outdated user data Secret, it should be kept", func(t *testing.T) {
			g := NewWithT(t)
			machineDeployment := &capiv1.MachineDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      nodePoolName,
					Namespace: controlplaneNamespace,
				},
				Spec: capiv1.MachineDeploymentSpec{
					Template: capiv1.MachineTemplateSpec{
						Spec: capiv1.MachineSpec{
							Bootstrap: capiv1.Bootstrap{
								DataSecretName: &outdatedUserDataName,
							},
						},
					},
				},
			}
			fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(userDataSecret.DeepCopy(), tokenSecret.DeepCopy(), machineDeployment).Build()
			token := &Token{
				ConfigGenerator: &ConfigGenerator{
					Client:                fakeClient,
					nodePool:              nodePool(hyperv1.AzurePlatform),
					controlplaneNamespace: controlplaneNamespace,
					rolloutConfig:         &rolloutConfig{},
				},
			}
			g.Expect(token.cleanupOutdated(t.Context())).To(Succeed(), "cleanup should succeed when the outdated Secret is referenced")
			got := &corev1.Secret{}
			g.Expect(fakeClient.Get(t.Context(), crclient.ObjectKey{Namespace: controlplaneNamespace, Name: outdatedUserDataName}, got)).To(Succeed(), "MachineDeployment-referenced outdated user data Secret should be kept")
		})

		t.Run("When a rollout MachineSet with no Machines references the outdated user data Secret, it should be kept", func(t *testing.T) {
			g := NewWithT(t)
			// MachineDeployments generate rollout MachineSets named
			// <np>-<hash>-<suffix> that carry the NodePool annotation; one that has
			// scaled down to zero Machines must still keep its referenced Secret
			// alive, since Machines created from it afterwards would never
			// provision without it.
			rolloutMachineSet := &capiv1.MachineSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("%s-rollouthash-abcde", nodePoolName),
					Namespace: controlplaneNamespace,
					Annotations: map[string]string{
						nodePoolAnnotation: fmt.Sprintf("%s/%s", controlplaneNamespace, nodePoolName),
					},
				},
				Spec: capiv1.MachineSetSpec{
					Template: capiv1.MachineTemplateSpec{
						Spec: capiv1.MachineSpec{
							Bootstrap: capiv1.Bootstrap{
								DataSecretName: &outdatedUserDataName,
							},
						},
					},
				},
			}
			fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(userDataSecret.DeepCopy(), tokenSecret.DeepCopy(), rolloutMachineSet).Build()
			token := &Token{
				ConfigGenerator: &ConfigGenerator{
					Client:                fakeClient,
					nodePool:              nodePool(hyperv1.AzurePlatform),
					controlplaneNamespace: controlplaneNamespace,
					rolloutConfig:         &rolloutConfig{},
				},
			}
			g.Expect(token.cleanupOutdated(t.Context())).To(Succeed(), "cleanup should succeed when the outdated Secret is referenced by a rollout MachineSet")
			got := &corev1.Secret{}
			g.Expect(fakeClient.Get(t.Context(), crclient.ObjectKey{Namespace: controlplaneNamespace, Name: outdatedUserDataName}, got)).To(Succeed(), "rollout MachineSet-referenced outdated user data Secret should be kept")
		})
	})
}

func TestSetExpirationTimestampOnToken(t *testing.T) {
	theTime, err := time.Parse(time.RFC3339Nano, "2006-01-02T15:04:05.999999999Z")
	if err != nil {
		t.Fatalf("could not parse time: %v", err)
	}
	fakeClock := testingclock.NewFakeClock(theTime)

	fakeName := "test-token"
	fakeNamespace := "master-cluster1"
	fakeCurrentTokenVal := "tokenval1"

	testCases := []struct {
		name              string
		inputSecret       *corev1.Secret
		expectedTimestamp string
	}{
		{
			name: "when set expiration timestamp on token is called on a secret then the expiration timestamp is set",
			inputSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fakeName,
					Namespace: fakeNamespace,
				},
				Data: map[string][]byte{
					TokenSecretTokenKey: []byte(fakeCurrentTokenVal),
				},
			},
			expectedTimestamp: theTime.Add(2 * time.Hour).Format(time.RFC3339),
		},
		{
			name: "when set expiration timestamp on token is called on a secret that already has an expiration timestamp, timestamp is not reset",
			inputSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fakeName,
					Namespace: fakeNamespace,
					Annotations: map[string]string{
						hyperv1.IgnitionServerTokenExpirationTimestampAnnotation: theTime.Add(1 * time.Hour).Format(time.RFC3339),
					},
				},
				Data: map[string][]byte{
					TokenSecretTokenKey: []byte(fakeCurrentTokenVal),
				},
			},
			expectedTimestamp: theTime.Add(1 * time.Hour).Format(time.RFC3339),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			c := fake.NewClientBuilder().WithObjects(tc.inputSecret).Build()
			err := setExpirationTimestampOnToken(t.Context(), c, tc.inputSecret, fakeClock.Now)
			g.Expect(err).To(Not(HaveOccurred()))
			actualSecretData := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fakeName,
					Namespace: fakeNamespace,
				},
			}
			err = c.Get(t.Context(), crclient.ObjectKeyFromObject(actualSecretData), actualSecretData)
			g.Expect(err).To(Not(HaveOccurred()))
			g.Expect(actualSecretData.Annotations).To(testutil.MatchExpected(map[string]string{
				hyperv1.IgnitionServerTokenExpirationTimestampAnnotation: tc.expectedTimestamp,
			}))
		})
	}
}

func TestTokenReconcile(t *testing.T) {
	hcName := "test-hc"
	hcNamespace := "namespace"
	controlplaneNamespace := "controlplane-namespace"
	pullSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pull-secret",
			Namespace: hcNamespace,
		},
		Data: map[string][]byte{
			".dockerconfigjson": []byte(`{"auths":{"example.com":{"auth":"dGVzdDp0ZXN0"}}}`),
		},
	}

	additionalTrustBundle := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "additional-trust-bundle",
			Namespace: hcNamespace,
		},
		Data: map[string]string{
			"ca-bundle.crt": "test-ca-bundle",
		},
	}

	ignitionServerCACert := ignitionserver.IgnitionCACertSecret(controlplaneNamespace)
	ignitionServerCACert.Data = map[string][]byte{
		corev1.TLSCertKey: []byte("test-ignition-ca-cert"),
	}

	expectedProxyConfig := &configv1.Proxy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "proxy",
			Namespace: hcNamespace,
		},
		Spec: configv1.ProxySpec{
			HTTPProxy:  "http://proxy.example.com",
			HTTPSProxy: "https://proxy.example.com",
		},
	}

	testCases := []struct {
		name            string
		configGenerator *ConfigGenerator
		cpoCapabilities *CPOCapabilities
		fakeObjects     []crclient.Object
	}{
		{
			name: "when all input is given it should create the token and user data secrets successfully",
			configGenerator: &ConfigGenerator{
				hostedCluster: &hyperv1.HostedCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name:      hcName,
						Namespace: hcNamespace,
					},
					Spec: hyperv1.HostedClusterSpec{
						PullSecret: corev1.LocalObjectReference{
							Name: pullSecret.GetName(),
						},
						AdditionalTrustBundle: &corev1.LocalObjectReference{
							Name: additionalTrustBundle.GetName(),
						},
						Configuration: &hyperv1.ClusterConfiguration{
							Proxy: &expectedProxyConfig.Spec,
						},
					},
					Status: hyperv1.HostedClusterStatus{
						IgnitionEndpoint: "https://example.com",
					},
				},
				nodePool: &hyperv1.NodePool{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "name",
						Namespace: "namespace",
					},
					Spec: hyperv1.NodePoolSpec{
						Management: hyperv1.NodePoolManagement{
							UpgradeType: hyperv1.UpgradeTypeReplace,
						},
						Release: hyperv1.Release{
							Image: "image:4.17",
						},
					},
				},
				controlplaneNamespace: controlplaneNamespace,
				rolloutConfig: &rolloutConfig{
					releaseImage: &releaseinfo.ReleaseImage{
						ImageStream: &imageapi.ImageStream{
							ObjectMeta: metav1.ObjectMeta{
								Name: "4.17",
							},
						},
					},
					globalConfig: "test-global-config",
					mcoRawConfig: "raw-config",
				},
			},
			fakeObjects: []crclient.Object{
				pullSecret,
				additionalTrustBundle,
				ignitionServerCACert,
			},
			cpoCapabilities: &CPOCapabilities{
				DecompressAndDecodeConfig: true,
			},
		},
		{
			name: "when HostedCluster is restored from backup it should set ignition-reached annotation on the token secret",
			configGenerator: &ConfigGenerator{
				hostedCluster: &hyperv1.HostedCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name:      hcName,
						Namespace: hcNamespace,
						Annotations: map[string]string{
							hyperv1.HostedClusterRestoredFromBackupAnnotation: "",
						},
					},
					Spec: hyperv1.HostedClusterSpec{
						PullSecret: corev1.LocalObjectReference{
							Name: pullSecret.GetName(),
						},
						AdditionalTrustBundle: &corev1.LocalObjectReference{
							Name: additionalTrustBundle.GetName(),
						},
						Configuration: &hyperv1.ClusterConfiguration{
							Proxy: &expectedProxyConfig.Spec,
						},
					},
					Status: hyperv1.HostedClusterStatus{
						IgnitionEndpoint: "https://example.com",
					},
				},
				nodePool: &hyperv1.NodePool{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "name",
						Namespace: "namespace",
					},
					Spec: hyperv1.NodePoolSpec{
						Management: hyperv1.NodePoolManagement{
							UpgradeType: hyperv1.UpgradeTypeReplace,
						},
						Release: hyperv1.Release{
							Image: "image:4.17",
						},
					},
				},
				controlplaneNamespace: controlplaneNamespace,
				rolloutConfig: &rolloutConfig{
					releaseImage: &releaseinfo.ReleaseImage{
						ImageStream: &imageapi.ImageStream{
							ObjectMeta: metav1.ObjectMeta{
								Name: "4.17",
							},
						},
					},
					globalConfig: "test-global-config",
					mcoRawConfig: "raw-config",
				},
			},
			fakeObjects: []crclient.Object{
				pullSecret,
				additionalTrustBundle,
				ignitionServerCACert,
			},
			cpoCapabilities: &CPOCapabilities{
				DecompressAndDecodeConfig: true,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(tc.fakeObjects...).Build()
			tc.configGenerator.Client = fakeClient

			token, err := NewToken(t.Context(), tc.configGenerator, tc.cpoCapabilities)
			g.Expect(err).To(Not(HaveOccurred()))

			err = token.Reconcile(t.Context())
			g.Expect(err).ToNot(HaveOccurred())

			gotTokenSecret := &corev1.Secret{}
			err = fakeClient.Get(t.Context(), crclient.ObjectKeyFromObject(token.TokenSecret()), gotTokenSecret)
			g.Expect(err).ToNot(HaveOccurred())

			// Validate the token secret has all the expected annotations.
			g.Expect(gotTokenSecret.Annotations[TokenSecretAnnotation]).To(Equal("true"))
			g.Expect(gotTokenSecret.Annotations[TokenSecretNodePoolUpgradeType]).To(Equal(string(hyperv1.UpgradeTypeReplace)))
			g.Expect(gotTokenSecret.Annotations[nodePoolAnnotation]).To(Equal(crclient.ObjectKeyFromObject(tc.configGenerator.nodePool).String()))
			g.Expect(gotTokenSecret.Annotations[nodePoolAnnotation]).To(Not(BeEmpty()))

			// Active token should never be marked as expired.
			g.Expect(gotTokenSecret.Annotations).ToNot(HaveKey(hyperv1.IgnitionServerTokenExpirationTimestampAnnotation))

			// When the HostedCluster was restored from backup, the ignition-reached
			// annotation should be set so ReachedIgnitionEndpoint stays True.
			if _, restored := tc.configGenerator.hostedCluster.Annotations[hyperv1.HostedClusterRestoredFromBackupAnnotation]; restored {
				g.Expect(gotTokenSecret.Annotations[TokenSecretIgnitionReachedAnnotation]).To(Equal("True"))
			} else {
				g.Expect(gotTokenSecret.Annotations).ToNot(HaveKey(TokenSecretIgnitionReachedAnnotation))
			}

			// Generation time should be from ~now.
			generationTime, err := time.Parse(time.RFC3339Nano, gotTokenSecret.Annotations[TokenSecretTokenGenerationTime])
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(generationTime).To(BeTemporally("~", time.Now(), 5*time.Minute))

			// A valid UUID token is given.
			UUIDToken, err := uuid.Parse(string(gotTokenSecret.Data[TokenSecretTokenKey]))
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(UUIDToken).To(BeAssignableToTypeOf(uuid.UUID{}))
			g.Expect(gotTokenSecret.Data[TokenSecretReleaseKey]).To(Equal([]byte(tc.configGenerator.nodePool.Spec.Release.Image)))
			g.Expect(gotTokenSecret.Data[TokenSecretReleaseKey]).ToNot(BeEmpty())
			g.Expect(gotTokenSecret.Data[TokenSecretReleaseVersionKey]).To(Equal([]byte(tc.configGenerator.releaseImage.Version())))
			g.Expect(gotTokenSecret.Data[TokenSecretReleaseVersionKey]).ToNot(BeEmpty())

			// Validate the config is compressed and encoded in the token secret.
			compressedAndEncodedConfig := gotTokenSecret.Data[TokenSecretConfigKey]
			decodedAndDecompressed, err := supportutil.DecodeAndDecompress(compressedAndEncodedConfig)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(decodedAndDecompressed.String()).To(Equal("raw-config"))

			// Validate hashes are set.
			expectedPullSecretHash := []byte(supportutil.HashSimple([]byte(`{"auths":{"example.com":{"auth":"dGVzdDp0ZXN0"}}}`)))
			expectedAdditionalTrustBundleHash := []byte(supportutil.HashSimple("test-ca-bundle"))
			expectedGlobalConfig, err := supportutil.HashStruct(tc.configGenerator.hostedCluster.Spec.Configuration)
			g.Expect(err).ToNot(HaveOccurred())

			g.Expect(gotTokenSecret.Data[TokenSecretPullSecretHashKey]).To(Equal(expectedPullSecretHash))
			g.Expect(gotTokenSecret.Data[TokenSecretAdditionalTrustBundleKey]).To(Equal(expectedAdditionalTrustBundleHash))
			g.Expect(gotTokenSecret.Data[TokenSecretHCConfigurationHashKey]).To(Equal([]byte(expectedGlobalConfig)))

			// Validate the os-stream key is set to the resolved RHEL stream.
			g.Expect(gotTokenSecret.Data[TokenSecretOSStreamKey]).To(Equal([]byte(tc.configGenerator.resolvedRHELStreamForBootImage)))

			// Validate the user data secret has all the expected annotations.
			// Start Generation Here
			gotUserDataSecret := &corev1.Secret{}
			err = fakeClient.Get(t.Context(), crclient.ObjectKeyFromObject(token.UserDataSecret()), gotUserDataSecret)
			g.Expect(err).ToNot(HaveOccurred())

			// Validate the user data secret has all the expected annotations.
			g.Expect(gotUserDataSecret.Annotations[nodePoolAnnotation]).To(Equal(crclient.ObjectKeyFromObject(tc.configGenerator.nodePool).String()))
			g.Expect(gotUserDataSecret.Annotations[nodePoolAnnotation]).To(Not(BeEmpty()))

			encodedCACert := base64.StdEncoding.EncodeToString([]byte("test-ignition-ca-cert"))
			encodedToken := base64.StdEncoding.EncodeToString([]byte(gotTokenSecret.Data[TokenSecretTokenKey]))

			expectedProxy := globalconfig.ProxyConfig()
			globalconfig.ReconcileProxyConfigWithStatusFromHostedCluster(expectedProxy, tc.configGenerator.hostedCluster)
			expectedIgnition := ignitionapi.Config{
				Ignition: ignitionapi.Ignition{
					Version: "3.2.0",
					Security: ignitionapi.Security{
						TLS: ignitionapi.TLS{
							CertificateAuthorities: []ignitionapi.Resource{
								{
									Source: ptr.To(fmt.Sprintf("data:text/plain;base64,%s", encodedCACert)),
								},
							},
						},
					},
					Config: ignitionapi.IgnitionConfig{
						Merge: []ignitionapi.Resource{
							{
								Source: ptr.To(fmt.Sprintf("https://%s/ignition", tc.configGenerator.hostedCluster.Status.IgnitionEndpoint)),
								HTTPHeaders: []ignitionapi.HTTPHeader{
									{
										Name:  "Authorization",
										Value: ptr.To(fmt.Sprintf("Bearer %s", encodedToken)),
									},
									{
										Name:  "NodePool",
										Value: ptr.To(crclient.ObjectKeyFromObject(tc.configGenerator.nodePool).String()),
									},
									{
										Name:  "TargetConfigVersionHash",
										Value: ptr.To(token.Hash()),
									},
								},
							},
						},
					},
					Proxy: ignitionapi.Proxy{
						HTTPProxy:  ptr.To(expectedProxyConfig.Spec.HTTPProxy),
						HTTPSProxy: ptr.To(expectedProxyConfig.Spec.HTTPSProxy),
						NoProxy: []ignitionapi.NoProxyItem{
							".cluster.local", ".local", ".svc", "127.0.0.1", "localhost",
						},
					},
				},
			}

			// Validate the userdata[value] returns the expected ignition config
			var gotIgnition ignitionapi.Config
			err = json.Unmarshal(gotUserDataSecret.Data["value"], &gotIgnition)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(gotIgnition).To(Equal(expectedIgnition))
		})

	}
}

func TestTokenUserDataSecret(t *testing.T) {
	testCases := []struct {
		name                     string
		token                    *Token
		expectedSecretNamePrefix string
	}{
		{
			name: "When a user data secret is created it should be created with the expected name: prefix + nodepool name + hash",
			token: &Token{
				ConfigGenerator: &ConfigGenerator{
					nodePool: &hyperv1.NodePool{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-nodepool",
						},
					},
					controlplaneNamespace: "test-namespace",
					rolloutConfig: &rolloutConfig{
						releaseImage: &releaseinfo.ReleaseImage{
							ImageStream: &imageapi.ImageStream{
								ObjectMeta: metav1.ObjectMeta{
									Name: "4.17",
								},
							},
						},
						pullSecretName:            "test-pull-secret",
						additionalTrustBundleName: "test-trust-bundle",
						globalConfig:              "test-global-config",
						mcoRawConfig:              "test-mco-raw-config",
					},
				},
			},
			expectedSecretNamePrefix: "user-data-test-nodepool-",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			hash := tc.token.Hash()
			g.Expect(hash).ToNot(BeEmpty())
			secret := tc.token.UserDataSecret()
			g.Expect(secret).NotTo(BeNil())
			g.Expect(secret.Namespace).To(Equal(tc.token.controlplaneNamespace))
			g.Expect(secret.Name).To(Equal(tc.expectedSecretNamePrefix + hash))
		})
	}
}

func TestTokenSecret(t *testing.T) {
	testCases := []struct {
		name                     string
		token                    *Token
		expectedSecretNamePrefix string
	}{
		{
			name: "When a token secret is created it should be created with the expected name: prefix + nodepool name + hash",
			token: &Token{
				ConfigGenerator: &ConfigGenerator{
					nodePool: &hyperv1.NodePool{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-nodepool",
						},
					},
					controlplaneNamespace: "test-namespace",
					rolloutConfig: &rolloutConfig{
						releaseImage: &releaseinfo.ReleaseImage{
							ImageStream: &imageapi.ImageStream{
								ObjectMeta: metav1.ObjectMeta{
									Name: "4.17",
								},
							},
						},
					},
				},
			},
			expectedSecretNamePrefix: "token-test-nodepool-",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			hash := tc.token.Hash()
			g.Expect(hash).ToNot(BeEmpty())
			secret := tc.token.TokenSecret()
			g.Expect(secret).NotTo(BeNil())
			g.Expect(secret.Namespace).To(Equal(tc.token.controlplaneNamespace))
			g.Expect(secret.Name).To(Equal(tc.expectedSecretNamePrefix + hash))
		})
	}
}

func TestOutdatedUserdataSecret(t *testing.T) {
	testCases := []struct {
		name                     string
		token                    *Token
		expectedSecretNamePrefix string
	}{
		{
			name: "When an outdated user data secret is created it should be created with the expected name: prefix + nodepool name + nodePoolAnnotationCurrentConfigVersion annotation",
			token: &Token{
				ConfigGenerator: &ConfigGenerator{
					nodePool: &hyperv1.NodePool{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-nodepool",
							Annotations: map[string]string{
								nodePoolAnnotationCurrentConfigVersion: "old-hash",
							},
						},
					},
					controlplaneNamespace: "test-namespace",
					rolloutConfig: &rolloutConfig{
						releaseImage: &releaseinfo.ReleaseImage{
							ImageStream: &imageapi.ImageStream{
								ObjectMeta: metav1.ObjectMeta{
									Name: "new-release",
								},
							},
						},
					},
				},
			},
			expectedSecretNamePrefix: "user-data-test-nodepool-",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			secret := tc.token.outdatedUserDataSecret()
			g.Expect(secret).NotTo(BeNil())
			g.Expect(secret.Namespace).To(Equal(tc.token.controlplaneNamespace))
			g.Expect(secret.Name).To(Equal(tc.expectedSecretNamePrefix + tc.token.ConfigGenerator.nodePool.Annotations[nodePoolAnnotationCurrentConfigVersion]))
		})
	}
}

func TestOutdatedTokenSecret(t *testing.T) {
	testCases := []struct {
		name                     string
		token                    *Token
		expectedSecretNamePrefix string
	}{
		{
			name: "When an outdated token secret is created it should be created with the expected name: prefix + nodepool name + nodePoolAnnotationCurrentConfigVersion annotation",
			token: &Token{
				ConfigGenerator: &ConfigGenerator{
					nodePool: &hyperv1.NodePool{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-nodepool",
							Annotations: map[string]string{
								nodePoolAnnotationCurrentConfigVersion: "old-hash",
							},
						},
					},
					controlplaneNamespace: "test-namespace",
					rolloutConfig: &rolloutConfig{
						releaseImage: &releaseinfo.ReleaseImage{
							ImageStream: &imageapi.ImageStream{
								ObjectMeta: metav1.ObjectMeta{
									Name: "new-release",
								},
							},
						},
					},
				},
			},
			expectedSecretNamePrefix: "token-test-nodepool-",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			secret := tc.token.outdatedTokenSecret()
			g.Expect(secret).NotTo(BeNil())
			g.Expect(secret.Namespace).To(Equal(tc.token.controlplaneNamespace))
			g.Expect(secret.Name).To(Equal(tc.expectedSecretNamePrefix + tc.token.ConfigGenerator.nodePool.Annotations[nodePoolAnnotationCurrentConfigVersion]))
		})
	}
}

func TestPairedUserDataSecretName(t *testing.T) {
	testCases := []struct {
		name               string
		tokenSecretName    string
		expectedPairedName string
	}{
		{
			name:               "When given a token Secret name, it should return the user data Secret name of the same generation",
			tokenSecretName:    "token-test-nodepool-confighash",
			expectedPairedName: "user-data-test-nodepool-confighash",
		},
		{
			name:               "When given a name without the token prefix, it should only replace the prefix",
			tokenSecretName:    "token-something-else",
			expectedPairedName: "user-data-something-else",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(pairedUserDataSecretName(tc.tokenSecretName)).To(Equal(tc.expectedPairedName),
				"input %q: expected paired user data Secret name %q", tc.tokenSecretName, tc.expectedPairedName)
		})
	}
}

func TestPairedTokenSecretName(t *testing.T) {
	testCases := []struct {
		name               string
		userDataSecretName string
		expectedPairedName string
	}{
		{
			name:               "When given a user data Secret name, it should return the token Secret name of the same generation",
			userDataSecretName: "user-data-test-nodepool-confighash",
			expectedPairedName: "token-test-nodepool-confighash",
		},
		{
			name:               "When given a name without the user data prefix, it should only replace the prefix",
			userDataSecretName: "user-data-something-else",
			expectedPairedName: "token-something-else",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(pairedTokenSecretName(tc.userDataSecretName)).To(Equal(tc.expectedPairedName),
				"input %q: expected paired token Secret name %q", tc.userDataSecretName, tc.expectedPairedName)
		})
	}
}

func TestIsNodePoolUserDataSecretName(t *testing.T) {
	testCases := []struct {
		name          string
		secretName    string
		nodePoolName  string
		expectedMatch bool
	}{
		{
			name:          "When the name is the exact generated shape, it should match",
			secretName:    "user-data-test-nodepool-01d4c0de",
			nodePoolName:  "test-nodepool",
			expectedMatch: true,
		},
		{
			name:          "When the NodePool name contains hyphens, it should still match",
			secretName:    "user-data-a-b-c-01d4c0de",
			nodePoolName:  "a-b-c",
			expectedMatch: true,
		},
		{
			name:          "When the name belongs to another NodePool, it should not match",
			secretName:    "user-data-other-nodepool-01d4c0de",
			nodePoolName:  "test-nodepool",
			expectedMatch: false,
		},
		{
			name:          "When the name belongs to a NodePool whose name extends this one's, it should not match",
			secretName:    "user-data-test-nodepool-extra-01d4c0de",
			nodePoolName:  "test-nodepool",
			expectedMatch: false,
		},
		{
			name:          "When the hash is not eight characters, it should not match",
			secretName:    "user-data-test-nodepool-01d4c0",
			nodePoolName:  "test-nodepool",
			expectedMatch: false,
		},
		{
			name:          "When the hash is not lowercase hexadecimal, it should not match",
			secretName:    "user-data-test-nodepool-notahex1",
			nodePoolName:  "test-nodepool",
			expectedMatch: false,
		},
		{
			name:          "When the name has an arbitrary shape, it should not match",
			secretName:    "custom-bootstrap-secret",
			nodePoolName:  "test-nodepool",
			expectedMatch: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(isNodePoolUserDataSecretName(tc.secretName, tc.nodePoolName)).To(Equal(tc.expectedMatch),
				"input %q for NodePool %q: expected match %t", tc.secretName, tc.nodePoolName, tc.expectedMatch)
		})
	}
}

func TestEmbeddedBootstrapToken(t *testing.T) {
	token := "an-ignition-token"
	encoded := base64.StdEncoding.EncodeToString([]byte(token))
	userDataSecret := func(value string) *corev1.Secret {
		return &corev1.Secret{Data: map[string][]byte{"value": []byte(value)}}
	}

	t.Run("When the ignition config carries an Authorization header, it should return the embedded token", func(t *testing.T) {
		g := NewWithT(t)
		config := fmt.Sprintf(`{"ignition":{"version":"3.2.0","config":{"merge":[{"httpHeaders":[{"name":"Authorization","value":"Bearer %s"}]}]}}}`, encoded)
		got, err := embeddedBootstrapToken(userDataSecret(config))
		g.Expect(err).ToNot(HaveOccurred(), "a well-formed ignition config should parse")
		g.Expect(got).To(Equal(token), "the embedded token should be extracted and decoded")
	})

	t.Run("When the payload is not an ignition config, it should error", func(t *testing.T) {
		g := NewWithT(t)
		_, err := embeddedBootstrapToken(userDataSecret("not-an-ignition-config"))
		g.Expect(err).To(HaveOccurred(), "unparsable content should be reported as an error")
	})

	t.Run("When the ignition config carries no Authorization header, it should error", func(t *testing.T) {
		g := NewWithT(t)
		_, err := embeddedBootstrapToken(userDataSecret(`{"ignition":{"version":"3.2.0","config":{"merge":[{"httpHeaders":[{"name":"NodePool","value":"ns/np"}]}]}}}`))
		g.Expect(err).To(HaveOccurred(), "a config without an Authorization header cannot yield a token")
	})
}

func TestGetIgnitionCACert(t *testing.T) {
	controlplaneNamespace := "controlplane-namespace"
	testCases := []struct {
		name           string
		secret         *corev1.Secret
		expectedCACert []byte
		expectedError  string
	}{
		{
			name: "when the secret exists and has content in the expected key it should return it",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "ignition-server-ca-cert",
					Namespace: controlplaneNamespace,
				},
				Data: map[string][]byte{
					"tls.crt": []byte("something"),
				},
			},
			expectedCACert: []byte("something"),
		},
		{
			name: "When the key does not exist it should fail",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "ignition-server-ca-cert",
					Namespace: controlplaneNamespace,
				},
				Data: map[string][]byte{},
			},
			expectedError: "CA Secret is missing tls.crt key",
		},
		{
			name: "When the secret does not exist it should fail",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "other-secret",
					Namespace: controlplaneNamespace,
				},
				Data: map[string][]byte{},
			},
			expectedError: "secrets \"ignition-server-ca-cert\" not found",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(tc.secret).Build()
			token := &Token{
				ConfigGenerator: &ConfigGenerator{
					Client:                fakeClient,
					controlplaneNamespace: controlplaneNamespace,
				},
			}

			caCert, err := token.getIgnitionCACert(t.Context())
			if tc.expectedError != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(Equal(tc.expectedError))
				return
			}
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(caCert).To(Equal(tc.expectedCACert))
		})
	}
}

func TestSetKarpenterAMILabels(t *testing.T) {
	testCases := []struct {
		name           string
		platform       hyperv1.PlatformType
		userDataSecret *corev1.Secret
		releaseImage   *releaseinfo.ReleaseImage
		region         string
		rhelStream     string
		expectedError  string
		expectedLabels map[string]string
	}{
		{
			name:     "when the user data secret is created for supported platform and architecture it should set the expected labels",
			platform: hyperv1.AWSPlatform,
			region:   "us-east-1",
			userDataSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "user-data-secret",
					Namespace: "test-namespace",
					Labels: map[string]string{
						karpenterutil.ManagedByKarpenterLabel: "true",
					},
				},
			},
			expectedLabels: map[string]string{
				karpenterutil.ArchToAMILabelKey(hyperv1.ArchitectureAMD64): "us-east-1-x86_64-image",
				karpenterutil.ArchToAMILabelKey(hyperv1.ArchitectureARM64): "us-east-1-aarch64-image",
			},
		},
		{
			name:     "when the AMI is unavailable for all architectures it should return an error",
			platform: hyperv1.AWSPlatform,
			region:   "us-west-2",
			userDataSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "user-data-secret",
					Namespace: "test-namespace",
					Labels: map[string]string{
						karpenterutil.ManagedByKarpenterLabel: "true",
					},
				},
			},
			expectedError: "no supported architectures found",
		},
		{
			name:     "when one architecture AMI is unavailable it should set the label only for the available one",
			platform: hyperv1.AWSPlatform,
			region:   "us-east-1",
			userDataSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "user-data-secret",
					Namespace: "test-namespace",
					Labels: map[string]string{
						karpenterutil.ManagedByKarpenterLabel: "true",
					},
				},
			},
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{
					ObjectMeta: metav1.ObjectMeta{Name: "test-release"},
				},
				StreamMetadata: testAWSStream("x86_64", "us-east-1", "ami-amd64-only"),
			},
			expectedLabels: map[string]string{
				karpenterutil.ArchToAMILabelKey(hyperv1.ArchitectureAMD64): "ami-amd64-only",
			},
		},
		{
			name:     "when the user data secret is created for unsupported platform it should return an error",
			platform: hyperv1.AzurePlatform,
			region:   "us-east-1",
			userDataSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "user-data-secret",
					Namespace: "test-namespace",
					Labels: map[string]string{
						karpenterutil.ManagedByKarpenterLabel: "true",
					},
				},
			},
			expectedError: "failed to get supported architectures: unsupported platform: Azure",
		},
		{
			name:       "When rhelStream is rhel-9 with single-stream payload, it should set AMI labels from StreamMetadata fallback",
			platform:   hyperv1.AWSPlatform,
			region:     "us-east-1",
			rhelStream: "rhel-9",
			userDataSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "user-data-secret",
					Namespace: "test-namespace",
					Labels: map[string]string{
						karpenterutil.ManagedByKarpenterLabel: "true",
					},
				},
			},
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{
					ObjectMeta: metav1.ObjectMeta{Name: "4.17.0"},
				},
				StreamMetadata: &stream.Stream{
					Architectures: map[string]stream.Arch{
						"x86_64": {
							Images: stream.Images{
								Aws: &stream.AwsImage{
									Regions: map[string]stream.SingleImage{
										"us-east-1": {Image: "ami-rhel9-fallback-amd64"},
									},
								},
							},
						},
						"aarch64": {
							Images: stream.Images{
								Aws: &stream.AwsImage{
									Regions: map[string]stream.SingleImage{
										"us-east-1": {Image: "ami-rhel9-fallback-arm64"},
									},
								},
							},
						},
					},
				},
				OSStreams: nil,
			},
			expectedLabels: map[string]string{
				karpenterutil.ArchToAMILabelKey(hyperv1.ArchitectureAMD64): "ami-rhel9-fallback-amd64",
				karpenterutil.ArchToAMILabelKey(hyperv1.ArchitectureARM64): "ami-rhel9-fallback-arm64",
			},
		},
		{
			name:       "When rhelStream is rhel-9 with multi-stream payload, it should use OSStreams rhel-9 AMI",
			platform:   hyperv1.AWSPlatform,
			region:     "us-east-1",
			rhelStream: "rhel-9",
			userDataSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "user-data-secret",
					Namespace: "test-namespace",
					Labels: map[string]string{
						karpenterutil.ManagedByKarpenterLabel: "true",
					},
				},
			},
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{
					ObjectMeta: metav1.ObjectMeta{Name: "5.0.0"},
				},
				StreamMetadata: testAWSStream("x86_64", "us-east-1", "ami-default-amd64"),
				OSStreams: map[string]*stream.Stream{
					"rhel-9": testAWSStream("x86_64", "us-east-1", "ami-rhel9-osstream-amd64"),
				},
			},
			expectedLabels: map[string]string{
				karpenterutil.ArchToAMILabelKey(hyperv1.ArchitectureAMD64): "ami-rhel9-osstream-amd64",
			},
		},
		{
			name:       "When rhelStream is rhel-10 with multi-stream payload, it should use OSStreams rhel-10 AMI",
			platform:   hyperv1.AWSPlatform,
			region:     "us-east-1",
			rhelStream: "rhel-10",
			userDataSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "user-data-secret",
					Namespace: "test-namespace",
					Labels: map[string]string{
						karpenterutil.ManagedByKarpenterLabel: "true",
					},
				},
			},
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{
					ObjectMeta: metav1.ObjectMeta{Name: "5.0.0"},
				},
				StreamMetadata: testAWSStream("x86_64", "us-east-1", "ami-default-amd64"),
				OSStreams: map[string]*stream.Stream{
					"rhel-10": testAWSStream("x86_64", "us-east-1", "ami-rhel10-osstream-amd64"),
				},
			},
			expectedLabels: map[string]string{
				karpenterutil.ArchToAMILabelKey(hyperv1.ArchitectureAMD64): "ami-rhel10-osstream-amd64",
			},
		},
	}
	log := testr.New(t)
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			ri := tc.releaseImage
			if ri == nil {
				ri = testutils.InitReleaseImageOrDie("test-release")
			}
			err := setKarpenterAMILabels(log, tc.userDataSecret, tc.region, ri, tc.platform, tc.rhelStream)
			if tc.expectedError != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(Equal(tc.expectedError))
				return
			}
			g.Expect(err).NotTo(HaveOccurred())
			for labelKey, expectedAMI := range tc.expectedLabels {
				g.Expect(tc.userDataSecret.Labels).To(HaveKeyWithValue(labelKey, expectedAMI))
			}
			amdKey := karpenterutil.ArchToAMILabelKey(hyperv1.ArchitectureAMD64)
			armKey := karpenterutil.ArchToAMILabelKey(hyperv1.ArchitectureARM64)
			if _, ok := tc.expectedLabels[amdKey]; !ok {
				g.Expect(tc.userDataSecret.Labels).NotTo(HaveKey(amdKey))
			}
			if _, ok := tc.expectedLabels[armKey]; !ok {
				g.Expect(tc.userDataSecret.Labels).NotTo(HaveKey(armKey))
			}
		})
	}
}

func TestReconcileUserDataSecret(t *testing.T) {
	testCases := []struct {
		name           string
		token          *Token
		userDataSecret *corev1.Secret
		expectedError  string
	}{
		{
			name: "when platform is Azure and NodePool is managed by Karpenter, it should return an error",
			token: &Token{
				ConfigGenerator: &ConfigGenerator{
					hostedCluster: &hyperv1.HostedCluster{
						Spec: hyperv1.HostedClusterSpec{
							Platform: hyperv1.PlatformSpec{Type: hyperv1.AzurePlatform},
							AutoNode: hyperv1.AutoNode{
								Provisioner: hyperv1.ProvisionerConfig{
									Name: hyperv1.ProvisionerKarpenter,
									Karpenter: hyperv1.KarpenterConfig{
										Platform: hyperv1.AzurePlatform,
										Azure: hyperv1.KarpenterAzureConfig{
											ClientID: "12345678-1234-1234-1234-123456789012",
										},
									},
								},
							},
						},
					},
					nodePool: &hyperv1.NodePool{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-nodepool",
							Labels: map[string]string{
								karpenterutil.ManagedByKarpenterLabel: "true",
							},
						},
					},
					rolloutConfig: &rolloutConfig{},
				},
				userData: &userData{},
			},
			userDataSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "user-data-secret",
					Namespace: "test-namespace",
					Labels: map[string]string{
						karpenterutil.ManagedByKarpenterLabel: "true",
					},
				},
			},
			expectedError: "karpenter userData reconciliation is currently not supported for platform: Azure",
		},
	}

	log := testr.New(t)
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			err := tc.token.reconcileUserDataSecret(log, tc.userDataSecret, "test-token")
			if tc.expectedError != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(Equal(tc.expectedError))
				return
			}
			g.Expect(err).NotTo(HaveOccurred())
		})
	}
}

func TestTokenReconcileAfterTokenRotation(t *testing.T) {
	g := NewWithT(t)

	hcName := "test-hc"
	hcNamespace := "namespace"
	controlplaneNamespace := "controlplane-namespace"
	pullSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pull-secret",
			Namespace: hcNamespace,
		},
		Data: map[string][]byte{
			".dockerconfigjson": []byte(`{"auths":{"example.com":{"auth":"dGVzdDp0ZXN0"}}}`),
		},
	}
	ignitionServerCACert := ignitionserver.IgnitionCACertSecret(controlplaneNamespace)
	ignitionServerCACert.Data = map[string][]byte{
		corev1.TLSCertKey: []byte("test-ignition-ca-cert"),
	}

	configGenerator := &ConfigGenerator{
		hostedCluster: &hyperv1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      hcName,
				Namespace: hcNamespace,
			},
			Spec: hyperv1.HostedClusterSpec{
				PullSecret: corev1.LocalObjectReference{Name: pullSecret.GetName()},
			},
			Status: hyperv1.HostedClusterStatus{
				IgnitionEndpoint: "https://example.com",
			},
		},
		nodePool: &hyperv1.NodePool{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-np",
				Namespace: hcNamespace,
				Annotations: map[string]string{
					nodePoolAnnotationCurrentConfigVersion: "placeholder",
					nodePoolAnnotationCurrentRolloutConfig: "placeholder",
				},
			},
			Spec: hyperv1.NodePoolSpec{
				Management: hyperv1.NodePoolManagement{UpgradeType: hyperv1.UpgradeTypeReplace},
				Release:    hyperv1.Release{Image: "image:4.17"},
				Platform:   hyperv1.NodePoolPlatform{Type: hyperv1.AWSPlatform},
			},
		},
		controlplaneNamespace: controlplaneNamespace,
		rolloutConfig: &rolloutConfig{
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{
					ObjectMeta: metav1.ObjectMeta{Name: "4.17"},
				},
			},
			globalConfig: "test-global-config",
			mcoRawConfig: "raw-config",
		},
	}
	cpoCapabilities := &CPOCapabilities{DecompressAndDecodeConfig: true}

	fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(pullSecret, ignitionServerCACert).Build()
	configGenerator.Client = fakeClient

	token, err := NewToken(t.Context(), configGenerator, cpoCapabilities)
	g.Expect(err).ToNot(HaveOccurred())

	// Seed the annotations so isOutdated() returns false on subsequent calls.
	configGenerator.nodePool.Annotations[nodePoolAnnotationCurrentConfigVersion] = token.Hash()
	configGenerator.nodePool.Annotations[nodePoolAnnotationCurrentRolloutConfig] = token.RolloutHashWithoutVersion()
	configGenerator.nodePool.Status.Version = token.Version()

	// First reconcile: creates the token and user data secrets.
	err = token.Reconcile(t.Context())
	g.Expect(err).ToNot(HaveOccurred())

	// Read back the original token UUID from the token secret.
	gotTokenSecret := &corev1.Secret{}
	err = fakeClient.Get(t.Context(), crclient.ObjectKeyFromObject(token.TokenSecret()), gotTokenSecret)
	g.Expect(err).ToNot(HaveOccurred())
	originalToken := string(gotTokenSecret.Data[TokenSecretTokenKey])
	g.Expect(originalToken).ToNot(BeEmpty())

	// Read back the user data secret and verify it contains the original token.
	gotUserData := &corev1.Secret{}
	err = fakeClient.Get(t.Context(), crclient.ObjectKeyFromObject(token.UserDataSecret()), gotUserData)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(string(gotUserData.Data["value"])).To(ContainSubstring(
		base64.StdEncoding.EncodeToString([]byte(originalToken)),
	))

	// Simulate token rotation: the ignition server's TokenSecretReconciler
	// generates a new UUID and moves the current one to old_token.
	rotatedToken := uuid.New().String()
	gotTokenSecret.Data["old_token"] = []byte(originalToken)
	gotTokenSecret.Data[TokenSecretTokenKey] = []byte(rotatedToken)
	err = fakeClient.Update(t.Context(), gotTokenSecret)
	g.Expect(err).ToNot(HaveOccurred())

	// Verify isOutdated() is false — no spec-driven change occurred.
	g.Expect(token.isOutdated()).To(BeFalse(), "isOutdated should be false: no spec-driven change")

	// Second reconcile: should still refresh the user data secret with the
	// rotated token, even though isOutdated() is false.
	err = token.Reconcile(t.Context())
	g.Expect(err).ToNot(HaveOccurred())

	// Read back the user data secret and verify it now contains the ROTATED
	// token, not the original. This is the core assertion: if Token.Reconcile()
	// had skipped secret maintenance, the user data would still hold the
	// original token UUID, which the ignition server would eventually purge.
	err = fakeClient.Get(t.Context(), crclient.ObjectKeyFromObject(token.UserDataSecret()), gotUserData)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(string(gotUserData.Data["value"])).To(ContainSubstring(
		base64.StdEncoding.EncodeToString([]byte(rotatedToken)),
	), "user data secret must contain the rotated token UUID")
	g.Expect(string(gotUserData.Data["value"])).ToNot(ContainSubstring(
		base64.StdEncoding.EncodeToString([]byte(originalToken)),
	), "user data secret must NOT contain the old token UUID")
}

// TestTokenReconcileManagementSideDrift verifies that Token.Reconcile() does
// NOT create orphan secrets when only management-side content changes (e.g.
// HAProxy image bump). Instead, it should maintain the deployed secrets
// (identified by the nodePoolAnnotationCurrentConfigVersion annotation).
func TestResolveEffectiveHash(t *testing.T) {
	configGenerator := &ConfigGenerator{
		controlplaneNamespace: "clusters-test",
		rolloutConfig: &rolloutConfig{
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: "4.18.5"}},
			},
			mcoRawConfig:        "user-config-A + haproxy-B",
			rolloutMcoRawConfig: "user-config-A",
		},
	}
	currentHash := configGenerator.Hash()
	rolloutHash := configGenerator.RolloutHash()
	const previousHash = "deadbeef"
	const olderHash = "cafebabe"

	tests := []struct {
		name              string
		outdated          bool
		deployedHash      string
		completedHash     string
		inProgressRollout string
		deployedVersion   string
		deployedRollout   string
		versionChanged    bool
		expectedState     secretMaintenanceState
		expectedHash      string
	}{
		{
			name:            "When a newer release is requested during a legacy rollout, it should supersede the old payload",
			outdated:        true,
			deployedHash:    previousHash,
			completedHash:   olderHash,
			deployedVersion: "4.18.4",
			expectedState:   stateSupersededRollout,
			expectedHash:    currentHash,
		},
		{
			name:              "When the workload target matches but NodePool tracking is stale, it should continue the deployed payload",
			outdated:          true,
			deployedHash:      previousHash,
			completedHash:     olderHash,
			inProgressRollout: "stale-request",
			deployedRollout:   rolloutHash,
			expectedState:     stateContinuedRollout,
			expectedHash:      previousHash,
		},
		{
			name:            "When the workload target differs and NodePool tracking is missing, it should supersede the deployed payload",
			outdated:        true,
			deployedHash:    previousHash,
			completedHash:   olderHash,
			deployedRollout: "previous-request",
			expectedState:   stateSupersededRollout,
			expectedHash:    currentHash,
		},
		{
			name:          "When no hashes are recorded and no change is pending, it should maintain the current hash in Steady",
			expectedState: stateSteady,
			expectedHash:  currentHash,
		},
		{
			name:          "When deployed and completed hashes match the current hash, it should remain Steady",
			deployedHash:  currentHash,
			completedHash: currentHash,
			expectedState: stateSteady,
			expectedHash:  currentHash,
		},
		{
			name:          "When management content drifts, it should prefer the deployed hash over the completed hash",
			deployedHash:  previousHash,
			completedHash: olderHash,
			expectedState: stateManagementDrift,
			expectedHash:  previousHash,
		},
		{
			name:          "When management content drifts without a bootstrap reference, it should maintain the completed hash",
			completedHash: previousHash,
			expectedState: stateCompletedDrift,
			expectedHash:  previousHash,
		},
		{
			name:          "When the deployed hash matches current but completion differs without a pending change, it should use CompletedDrift",
			deployedHash:  currentHash,
			completedHash: previousHash,
			expectedState: stateCompletedDrift,
			expectedHash:  previousHash,
		},
		{
			name:          "When an outdated NodePool has no recorded hashes, it should start a NewRollout under the current hash",
			outdated:      true,
			expectedState: stateNewRollout,
			expectedHash:  currentHash,
		},
		{
			name:          "When a spec change has no deployed hash but an older completed hash, it should select NewRollout instead of CompletedDrift",
			outdated:      true,
			completedHash: previousHash,
			expectedState: stateNewRollout,
			expectedHash:  currentHash,
		},
		{
			name:              "When the current rollout target is already deployed but incomplete, it should maintain it as NewRollout",
			outdated:          true,
			deployedHash:      currentHash,
			completedHash:     previousHash,
			inProgressRollout: rolloutHash,
			expectedState:     stateNewRollout,
			expectedHash:      currentHash,
		},
		{
			name:          "When a config change follows a completed rollout, it should select NewRollout",
			outdated:      true,
			deployedHash:  previousHash,
			completedHash: previousHash,
			expectedState: stateNewRollout,
			expectedHash:  currentHash,
		},
		{
			name:           "When a version upgrade starts without an in-progress annotation, it should select NewRollout instead of SupersededRollout",
			outdated:       true,
			deployedHash:   previousHash,
			completedHash:  previousHash,
			versionChanged: true,
			expectedState:  stateNewRollout,
			expectedHash:   currentHash,
		},
		{
			name:              "When a version rollout has aligned deployed and completed hashes with a matching marker, it should continue the deployed target",
			outdated:          true,
			deployedHash:      previousHash,
			completedHash:     previousHash,
			inProgressRollout: rolloutHash,
			versionChanged:    true,
			expectedState:     stateContinuedRollout,
			expectedHash:      previousHash,
		},
		{
			name:           "When an unannotated version rollout has a deployed target ahead of completion, it should adopt the deployed hash",
			outdated:       true,
			deployedHash:   previousHash,
			completedHash:  olderHash,
			versionChanged: true,
			expectedState:  stateAdoptedRollout,
			expectedHash:   previousHash,
		},
		{
			name:          "When an unannotated rollout has a different deployed hash and no completed hash, it should adopt the deployed hash",
			outdated:      true,
			deployedHash:  previousHash,
			expectedState: stateAdoptedRollout,
			expectedHash:  previousHash,
		},
		{
			name:              "When management content drifts during the same config rollout, it should continue the deployed target",
			outdated:          true,
			deployedHash:      previousHash,
			completedHash:     olderHash,
			inProgressRollout: rolloutHash,
			expectedState:     stateContinuedRollout,
			expectedHash:      previousHash,
		},
		{
			name:              "When a new spec change supersedes a config rollout, it should select the current hash as SupersededRollout",
			outdated:          true,
			deployedHash:      previousHash,
			completedHash:     olderHash,
			inProgressRollout: "abcdef01",
			expectedState:     stateSupersededRollout,
			expectedHash:      currentHash,
		},
		{
			name:              "When a new spec change supersedes a version rollout with aligned hashes, it should select SupersededRollout",
			outdated:          true,
			deployedHash:      previousHash,
			completedHash:     previousHash,
			inProgressRollout: "abcdef01",
			versionChanged:    true,
			expectedState:     stateSupersededRollout,
			expectedHash:      currentHash,
		},
		{
			name:              "When deployed and completed hashes and versions agree despite a matching marker, it should select NewRollout",
			outdated:          true,
			deployedHash:      previousHash,
			completedHash:     previousHash,
			inProgressRollout: rolloutHash,
			expectedState:     stateNewRollout,
			expectedHash:      currentHash,
		},
		{
			name:              "When deployed and completed hashes and versions agree despite a different marker, it should select NewRollout",
			outdated:          true,
			deployedHash:      previousHash,
			completedHash:     previousHash,
			inProgressRollout: "abcdef01",
			expectedState:     stateNewRollout,
			expectedHash:      currentHash,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			cg := *configGenerator
			cg.nodePool = &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{Name: "workers", Namespace: "clusters"},
				Status:     hyperv1.NodePoolStatus{Version: cg.Version()},
			}
			if tc.completedHash != "" || tc.inProgressRollout != "" {
				cg.nodePool.Annotations = map[string]string{}
				if tc.completedHash != "" {
					cg.nodePool.Annotations[nodePoolAnnotationCurrentConfigVersion] = tc.completedHash
				}
				if tc.inProgressRollout != "" {
					cg.nodePool.Annotations[nodePoolAnnotationInProgressRolloutConfig] = tc.inProgressRollout
				}
			}
			if tc.versionChanged {
				cg.nodePool.Status.Version = "4.18.4"
			}
			token := &Token{
				ConfigGenerator:       &cg,
				deployedBootstrapHash: tc.deployedHash,
				deployedVersion:       tc.deployedVersion,
				deployedRolloutConfig: tc.deployedRollout,
			}

			g.Expect(token.resolveEffectiveHash(tc.outdated)).To(Equal(tc.expectedState))
			g.Expect(token.secretState).To(Equal(tc.expectedState))
			g.Expect(token.effectiveHash).To(Equal(tc.expectedHash))
		})
	}
}

func TestTokenReconcileManagementSideDrift(t *testing.T) {
	g := NewWithT(t)

	hcName := "test-hc"
	hcNamespace := "namespace"
	controlplaneNamespace := "controlplane-namespace"
	pullSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pull-secret",
			Namespace: hcNamespace,
		},
		Data: map[string][]byte{
			".dockerconfigjson": []byte(`{"auths":{"example.com":{"auth":"dGVzdDp0ZXN0"}}}`),
		},
	}
	ignitionServerCACert := ignitionserver.IgnitionCACertSecret(controlplaneNamespace)
	ignitionServerCACert.Data = map[string][]byte{
		corev1.TLSCertKey: []byte("test-ignition-ca-cert"),
	}

	configGenerator := &ConfigGenerator{
		hostedCluster: &hyperv1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      hcName,
				Namespace: hcNamespace,
			},
			Spec: hyperv1.HostedClusterSpec{
				PullSecret: corev1.LocalObjectReference{Name: pullSecret.GetName()},
			},
			Status: hyperv1.HostedClusterStatus{
				IgnitionEndpoint: "https://example.com",
			},
		},
		nodePool: &hyperv1.NodePool{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-np",
				Namespace: hcNamespace,
				Annotations: map[string]string{
					nodePoolAnnotationCurrentConfigVersion: "placeholder",
					nodePoolAnnotationCurrentRolloutConfig: "placeholder",
				},
			},
			Spec: hyperv1.NodePoolSpec{
				Management: hyperv1.NodePoolManagement{UpgradeType: hyperv1.UpgradeTypeReplace},
				Release:    hyperv1.Release{Image: "image:4.17"},
				Platform:   hyperv1.NodePoolPlatform{Type: hyperv1.AWSPlatform},
			},
		},
		controlplaneNamespace: controlplaneNamespace,
		rolloutConfig: &rolloutConfig{
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{
					ObjectMeta: metav1.ObjectMeta{Name: "4.17"},
				},
			},
			globalConfig: "test-global-config",
			mcoRawConfig: "raw-config",
		},
	}
	cpoCapabilities := &CPOCapabilities{DecompressAndDecodeConfig: true}

	fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(pullSecret, ignitionServerCACert).Build()
	configGenerator.Client = fakeClient

	token, err := NewToken(t.Context(), configGenerator, cpoCapabilities)
	g.Expect(err).ToNot(HaveOccurred())

	// Seed annotations so isOutdated() returns false.
	configGenerator.nodePool.Annotations[nodePoolAnnotationCurrentConfigVersion] = token.Hash()
	configGenerator.nodePool.Annotations[nodePoolAnnotationCurrentRolloutConfig] = token.RolloutHashWithoutVersion()
	configGenerator.nodePool.Status.Version = token.Version()

	// First reconcile: creates the initial token and user data secrets.
	err = token.Reconcile(t.Context())
	g.Expect(err).ToNot(HaveOccurred())

	deployedHash := token.Hash()
	deployedTokenSecretName := token.TokenSecret().Name
	deployedUserDataSecretName := token.UserDataSecret().Name

	// Read back and verify initial secrets exist.
	gotTokenSecret := &corev1.Secret{}
	err = fakeClient.Get(t.Context(), crclient.ObjectKeyFromObject(token.TokenSecret()), gotTokenSecret)
	g.Expect(err).ToNot(HaveOccurred())
	originalTokenUUID := string(gotTokenSecret.Data[TokenSecretTokenKey])
	g.Expect(originalTokenUUID).ToNot(BeEmpty())

	// Simulate a management-side-only change: modify globalConfig (included in
	// Hash() but excluded from RolloutHash). This changes Hash() without
	// changing RolloutHashWithoutVersion().
	configGenerator.rolloutConfig.globalConfig = "changed-global-config"
	token2, err := NewToken(t.Context(), configGenerator, cpoCapabilities)
	g.Expect(err).ToNot(HaveOccurred())

	// Verify the management change actually changed Hash() but not RolloutHash.
	g.Expect(token2.Hash()).ToNot(Equal(deployedHash), "Hash should change with management content")
	g.Expect(token2.RolloutHashWithoutVersion()).To(Equal(token.RolloutHashWithoutVersion()),
		"RolloutHashWithoutVersion should NOT change")
	g.Expect(token2.isOutdated()).To(BeFalse(), "isOutdated should be false for management-side drift")

	// Second reconcile: should maintain the deployed secrets, NOT create orphans.
	err = token2.Reconcile(t.Context())
	g.Expect(err).ToNot(HaveOccurred())

	// Verify the deployed secrets still exist and were maintained.
	err = fakeClient.Get(t.Context(), crclient.ObjectKeyFromObject(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: deployedTokenSecretName, Namespace: controlplaneNamespace},
	}), gotTokenSecret)
	g.Expect(err).ToNot(HaveOccurred(), "deployed token secret should still exist")

	gotUserData := &corev1.Secret{}
	err = fakeClient.Get(t.Context(), crclient.ObjectKeyFromObject(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: deployedUserDataSecretName, Namespace: controlplaneNamespace},
	}), gotUserData)
	g.Expect(err).ToNot(HaveOccurred(), "deployed user data secret should still exist")

	// Verify no orphan secrets were created under the new hash.
	orphanTokenSecret := token2.TokenSecret()
	orphanUserDataSecret := token2.UserDataSecret()
	g.Expect(orphanTokenSecret.Name).ToNot(Equal(deployedTokenSecretName),
		"new hash should produce a different secret name")

	err = fakeClient.Get(t.Context(), crclient.ObjectKeyFromObject(orphanTokenSecret), &corev1.Secret{})
	g.Expect(apierrors.IsNotFound(err)).To(BeTrue(),
		"orphan token secret under new hash should NOT have been created")
	err = fakeClient.Get(t.Context(), crclient.ObjectKeyFromObject(orphanUserDataSecret), &corev1.Secret{})
	g.Expect(apierrors.IsNotFound(err)).To(BeTrue(),
		"orphan user data secret under new hash should NOT have been created")

	// Verify the deployed user data secret still contains the original token UUID.
	g.Expect(string(gotUserData.Data["value"])).To(ContainSubstring(
		base64.StdEncoding.EncodeToString([]byte(originalTokenUUID)),
	), "deployed user data secret should still reference the original token UUID")
}

func TestTokenRepairMissingReferencedSecrets(t *testing.T) {
	controlplaneNamespace := "test-namespace"
	nodePoolName := "test-nodepool"
	hcName := "test-hostedcluster"
	hcNamespace := "test-hc-namespace"

	pullSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pull-secret", Namespace: hcNamespace},
		Data:       map[string][]byte{".dockerconfigjson": []byte("test-pull-secret")},
	}
	additionalTrustBundle := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "test-trust-bundle", Namespace: hcNamespace},
		Data:       map[string]string{"ca-bundle.crt": "test-ca-bundle"},
	}
	ignitionServerCACert := ignitionserver.IgnitionCACertSecret(controlplaneNamespace)
	ignitionServerCACert.Data = map[string][]byte{
		corev1.TLSCertKey: []byte("test-ignition-ca-cert"),
	}

	// staleToken stands in for a token of an older config generation that the
	// ignition server may no longer serve.
	staleToken := "stale-ignition-token"
	staleBearer := "Bearer " + base64.StdEncoding.EncodeToString([]byte(staleToken))

	// A user data Secret of an older config generation, as written by a
	// previous reconciliation of the given NodePool.
	staleUserDataSecret := func(poolName, hash string) *corev1.Secret {
		return &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: controlplaneNamespace,
				Name:      fmt.Sprintf("%s-%s-%s", UserDataSecrePrefix, poolName, hash),
			},
			Data: map[string][]byte{
				"value": []byte(fmt.Sprintf(`{"ignition":{"version":"3.2.0","config":{"merge":[{"httpHeaders":[{"name":"Authorization","value":"%s"}]}]}}}`, staleBearer)),
			},
		}
	}

	// The token Secret paired with a user data Secret of the same generation,
	// carrying the given token value.
	pairedTokenSecret := func(hash, token string) *corev1.Secret {
		return &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: controlplaneNamespace,
				Name:      fmt.Sprintf("%s-%s-%s", TokenSecretPrefix, nodePoolName, hash),
			},
			Data: map[string][]byte{TokenSecretTokenKey: []byte(token)},
		}
	}

	// A MachineDeployment written by a previous generation pointing at a user
	// data Secret of that generation, as happens on management-side-only
	// config changes where no rollout moves the reference.
	machineDeploymentFor := func(userDataName string) *capiv1.MachineDeployment {
		return &capiv1.MachineDeployment{
			ObjectMeta: metav1.ObjectMeta{Name: nodePoolName, Namespace: controlplaneNamespace},
			Spec: capiv1.MachineDeploymentSpec{
				Template: capiv1.MachineTemplateSpec{
					Spec: capiv1.MachineSpec{
						Bootstrap: capiv1.Bootstrap{DataSecretName: ptr.To(userDataName)},
					},
				},
			},
		}
	}

	setup := func(t *testing.T, referencedName string, extraObjects ...crclient.Object) (*Token, crclient.Client) {
		objects := append([]crclient.Object{
			pullSecret,
			additionalTrustBundle,
			ignitionServerCACert,
			machineDeploymentFor(referencedName),
		}, extraObjects...)
		fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(objects...).Build()

		configGenerator := &ConfigGenerator{
			Client: fakeClient,
			hostedCluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: hcName, Namespace: hcNamespace},
				Spec: hyperv1.HostedClusterSpec{
					PullSecret:            corev1.LocalObjectReference{Name: pullSecret.GetName()},
					AdditionalTrustBundle: &corev1.LocalObjectReference{Name: additionalTrustBundle.GetName()},
				},
				Status: hyperv1.HostedClusterStatus{
					IgnitionEndpoint: "https://example.com",
				},
			},
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{Name: nodePoolName, Namespace: controlplaneNamespace},
				Spec: hyperv1.NodePoolSpec{
					Management: hyperv1.NodePoolManagement{UpgradeType: hyperv1.UpgradeTypeReplace},
					Release:    hyperv1.Release{Image: "image:4.17"},
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AzurePlatform,
					},
				},
			},
			controlplaneNamespace: controlplaneNamespace,
			rolloutConfig: &rolloutConfig{
				releaseImage: &releaseinfo.ReleaseImage{
					ImageStream: &imageapi.ImageStream{
						ObjectMeta: metav1.ObjectMeta{Name: "4.17"},
						Status: imageapi.ImageStreamStatus{
							Tags: []imageapi.NamedTagEventList{{
								Tag: "4.17",
								Items: []imageapi.TagEvent{{
									DockerImageReference: "image:4.17",
								}},
							}},
						},
					},
				},
				globalConfig: "test-global-config",
				mcoRawConfig: "raw-config",
			},
		}
		token, err := NewToken(t.Context(), configGenerator, &CPOCapabilities{DecompressAndDecodeConfig: true})
		if err != nil {
			t.Fatalf("failed to create token: %v", err)
		}
		return token, fakeClient
	}

	currentEncodedToken := func(t *testing.T, c crclient.Client, token *Token) string {
		gotTokenSecret := &corev1.Secret{}
		if err := c.Get(t.Context(), crclient.ObjectKeyFromObject(token.TokenSecret()), gotTokenSecret); err != nil {
			t.Fatalf("failed to get current token Secret: %v", err)
		}
		return base64.StdEncoding.EncodeToString(gotTokenSecret.Data[TokenSecretTokenKey])
	}

	t.Run("When a referenced user data Secret is missing, it should be re-created with the current token", func(t *testing.T) {
		g := NewWithT(t)
		staleUserDataName := fmt.Sprintf("%s-%s-%s", UserDataSecrePrefix, nodePoolName, "01d4c0de")
		token, fakeClient := setup(t, staleUserDataName)

		g.Expect(token.Reconcile(t.Context())).To(Succeed(), "reconcile should repair the missing referenced Secret")

		// The referenced user data Secret must have been re-created, carrying the
		// current token so a Machine that has not booted yet can still fetch its
		// ignition payload.
		repaired := &corev1.Secret{}
		g.Expect(fakeClient.Get(t.Context(), crclient.ObjectKey{Namespace: controlplaneNamespace, Name: staleUserDataName}, repaired)).
			To(Succeed(), "missing referenced user data Secret should be re-created")
		g.Expect(repaired.Annotations[nodePoolAnnotation]).To(Equal(fmt.Sprintf("%s/%s", controlplaneNamespace, nodePoolName)),
			"repaired Secret should be annotated with its NodePool")
		g.Expect(string(repaired.Data["value"])).To(ContainSubstring("Bearer "+currentEncodedToken(t, fakeClient, token)),
			"repaired Secret should embed the current token")

		// Repair must not invent user data Secrets that are not referenced.
		unreferencedName := fmt.Sprintf("%s-%s-%s", UserDataSecrePrefix, nodePoolName, "feedface")
		g.Expect(fakeClient.Get(t.Context(), crclient.ObjectKey{Namespace: controlplaneNamespace, Name: unreferencedName}, &corev1.Secret{})).
			To(HaveOccurred(), "repair must not invent unreferenced user data Secrets")
	})

	t.Run("When a Machine references an arbitrarily named Secret that does not exist, it should not be created", func(t *testing.T) {
		g := NewWithT(t)
		// A Bootstrap.DataSecretName can point at any name; repair must only
		// manage Secrets named by this controller (user-data-<np>-<hash>), not
		// hijack an arbitrary name by filling it with ignition data.
		customName := "custom-bootstrap-secret"
		token, fakeClient := setup(t, customName)

		g.Expect(token.Reconcile(t.Context())).To(Succeed(), "reconcile should skip an arbitrarily named reference without error")

		g.Expect(fakeClient.Get(t.Context(), crclient.ObjectKey{Namespace: controlplaneNamespace, Name: customName}, &corev1.Secret{})).
			ToNot(Succeed(), "repair must not create a Secret under an arbitrary referenced name")
	})

	t.Run("When a Machine references a missing user data Secret named for a different NodePool, it should not be created", func(t *testing.T) {
		g := NewWithT(t)
		// A drifted reference: the name carries another NodePool's prefix.
		// Repair must only manage Secrets named for this NodePool, so it must
		// not create another NodePool's Secret.
		driftedName := fmt.Sprintf("%s-%s-%s", UserDataSecrePrefix, "other-nodepool", "abcdef01")
		token, fakeClient := setup(t, driftedName)

		g.Expect(token.Reconcile(t.Context())).To(Succeed(), "reconcile should skip a drifted reference without error")

		g.Expect(fakeClient.Get(t.Context(), crclient.ObjectKey{Namespace: controlplaneNamespace, Name: driftedName}, &corev1.Secret{})).
			ToNot(Succeed(), "repair must not create a Secret named for another NodePool")
	})

	t.Run("When a Machine references a missing Secret of a NodePool whose name extends this one's, it should not be created", func(t *testing.T) {
		g := NewWithT(t)
		// "test-nodepool-extra" is a different NodePool whose name extends this
		// one's: a prefix match on user-data-<np>- would accept its Secret, so
		// the complete generated name shape must be validated.
		extendingName := fmt.Sprintf("%s-%s-%s", UserDataSecrePrefix, nodePoolName+"-extra", "abcdef01")
		token, fakeClient := setup(t, extendingName)

		g.Expect(token.Reconcile(t.Context())).To(Succeed(), "reconcile should skip a reference to an extending NodePool's Secret without error")

		g.Expect(fakeClient.Get(t.Context(), crclient.ObjectKey{Namespace: controlplaneNamespace, Name: extendingName}, &corev1.Secret{})).
			ToNot(Succeed(), "repair must not create a Secret named for a NodePool whose name extends this one's")
	})

	t.Run("When a Machine references an existing Secret of a NodePool whose name extends this one's, it should not be rewritten", func(t *testing.T) {
		g := NewWithT(t)
		extendingName := fmt.Sprintf("%s-%s-%s", UserDataSecrePrefix, nodePoolName+"-extra", "abcdef01")
		token, fakeClient := setup(t, extendingName, staleUserDataSecret(nodePoolName+"-extra", "abcdef01"))

		g.Expect(token.Reconcile(t.Context())).To(Succeed(), "reconcile should skip an extending NodePool's Secret without error")

		kept := &corev1.Secret{}
		g.Expect(fakeClient.Get(t.Context(), crclient.ObjectKey{Namespace: controlplaneNamespace, Name: extendingName}, kept)).
			To(Succeed(), "the extending NodePool's Secret should still exist")
		g.Expect(string(kept.Data["value"])).To(ContainSubstring(staleBearer),
			"a Secret named for a NodePool whose name extends this one's must not be rewritten")
	})

	t.Run("When a Machine references an existing user data Secret named for a different NodePool, it should not be rewritten", func(t *testing.T) {
		g := NewWithT(t)
		// The drifted Secret exists and its paired token Secret is missing,
		// which would trigger a refresh for one of this NodePool's own
		// Secrets: the refresh must not reach a Secret owned by another
		// NodePool.
		driftedName := fmt.Sprintf("%s-%s-%s", UserDataSecrePrefix, "other-nodepool", "abcdef01")
		token, fakeClient := setup(t, driftedName, staleUserDataSecret("other-nodepool", "abcdef01"))

		g.Expect(token.Reconcile(t.Context())).To(Succeed(), "reconcile should skip a drifted reference without error")

		kept := &corev1.Secret{}
		g.Expect(fakeClient.Get(t.Context(), crclient.ObjectKey{Namespace: controlplaneNamespace, Name: driftedName}, kept)).
			To(Succeed(), "the drifted Secret should still exist")
		g.Expect(string(kept.Data["value"])).To(ContainSubstring(staleBearer),
			"a Secret named for another NodePool must not be rewritten by this NodePool")
	})

	t.Run("When a referenced user data Secret exists but its paired token Secret is missing, it should be refreshed with the current token", func(t *testing.T) {
		g := NewWithT(t)
		staleUserDataName := fmt.Sprintf("%s-%s-%s", UserDataSecrePrefix, nodePoolName, "01d4c0de")
		token, fakeClient := setup(t, staleUserDataName, staleUserDataSecret(nodePoolName, "01d4c0de"))

		g.Expect(token.Reconcile(t.Context())).To(Succeed(), "reconcile should refresh the referenced Secret whose paired token Secret is missing")

		refreshed := &corev1.Secret{}
		g.Expect(fakeClient.Get(t.Context(), crclient.ObjectKey{Namespace: controlplaneNamespace, Name: staleUserDataName}, refreshed)).
			To(Succeed(), "referenced user data Secret should still exist")
		g.Expect(string(refreshed.Data["value"])).To(ContainSubstring("Bearer "+currentEncodedToken(t, fakeClient, token)),
			"referenced Secret should be refreshed with the current token when its paired token Secret is missing")
		g.Expect(string(refreshed.Data["value"])).ToNot(ContainSubstring(staleBearer),
			"stale token should be replaced by the refresh")
	})

	t.Run("When a referenced user data Secret and its paired token Secret both exist, it should be left unchanged", func(t *testing.T) {
		g := NewWithT(t)
		staleUserDataName := fmt.Sprintf("%s-%s-%s", UserDataSecrePrefix, nodePoolName, "01d4c0de")
		token, fakeClient := setup(t, staleUserDataName, staleUserDataSecret(nodePoolName, "01d4c0de"), pairedTokenSecret("01d4c0de", staleToken))

		g.Expect(token.Reconcile(t.Context())).To(Succeed(), "reconcile should succeed when the referenced pair is healthy")

		kept := &corev1.Secret{}
		g.Expect(fakeClient.Get(t.Context(), crclient.ObjectKey{Namespace: controlplaneNamespace, Name: staleUserDataName}, kept)).
			To(Succeed(), "healthy referenced user data Secret should be kept")
		g.Expect(string(kept.Data["value"])).To(ContainSubstring(staleBearer),
			"user data Secret with a live paired token Secret must not be rewritten")
	})

	t.Run("When the paired token Secret exists but the ignition server rotated its token, it should be refreshed with the current token", func(t *testing.T) {
		g := NewWithT(t)
		// The paired token Secret still exists, but the ignition server rotated
		// the token value inside it: the token embedded in the user data Secret
		// is no longer served, so Machines booting from it keep receiving
		// "Token not found" and never join the cluster.
		staleUserDataName := fmt.Sprintf("%s-%s-%s", UserDataSecrePrefix, nodePoolName, "01d4c0de")
		token, fakeClient := setup(t, staleUserDataName, staleUserDataSecret(nodePoolName, "01d4c0de"), pairedTokenSecret("01d4c0de", "rotated-ignition-token"))

		g.Expect(token.Reconcile(t.Context())).To(Succeed(), "reconcile should refresh the referenced Secret whose embedded token was rotated out")

		refreshed := &corev1.Secret{}
		g.Expect(fakeClient.Get(t.Context(), crclient.ObjectKey{Namespace: controlplaneNamespace, Name: staleUserDataName}, refreshed)).
			To(Succeed(), "referenced user data Secret should still exist")
		g.Expect(string(refreshed.Data["value"])).To(ContainSubstring("Bearer "+currentEncodedToken(t, fakeClient, token)),
			"referenced Secret should be refreshed with the current token when its embedded token is no longer served")
		g.Expect(string(refreshed.Data["value"])).ToNot(ContainSubstring(staleBearer),
			"rotated-out token should be replaced by the refresh")
	})

	t.Run("When a referenced user data Secret's ignition config cannot be parsed, it should be refreshed", func(t *testing.T) {
		g := NewWithT(t)
		// Content that cannot be parsed as an ignition config cannot be served
		// to a Machine either: repair rewrites it with a servable config.
		staleUserDataName := fmt.Sprintf("%s-%s-%s", UserDataSecrePrefix, nodePoolName, "01d4c0de")
		unparsable := staleUserDataSecret(nodePoolName, "01d4c0de")
		unparsable.Data["value"] = []byte("not-an-ignition-config")
		token, fakeClient := setup(t, staleUserDataName, unparsable, pairedTokenSecret("01d4c0de", staleToken))

		g.Expect(token.Reconcile(t.Context())).To(Succeed(), "reconcile should refresh a referenced Secret with unparsable content")

		refreshed := &corev1.Secret{}
		g.Expect(fakeClient.Get(t.Context(), crclient.ObjectKey{Namespace: controlplaneNamespace, Name: staleUserDataName}, refreshed)).
			To(Succeed(), "referenced user data Secret should still exist")
		g.Expect(string(refreshed.Data["value"])).To(ContainSubstring("Bearer "+currentEncodedToken(t, fakeClient, token)),
			"unparsable Secret should be rewritten with a servable ignition config")
	})
}
