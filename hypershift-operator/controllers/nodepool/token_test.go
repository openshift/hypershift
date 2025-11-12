package nodepool

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/ignitionserver"
	"github.com/openshift/hypershift/hypershift-operator/controllers/nodepool/kubevirt"
	"github.com/openshift/hypershift/support/globalconfig"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/testutil"
	supportutil "github.com/openshift/hypershift/support/util"

	configv1 "github.com/openshift/api/config/v1"
	imageapi "github.com/openshift/api/image/v1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	testingclock "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	kubevirtv1 "kubevirt.io/api/core/v1"
	capikubevirt "sigs.k8s.io/cluster-api-provider-kubevirt/api/v1alpha1"

	ignitionapi "github.com/coreos/ignition/v2/config/v3_2/types"
	"github.com/google/uuid"
)

// MockIgnitionProvider is a mock implementation of IgnitionProvider for testing
type MockIgnitionProvider struct {
	PayloadResponse []byte
	ErrorResponse   error
}

func (m *MockIgnitionProvider) GetPayload(ctx context.Context, payloadImage, config, pullSecretHash, additionalTrustBundleHash, hcConfigurationHash string) ([]byte, error) {
	return m.PayloadResponse, m.ErrorResponse
}

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
			name: "When userdata and token secret are outdated userdata secret should be deleted and token secret should get and expiration timestamp",
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
				},
			},
			fakeObjects: []crclient.Object{
				tokenSecretWithTimestamp,
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

			// user data secret should be deleted.
			got := &corev1.Secret{}
			err = fakeClient.Get(t.Context(), crclient.ObjectKeyFromObject(userdataSecret), got)
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue())

			// token secret if exists it should be have an expiration time.
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
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			fakeClient := fake.NewClientBuilder().WithObjects(tc.fakeObjects...).Build()
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

func TestSecretBasedIgnitionPayload(t *testing.T) {
	t.Run("Secret-based ignition for KubeVirt should fetch complete payload", func(t *testing.T) {
		g := NewWithT(t)

		// Create test data
		hcName := "test-hc"
		hcNamespace := "namespace"
		controlplaneNamespace := "controlplane-namespace"
		expectedPayload := []byte(`{"ignition":{"config":{"merge":[{"source":"complete-payload"}]},"version":"3.2.0"}}`)

		// Mock ignition provider
		mockProvider := &MockIgnitionProvider{
			PayloadResponse: expectedPayload,
			ErrorResponse:   nil,
		}

		pullSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pull-secret",
				Namespace: hcNamespace,
			},
			Data: map[string][]byte{
				corev1.DockerConfigJsonKey: []byte(`{"auths":{"example.com":{"auth":"dGVzdDp0ZXN0"}}}`),
			},
		}

		ignitionCACert := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      ignitionserver.IgnitionCACertSecret(controlplaneNamespace).Name,
				Namespace: controlplaneNamespace,
			},
			Data: map[string][]byte{
				corev1.TLSCertKey: []byte("test-ignition-ca-cert"),
			},
		}

		// Create KubeVirt NodePool with secret-based ignition enabled
		nodePool := &hyperv1.NodePool{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-nodepool",
				Namespace: hcNamespace,
				Annotations: map[string]string{
					"hypershift.openshift.io/kubevirt-secret-based-ignition": "true",
				},
			},
			Spec: hyperv1.NodePoolSpec{
				ClusterName: hcName,
				Platform: hyperv1.NodePoolPlatform{
					Type: hyperv1.KubevirtPlatform,
				},
				Release: hyperv1.Release{
					Image: "test-release-image:latest",
				},
			},
		}

		hostedCluster := &hyperv1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      hcName,
				Namespace: hcNamespace,
			},
			Spec: hyperv1.HostedClusterSpec{
				PullSecret: corev1.LocalObjectReference{
					Name: pullSecret.Name,
				},
			},
			Status: hyperv1.HostedClusterStatus{
				IgnitionEndpoint: "ignition.example.com",
			},
		}

		releaseImage := &releaseinfo.ReleaseImage{
			ImageStream: &imageapi.ImageStream{
				ObjectMeta: metav1.ObjectMeta{
					Name: "4.17",
				},
			},
		}

		configGenerator := &ConfigGenerator{
			hostedCluster:         hostedCluster,
			nodePool:              nodePool,
			controlplaneNamespace: controlplaneNamespace,
			rolloutConfig: &rolloutConfig{
				releaseImage:              releaseImage,
				pullSecretName:            "test-pull-secret",
				additionalTrustBundleName: "test-trust-bundle",
				globalConfig:              "test-global-config",
				mcoRawConfig:              "test-mco-raw-config",
			},
		}

		cpoCapabilities := &CPOCapabilities{
			CreateDefaultAWSSecurityGroup: true,
		}

		fakeClient := fake.NewClientBuilder().WithObjects(pullSecret, ignitionCACert).Build()
		configGenerator.Client = fakeClient

		ctx := context.Background()

		// Test NewTokenWithIgnitionProvider with secret-based ignition
		token, err := NewTokenWithIgnitionProvider(ctx, configGenerator, cpoCapabilities, mockProvider)
		g.Expect(err).To(Not(HaveOccurred()))
		g.Expect(token).To(Not(BeNil()))

		// Verify that the complete ignition payload was set
		g.Expect(token.userData.completeIgnitionPayload).To(Equal(expectedPayload))

		// Get user data secret directly to check the content
		userDataSecret := token.UserDataSecret()
		g.Expect(userDataSecret).To(Not(BeNil()))

		// The secret should be generated with complete payload instead of URL-based config
		err = token.reconcileUserDataSecret(userDataSecret, "test-token")
		g.Expect(err).To(Not(HaveOccurred()))

		// Verify user data secret was created with complete payload
		g.Expect(userDataSecret.Data).To(Not(BeNil()))
		g.Expect(userDataSecret.Data["value"]).To(Equal(expectedPayload))
	})

	t.Run("Cloud-init volume should be replaced with configdrive when secret-based ignition is enabled", func(t *testing.T) {
		g := NewWithT(t)

		// Create a mock VM template with existing CloudInitNoCloud volume
		existingTemplate := &capikubevirt.VirtualMachineTemplateSpec{
			Spec: kubevirtv1.VirtualMachineSpec{
				Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
					Spec: kubevirtv1.VirtualMachineInstanceSpec{
						Domain: kubevirtv1.DomainSpec{
							Devices: kubevirtv1.Devices{
								Disks: []kubevirtv1.Disk{
									{
										Name: "cloudinitvolume",
										DiskDevice: kubevirtv1.DiskDevice{
											Disk: &kubevirtv1.DiskTarget{Bus: "virtio"},
										},
									},
								},
							},
						},
						Volumes: []kubevirtv1.Volume{
							{
								Name: "cloudinitvolume",
								VolumeSource: kubevirtv1.VolumeSource{
									CloudInitNoCloud: &kubevirtv1.CloudInitNoCloudSource{
										UserDataSecretRef: &corev1.LocalObjectReference{
											Name: "existing-userdata-secret",
										},
									},
								},
							},
						},
					},
				},
			},
		}

		nodePool := &hyperv1.NodePool{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-nodepool",
				Annotations: map[string]string{
					"hypershift.openshift.io/kubevirt-secret-based-ignition": "true",
				},
			},
			Spec: hyperv1.NodePoolSpec{
				Platform: hyperv1.NodePoolPlatform{
					Type: hyperv1.KubevirtPlatform,
				},
			},
		}

		// Apply the replacement logic
		err := kubevirt.ReplaceCloudInitWithConfigDrive(existingTemplate, nodePool)
		g.Expect(err).To(Not(HaveOccurred()))

		// Verify that CloudInitNoCloud was replaced with CloudInitConfigDrive
		volumes := existingTemplate.Spec.Template.Spec.Volumes
		g.Expect(volumes).To(HaveLen(1))

		volume := volumes[0]
		g.Expect(volume.Name).To(Equal("cloudinitvolume"))
		g.Expect(volume.CloudInitNoCloud).To(BeNil())          // Should be removed
		g.Expect(volume.CloudInitConfigDrive).To(Not(BeNil())) // Should be added
		g.Expect(volume.CloudInitConfigDrive.UserDataSecretRef.Name).To(Equal("user-data-secret-placeholder"))

		// Verify disk is still present with same name
		disks := existingTemplate.Spec.Template.Spec.Domain.Devices.Disks
		g.Expect(disks).To(HaveLen(1))
		g.Expect(disks[0].Name).To(Equal("cloudinitvolume"))
		g.Expect(string(disks[0].DiskDevice.Disk.Bus)).To(Equal("virtio"))
	})

	t.Run("No new volume should be created when no existing cloud-init volume is found", func(t *testing.T) {
		g := NewWithT(t)

		// Create a VM template with NO existing cloud-init volume
		templateWithoutCloudInit := &capikubevirt.VirtualMachineTemplateSpec{
			Spec: kubevirtv1.VirtualMachineSpec{
				Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
					Spec: kubevirtv1.VirtualMachineInstanceSpec{
						Domain: kubevirtv1.DomainSpec{
							Devices: kubevirtv1.Devices{
								Disks: []kubevirtv1.Disk{
									{
										Name: "rhcos",
										DiskDevice: kubevirtv1.DiskDevice{
											Disk: &kubevirtv1.DiskTarget{Bus: "virtio"},
										},
									},
								},
							},
						},
						Volumes: []kubevirtv1.Volume{
							{
								Name: "rhcos",
								VolumeSource: kubevirtv1.VolumeSource{
									DataVolume: &kubevirtv1.DataVolumeSource{
										Name: "rhcos",
									},
								},
							},
						},
					},
				},
			},
		}

		nodePool := &hyperv1.NodePool{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-nodepool",
				Annotations: map[string]string{
					"hypershift.openshift.io/kubevirt-secret-based-ignition": "true",
				},
			},
			Spec: hyperv1.NodePoolSpec{
				Platform: hyperv1.NodePoolPlatform{
					Type: hyperv1.KubevirtPlatform,
				},
			},
		}

		// Apply the replacement logic
		err := kubevirt.ReplaceCloudInitWithConfigDrive(templateWithoutCloudInit, nodePool)
		g.Expect(err).To(Not(HaveOccurred()))

		// Verify that NO new volumes or disks were added (should still have only 1 volume and 1 disk)
		volumes := templateWithoutCloudInit.Spec.Template.Spec.Volumes
		g.Expect(volumes).To(HaveLen(1)) // Still only the rhcos volume
		g.Expect(volumes[0].Name).To(Equal("rhcos"))

		disks := templateWithoutCloudInit.Spec.Template.Spec.Domain.Devices.Disks
		g.Expect(disks).To(HaveLen(1)) // Still only the rhcos disk
		g.Expect(disks[0].Name).To(Equal("rhcos"))
	})
}
