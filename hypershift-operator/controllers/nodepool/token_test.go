package nodepool

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/ignitionserver"
	"github.com/openshift/hypershift/support/api"
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
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ignitionapi "github.com/coreos/ignition/v2/config/v3_2/types"
	"github.com/coreos/stream-metadata-go/stream"
	"github.com/go-logr/logr/testr"
	"github.com/google/uuid"
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

			fakeClient := fake.NewClientBuilder().WithObjects(tc.fakeObjects...).Build()
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
		name          string
		token         *Token
		fakeObjects   []crclient.Object
		expectedError string
	}{
		{
			name: "When userdata and token secret are outdated, it should delete userdata secret and add expiration timestamp to token secret",
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
				userdataSecret,
				tokenSecret,
			},
			expectedError: "",
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

			fakeClient := fake.NewClientBuilder().WithObjects(tc.fakeObjects...).Build()
			tc.token.Client = fakeClient

			err := tc.token.cleanupOutdated(t.Context())
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
	t.Run("When the active token rotates without a rollout, it should refresh the referenced user data only", func(t *testing.T) {
		g := NewWithT(t)
		nodePool := &hyperv1.NodePool{
			ObjectMeta: metav1.ObjectMeta{Name: "workers", Namespace: "clusters"},
		}
		config := &ConfigGenerator{
			nodePool:              nodePool,
			controlplaneNamespace: "control-plane",
			rolloutConfig: &rolloutConfig{
				mcoRawConfig:        "original-management-content",
				rolloutMcoRawConfig: "unchanged-node-content",
				releaseImage: &releaseinfo.ReleaseImage{ImageStream: &imageapi.ImageStream{
					ObjectMeta: metav1.ObjectMeta{Name: "4.17.0"},
				}},
			},
		}
		token := &Token{ConfigGenerator: config, userData: &userData{ignitionServerEndpoint: "ignition.example.com"}}
		originalHash := token.Hash()
		nodePool.Status.Version = token.Version()
		nodePool.Annotations = map[string]string{
			nodePoolAnnotationCurrentConfigVersion: originalHash,
			nodePoolAnnotationCurrentRolloutConfig: config.RolloutHashWithoutVersion(),
		}
		originalTokenSecret := token.TokenSecret()
		originalTokenSecret.Annotations = map[string]string{nodePoolAnnotation: crclient.ObjectKeyFromObject(nodePool).String()}
		originalTokenSecret.Data = map[string][]byte{TokenSecretTokenKey: []byte("previous-token")}
		originalUserDataSecret := token.UserDataSecret()
		originalUserDataSecret.Annotations = map[string]string{nodePoolAnnotation: crclient.ObjectKeyFromObject(nodePool).String()}
		originalIgnition := ignConfig("ca", base64.StdEncoding.EncodeToString([]byte("previous-token")),
			"ignition.example.com", originalHash, &configv1.Proxy{}, nodePool)
		originalValue, err := json.Marshal(originalIgnition)
		g.Expect(err).ToNot(HaveOccurred())
		originalUserDataSecret.Data = map[string][]byte{
			"disableTemplating": []byte("dHJ1ZQ=="),
			"value":             originalValue,
		}
		machineDeployment := &capiv1.MachineDeployment{
			ObjectMeta: metav1.ObjectMeta{Name: "workers", Namespace: "control-plane"},
			Spec: capiv1.MachineDeploymentSpec{Template: capiv1.MachineTemplateSpec{
				Spec: capiv1.MachineSpec{Bootstrap: capiv1.Bootstrap{DataSecretName: ptr.To(originalUserDataSecret.Name)}},
			}},
		}
		machineSet := &capiv1.MachineSet{
			ObjectMeta: metav1.ObjectMeta{Name: "workers", Namespace: "control-plane"},
			Spec: capiv1.MachineSetSpec{Template: capiv1.MachineTemplateSpec{
				Spec: capiv1.MachineSpec{Bootstrap: capiv1.Bootstrap{DataSecretName: ptr.To(originalUserDataSecret.Name)}},
			}},
		}
		fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(
			originalTokenSecret, originalUserDataSecret, machineDeployment, machineSet,
		).Build()
		token.Client = fakeClient
		capi := &CAPI{Token: token, capiClusterName: "cluster"}
		template := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "existing-template"}}
		nodePool.Spec.Management.Replace = &hyperv1.ReplaceUpgrade{}
		g.Expect(capi.reconcileMachineDeployment(t.Context(), testr.New(t), machineDeployment, template)).To(Succeed())
		g.Expect(capi.reconcileMachineSet(t.Context(), machineSet, template)).To(Succeed())
		g.Expect(capi.reconcileMachineDeployment(t.Context(), testr.New(t), machineDeployment, template)).To(Succeed())
		g.Expect(capi.reconcileMachineSet(t.Context(), machineSet, template)).To(Succeed())
		originalDeploymentTemplate := machineDeployment.Spec.Template.DeepCopy()
		originalSetTemplate := machineSet.Spec.Template.DeepCopy()
		originalSetAnnotations := maps.Clone(machineSet.Annotations)
		originalNodePoolAnnotations := maps.Clone(nodePool.Annotations)

		rotatedSecret := &corev1.Secret{}
		g.Expect(fakeClient.Get(t.Context(), crclient.ObjectKeyFromObject(originalTokenSecret), rotatedSecret)).To(Succeed())
		rotatedSecret.Data[TokenSecretTokenKey] = []byte("current-token")
		g.Expect(fakeClient.Update(t.Context(), rotatedSecret)).To(Succeed())
		config.mcoRawConfig = "changed-management-content"
		g.Expect(token.Hash()).NotTo(Equal(originalHash))
		g.Expect(token.isOutdated()).To(BeFalse())

		g.Expect(token.Reconcile(t.Context())).To(Succeed())
		g.Expect(capi.reconcileMachineDeployment(t.Context(), testr.New(t), machineDeployment, template)).To(Succeed())
		g.Expect(capi.reconcileMachineSet(t.Context(), machineSet, template)).To(Succeed())
		g.Expect(machineDeployment.Spec.Template).To(Equal(*originalDeploymentTemplate))
		g.Expect(machineSet.Spec.Template).To(Equal(*originalSetTemplate))
		g.Expect(machineSet.Annotations).To(Equal(originalSetAnnotations))
		g.Expect(nodePool.Annotations).To(Equal(originalNodePoolAnnotations))
		currentUserData := &corev1.Secret{}
		g.Expect(fakeClient.Get(t.Context(), crclient.ObjectKeyFromObject(originalUserDataSecret), currentUserData)).To(Succeed())
		g.Expect(currentUserData.Name).To(Equal(originalUserDataSecret.Name))
		g.Expect(currentUserData.Data["disableTemplating"]).To(Equal(originalUserDataSecret.Data["disableTemplating"]))
		var currentIgnition ignitionapi.Config
		g.Expect(json.Unmarshal(currentUserData.Data["value"], &currentIgnition)).To(Succeed())
		originalIgnition.Ignition.Config.Merge[0].HTTPHeaders[0].Value = ptr.To(
			"Bearer " + base64.StdEncoding.EncodeToString(rotatedSecret.Data[TokenSecretTokenKey]))
		g.Expect(currentIgnition).To(Equal(originalIgnition))
		resourceVersion := currentUserData.ResourceVersion
		g.Expect(token.Reconcile(t.Context())).To(Succeed())
		g.Expect(fakeClient.Get(t.Context(), crclient.ObjectKeyFromObject(originalUserDataSecret), currentUserData)).To(Succeed())
		g.Expect(currentUserData.ResourceVersion).To(Equal(resourceVersion))

		secrets := &corev1.SecretList{}
		g.Expect(fakeClient.List(t.Context(), secrets, crclient.InNamespace("control-plane"))).To(Succeed())
		g.Expect(secrets.Items).To(HaveLen(2))
		for _, resource := range []crclient.Object{machineDeployment, machineSet} {
			g.Expect(fakeClient.Get(t.Context(), crclient.ObjectKeyFromObject(resource), resource)).To(Succeed())
		}
		g.Expect(machineDeployment.Spec.Template.Spec.Bootstrap.DataSecretName).To(Equal(ptr.To(originalUserDataSecret.Name)))
		g.Expect(machineSet.Spec.Template.Spec.Bootstrap.DataSecretName).To(Equal(ptr.To(originalUserDataSecret.Name)))
		nodePool.Spec.Platform.Type = hyperv1.NonePlatform
		for range 2 {
			secretsCreated := token.isOutdated()
			g.Expect(secretsCreated).To(BeFalse())
			g.Expect(token.Reconcile(t.Context())).To(Succeed())
			reconcileNonAutomatedNodePool(t.Context(), nodePool, token, secretsCreated)
			g.Expect(nodePool.Annotations[nodePoolAnnotationCurrentConfigVersion]).To(Equal(originalHash))
		}
	})

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
		seedRolloutOnly bool
	}{
		{
			name:            "When the rollout annotation exists but the current config version is empty, it should create token and user data Secrets",
			seedRolloutOnly: true,
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

			fakeClient := fake.NewClientBuilder().WithObjects(tc.fakeObjects...).Build()
			tc.configGenerator.Client = fakeClient

			token, err := NewToken(t.Context(), tc.configGenerator, tc.cpoCapabilities)
			g.Expect(err).To(Not(HaveOccurred()))
			if tc.seedRolloutOnly {
				tc.configGenerator.nodePool.Annotations = map[string]string{
					nodePoolAnnotationCurrentConfigVersion: "",
					nodePoolAnnotationCurrentRolloutConfig: token.RolloutHashWithoutVersion(),
				}
				g.Expect(token.isOutdated()).To(BeTrue(), "missing config version requires initial Secret creation")
			}

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

func TestRefreshUserDataAuthorization(t *testing.T) {
	t.Run("When the Authorization token matches, it should preserve the opaque Ignition bytes", func(t *testing.T) {
		g := NewWithT(t)
		tokenBytes := []byte("current-token")
		original := []byte(`{ "ignition": {"config":{"merge":[{"source":"https://ignition.example.com/ignition","httpHeaders":[{"name":"Authorization","value":"Bearer ` + base64.StdEncoding.EncodeToString(tokenBytes) + `"}],"unknown":{"keep":true}}]}},"extra": [1,  2] }`)
		updated, changed, err := refreshUserDataAuthorization(original, tokenBytes, "https://ignition.example.com/ignition")
		g.Expect(err).ToNot(HaveOccurred(), "matching Authorization should be accepted")
		g.Expect(changed).To(BeFalse(), "matching token should not rewrite Ignition")
		g.Expect(updated).To(Equal(original), "opaque Ignition bytes should remain untouched")
	})
	t.Run("When merge sources and unknown Ignition members exist, it should update only Authorization headers", func(t *testing.T) {
		g := NewWithT(t)
		original := []byte(`{"unrecognized":{"keep":true},"ignition":{"unknown":"retained","config":{"merge":[{"source":"https://other.example","httpHeaders":[{"name":"Accept","value":"text/plain"},{"name":"Authorization","value":"Bearer external"}],"extra":1},{"source":"https://ignition.example.com/ignition","httpHeaders":[{"name":"Authorization","value":"Bearer old","extra":"retained"}]}],"unknownConfig":[1,2]}},"storage":{"files":[{"path":"/etc/example"}]}}`)
		updated, changed, err := refreshUserDataAuthorization(original, []byte("new-token"), "https://ignition.example.com/ignition")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(changed).To(BeTrue())
		g.Expect(string(updated)).To(ContainSubstring(`"unknownConfig":[1,2]`))
		g.Expect(string(updated)).To(ContainSubstring(`"extra":"retained"`))
		g.Expect(string(updated)).To(ContainSubstring(`"storage":{"files":[{"path":"/etc/example"}]}`))
		g.Expect(string(updated)).To(ContainSubstring(`"value":"Bearer ` + base64.StdEncoding.EncodeToString([]byte("new-token")) + `"`))
		g.Expect(string(updated)).To(ContainSubstring(`"value":"text/plain"`))
		g.Expect(string(updated)).To(ContainSubstring(`"value":"Bearer external"`), "unrelated source Authorization must remain unchanged")
		unchanged, changed, err := refreshUserDataAuthorization(updated, []byte("new-token"), "https://ignition.example.com/ignition")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(changed).To(BeFalse())
		g.Expect(unchanged).To(Equal(updated))
	})
	t.Run("When no Authorization header exists, it should fail without returning token contents", func(t *testing.T) {
		g := NewWithT(t)
		_, _, err := refreshUserDataAuthorization([]byte(`{"ignition":{"config":{"merge":[{"source":"https://ignition.example.com/ignition","httpHeaders":[{"name":"Accept"}]}]}}}`), []byte("private-token"), "https://ignition.example.com/ignition")
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).NotTo(ContainSubstring("private-token"))
		g.Expect(strings.Contains(err.Error(), "Authorization")).To(BeTrue())
	})
	t.Run("When only another source has Authorization, it should not send the token to that source", func(t *testing.T) {
		g := NewWithT(t)
		original := []byte(`{"ignition":{"config":{"merge":[{"source":"https://other.example/ignition","httpHeaders":[{"name":"Authorization","value":"Bearer external"}]}]}}}`)
		updated, changed, err := refreshUserDataAuthorization(original, []byte("private-token"), "https://ignition.example.com/ignition")
		g.Expect(err).To(HaveOccurred(), "the intended Ignition source is missing")
		g.Expect(err.Error()).NotTo(ContainSubstring("private-token"))
		g.Expect(changed).To(BeFalse())
		g.Expect(updated).To(BeNil(), "the other source must not be overwritten")
	})
}

func TestReconcileCurrentUserData(t *testing.T) {
	testCases := []struct {
		name            string
		currentHash     string
		includeToken    bool
		includeUserData bool
		tokenData       map[string][]byte
		userDataValue   []byte
	}{
		{
			name: "When the current config version is missing, it should not create a new Secret",
		},
		{
			name:        "When the current token Secret is missing, it should report an error without creating one",
			currentHash: "current-hash",
		},
		{
			name:         "When the active token is empty, it should not update user data",
			currentHash:  "current-hash",
			includeToken: true,
		},
		{
			name:         "When the current user data Secret is missing, it should not create a new Secret",
			currentHash:  "current-hash",
			includeToken: true,
			tokenData:    map[string][]byte{TokenSecretTokenKey: []byte("current-token")},
		},
		{
			name:            "When the user data is malformed, it should report an error without overwriting it",
			currentHash:     "current-hash",
			includeToken:    true,
			includeUserData: true,
			tokenData:       map[string][]byte{TokenSecretTokenKey: []byte("current-token")},
			userDataValue:   []byte("not-json"),
		},
		{
			name:            "When the Authorization header is missing, it should preserve the user data",
			currentHash:     "current-hash",
			includeToken:    true,
			includeUserData: true,
			tokenData:       map[string][]byte{TokenSecretTokenKey: []byte("current-token")},
			userDataValue:   []byte(`{"ignition":{"config":{"merge":[{"source":"https://ignition.example.com/ignition"}]}}}`),
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			g := NewWithT(t)
			nodePool := &hyperv1.NodePool{ObjectMeta: metav1.ObjectMeta{
				Name: "workers", Namespace: "clusters",
				Annotations: map[string]string{nodePoolAnnotationCurrentConfigVersion: testCase.currentHash},
			}}
			token := &Token{ConfigGenerator: &ConfigGenerator{nodePool: nodePool, controlplaneNamespace: "control-plane"}, userData: &userData{ignitionServerEndpoint: "ignition.example.com"}}
			var objects []crclient.Object
			if testCase.includeToken {
				secret := token.outdatedTokenSecret()
				secret.Data = testCase.tokenData
				objects = append(objects, secret)
			}
			if testCase.includeUserData {
				secret := token.outdatedUserDataSecret()
				secret.Data = map[string][]byte{"value": testCase.userDataValue}
				objects = append(objects, secret)
			}
			fakeClient := fake.NewClientBuilder().WithObjects(objects...).Build()
			token.Client = fakeClient
			g.Expect(token.reconcileCurrentUserData(t.Context())).ToNot(Succeed())
			secrets := &corev1.SecretList{}
			g.Expect(fakeClient.List(t.Context(), secrets)).To(Succeed())
			g.Expect(secrets.Items).To(HaveLen(len(objects)))
			if testCase.includeUserData {
				secret := &corev1.Secret{}
				g.Expect(fakeClient.Get(t.Context(), crclient.ObjectKeyFromObject(token.outdatedUserDataSecret()), secret)).To(Succeed())
				g.Expect(secret.Data["value"]).To(Equal(testCase.userDataValue))
			}
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
			fakeClient := fake.NewClientBuilder().WithObjects(tc.secret).Build()
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
