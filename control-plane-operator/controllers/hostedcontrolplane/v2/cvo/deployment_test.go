package cvo

import (
	"context"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	"github.com/openshift/hypershift/support/api"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"
	"github.com/openshift/hypershift/support/util/fakeimagemetadataprovider"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestPreparePayloadScript(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		platformType hyperv1.PlatformType
		oauthEnabled bool
		featureSet   configv1.FeatureSet
		assertions   func(g Gomega, script string)
	}{
		{
			name:         "When platform is AWS and oauth is enabled, it should not remove the oauth console manifest",
			platformType: hyperv1.AWSPlatform,
			oauthEnabled: true,
			featureSet:   configv1.Default,
			assertions: func(g Gomega, script string) {
				g.Expect(script).NotTo(ContainSubstring("0000_50_console-operator_01-oauth.yaml"))
			},
		},
		{
			name:         "When platform is AWS and oauth is disabled, it should contain rm command for oauth manifest",
			platformType: hyperv1.AWSPlatform,
			oauthEnabled: false,
			featureSet:   configv1.Default,
			assertions: func(g Gomega, script string) {
				g.Expect(script).To(ContainSubstring("rm -f /var/payload/release-manifests/0000_50_console-operator_01-oauth.yaml"))
			},
		},
		{
			name:         "When platform is IBMCloud, it should NOT remove the ibm-cloud-managed storage operator deployment manifest",
			platformType: hyperv1.IBMCloudPlatform,
			oauthEnabled: true,
			featureSet:   configv1.Default,
			assertions: func(g Gomega, script string) {
				g.Expect(script).NotTo(ContainSubstring("rm -f /var/payload/release-manifests/0000_50_cluster-storage-operator_10_deployment-ibm-cloud-managed.yaml"))
				g.Expect(script).NotTo(ContainSubstring("rm -f /var/payload/release-manifests/0000_50_cluster-csi-snapshot-controller-operator_07_deployment-ibm-cloud-managed.yaml"))
			},
		},
		{
			name:         "When platform is PowerVS, it should NOT remove the ibm-cloud-managed storage operator deployment manifest",
			platformType: hyperv1.PowerVSPlatform,
			oauthEnabled: true,
			featureSet:   configv1.Default,
			assertions: func(g Gomega, script string) {
				g.Expect(script).NotTo(ContainSubstring("rm -f /var/payload/release-manifests/0000_50_cluster-storage-operator_10_deployment-ibm-cloud-managed.yaml"))
				g.Expect(script).NotTo(ContainSubstring("rm -f /var/payload/release-manifests/0000_50_cluster-csi-snapshot-controller-operator_07_deployment-ibm-cloud-managed.yaml"))
			},
		},
		{
			name:         "When platform is AWS (default), it should remove the ibm-cloud-managed storage operator deployment manifest",
			platformType: hyperv1.AWSPlatform,
			oauthEnabled: true,
			featureSet:   configv1.Default,
			assertions: func(g Gomega, script string) {
				g.Expect(script).To(ContainSubstring("rm -f /var/payload/release-manifests/0000_50_cluster-storage-operator_10_deployment-ibm-cloud-managed.yaml"))
				g.Expect(script).To(ContainSubstring("rm -f /var/payload/release-manifests/0000_50_cluster-csi-snapshot-controller-operator_07_deployment-ibm-cloud-managed.yaml"))
			},
		},
		{
			// NOTE: configv1.Default is "", but adaptDeployment normalizes it to "Default"
			// before calling preparePayloadScript. We pass the normalized value here.
			name:         "When featureSet is Default, it should include Default in the feature-set filter script",
			platformType: hyperv1.AWSPlatform,
			oauthEnabled: true,
			featureSet:   configv1.FeatureSet("Default"),
			assertions: func(g Gomega, script string) {
				g.Expect(script).To(ContainSubstring(`=~ "Default"`))
			},
		},
		{
			name:         "When featureSet is TechPreviewNoUpgrade, it should include TechPreviewNoUpgrade in the feature-set filter script",
			platformType: hyperv1.AWSPlatform,
			oauthEnabled: true,
			featureSet:   configv1.TechPreviewNoUpgrade,
			assertions: func(g Gomega, script string) {
				g.Expect(script).To(ContainSubstring(`=~ "TechPreviewNoUpgrade"`))
			},
		},
		{
			name:         "When called, it should always start with cp -R /manifests to /var/payload/",
			platformType: hyperv1.AWSPlatform,
			oauthEnabled: true,
			featureSet:   configv1.Default,
			assertions: func(g Gomega, script string) {
				g.Expect(strings.HasPrefix(script, "cp -R /manifests /var/payload/")).To(BeTrue(),
					"script should start with 'cp -R /manifests /var/payload/'")
			},
		},
		{
			name:         "When called, it should always contain the cleanup yaml generation (0000_01_cleanup.yaml)",
			platformType: hyperv1.AWSPlatform,
			oauthEnabled: true,
			featureSet:   configv1.Default,
			assertions: func(g Gomega, script string) {
				g.Expect(script).To(ContainSubstring("0000_01_cleanup.yaml"))
			},
		},
		{
			name:         "When called, it should omit the packageserver PDB manifest from the payload",
			platformType: hyperv1.AWSPlatform,
			oauthEnabled: true,
			featureSet:   configv1.Default,
			assertions: func(g Gomega, script string) {
				g.Expect(script).To(ContainSubstring("rm -f /var/payload/release-manifests/0000_50_olm_00-packageserver.pdb.yaml"))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			script := preparePayloadScript(tt.platformType, tt.oauthEnabled, tt.featureSet)
			tt.assertions(g, script)
		})
	}
}

func TestResourcesToRemove(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		platformType hyperv1.PlatformType
		assertions   func(g Gomega, resources []string)
	}{
		{
			name:         "When platform is IBMCloud, it should return IBM-specific resources without CRDs",
			platformType: hyperv1.IBMCloudPlatform,
			assertions: func(g Gomega, resources []string) {
				g.Expect(resources).To(ContainElement("packageserver-pdb"))
				g.Expect(resources).To(ContainElement("network-operator"))
				g.Expect(resources).To(ContainElement("default-account-cluster-network-operator"))
				g.Expect(resources).To(ContainElement("cluster-node-tuning-operator"))
				g.Expect(resources).To(ContainElement("cluster-image-registry-operator"))
				g.Expect(resources).NotTo(ContainElement("machineconfigs.machineconfiguration.openshift.io"))
				g.Expect(resources).NotTo(ContainElement("machineconfigpools.machineconfiguration.openshift.io"))
			},
		},
		{
			name:         "When platform is PowerVS, it should return the same resources as IBMCloud",
			platformType: hyperv1.PowerVSPlatform,
			assertions: func(g Gomega, resources []string) {
				g.Expect(resources).To(ContainElement("network-operator"))
				g.Expect(resources).To(ContainElement("default-account-cluster-network-operator"))
				g.Expect(resources).To(ContainElement("cluster-node-tuning-operator"))
				g.Expect(resources).To(ContainElement("cluster-image-registry-operator"))
				g.Expect(resources).NotTo(ContainElement("machineconfigs.machineconfiguration.openshift.io"))
			},
		},
		{
			name:         "When platform is AWS (default), it should return the full list including CRDs and storage operators",
			platformType: hyperv1.AWSPlatform,
			assertions: func(g Gomega, resources []string) {
				g.Expect(resources).To(ContainElement("packageserver-pdb"))
				g.Expect(resources).To(ContainElement("machineconfigs.machineconfiguration.openshift.io"))
				g.Expect(resources).To(ContainElement("machineconfigpools.machineconfiguration.openshift.io"))
				g.Expect(resources).To(ContainElement("network-operator"))
				g.Expect(resources).To(ContainElement("cluster-storage-operator"))
				g.Expect(resources).To(ContainElement("csi-snapshot-controller-operator"))
				g.Expect(resources).To(ContainElement("aws-ebs-csi-driver-operator"))
				g.Expect(resources).To(ContainElement("aws-ebs-csi-driver-controller"))
				g.Expect(resources).To(ContainElement("csi-snapshot-controller"))
			},
		},
		{
			name:         "When platform is IBMCloud, it should return fewer resources than the default platform",
			platformType: hyperv1.IBMCloudPlatform,
			assertions: func(g Gomega, resources []string) {
				defaultResources := extractResourceNames(resourcesToRemove(hyperv1.AWSPlatform))
				g.Expect(len(resources)).To(BeNumerically("<", len(defaultResources)),
					"IBMCloud should have fewer resources to remove than the default platform")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			objects := resourcesToRemove(tt.platformType)
			names := extractResourceNames(objects)
			tt.assertions(g, names)
		})
	}
}

func extractResourceNames(objects []client.Object) []string {
	names := make([]string, 0, len(objects))
	for _, obj := range objects {
		names = append(names, obj.GetName())
	}
	return names
}

func createTestContext(hcp *hyperv1.HostedControlPlane) component.WorkloadContext {
	pullSecret := common.PullSecret(hcp.Namespace)
	pullSecret.Data = map[string][]byte{
		corev1.DockerConfigJsonKey: []byte(`{"auths":{"test.registry":{"auth":"dGVzdDp0ZXN0"}}}`),
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(api.Scheme).
		WithObjects(pullSecret).
		Build()

	fakeImageProvider := &fakeimagemetadataprovider.FakeRegistryClientImageMetadataProvider{
		Digest: "sha256:1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
	}

	if hcp.Spec.ReleaseImage == "" {
		hcp.Spec.ReleaseImage = "quay.io/openshift-release-dev/ocp-release:4.18.0-x86_64"
	}

	return component.WorkloadContext{
		Context:               context.Background(),
		Client:                fakeClient,
		HCP:                   hcp,
		ImageMetadataProvider: fakeImageProvider,
	}
}

func TestAdaptDeployment(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name                  string
		hcp                   *hyperv1.HostedControlPlane
		expectedTLSMinVersion string
		expectedCipherSuites  string
	}{
		{
			name: "When TLS profile has empty cipher suites list, it should not add tls-cipher-suites flag",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Name: "test-hcp", Namespace: "test-ns"},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform},
					Configuration: &hyperv1.ClusterConfiguration{
						APIServer: &configv1.APIServerSpec{
							TLSSecurityProfile: &configv1.TLSSecurityProfile{
								Type: configv1.TLSProfileCustomType,
								Custom: &configv1.CustomTLSProfile{
									TLSProfileSpec: configv1.TLSProfileSpec{
										Ciphers:       []string{},
										MinTLSVersion: configv1.VersionTLS12,
									},
								},
							},
						},
					},
				},
			},
			expectedTLSMinVersion: "--tls-min-version=VersionTLS12",
			expectedCipherSuites:  "",
		},
		{
			name: "When TLS profile has empty MinTLSVersion, it should not add tls-min-version flag",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Name: "test-hcp", Namespace: "test-ns"},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform},
					Configuration: &hyperv1.ClusterConfiguration{
						APIServer: &configv1.APIServerSpec{
							TLSSecurityProfile: &configv1.TLSSecurityProfile{
								Type: configv1.TLSProfileCustomType,
								Custom: &configv1.CustomTLSProfile{
									TLSProfileSpec: configv1.TLSProfileSpec{
										Ciphers:       []string{"ECDHE-RSA-AES128-GCM-SHA256"},
										MinTLSVersion: "",
									},
								},
							},
						},
					},
				},
			},
			expectedTLSMinVersion: "",
			expectedCipherSuites:  "--tls-cipher-suites=TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
		},
		{
			name: "When TLS profile has both fields empty, it should not add any TLS flags",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Name: "test-hcp", Namespace: "test-ns"},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform},
					Configuration: &hyperv1.ClusterConfiguration{
						APIServer: &configv1.APIServerSpec{
							TLSSecurityProfile: &configv1.TLSSecurityProfile{
								Type: configv1.TLSProfileCustomType,
								Custom: &configv1.CustomTLSProfile{
									TLSProfileSpec: configv1.TLSProfileSpec{
										Ciphers:       []string{},
										MinTLSVersion: "",
									},
								},
							},
						},
					},
				},
			},
			expectedTLSMinVersion: "",
			expectedCipherSuites:  "",
		},
		{
			name: "When TLS profile has both fields set and a single cipher suite, it should add both flags and a single cipher",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Name: "test-hcp", Namespace: "test-ns"},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform},
					Configuration: &hyperv1.ClusterConfiguration{
						APIServer: &configv1.APIServerSpec{
							TLSSecurityProfile: &configv1.TLSSecurityProfile{
								Type: configv1.TLSProfileCustomType,
								Custom: &configv1.CustomTLSProfile{
									TLSProfileSpec: configv1.TLSProfileSpec{
										Ciphers:       []string{"ECDHE-RSA-AES128-GCM-SHA256"},
										MinTLSVersion: configv1.VersionTLS12,
									},
								},
							},
						},
					},
				},
			},
			expectedTLSMinVersion: "--tls-min-version=VersionTLS12",
			expectedCipherSuites:  "--tls-cipher-suites=TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
		},
		{
			name: "When TLS profile has both fields set and multiple cipher suites, it should add both flags and a comma-separated cipher list",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Name: "test-hcp", Namespace: "test-ns"},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform},
					Configuration: &hyperv1.ClusterConfiguration{
						APIServer: &configv1.APIServerSpec{
							TLSSecurityProfile: &configv1.TLSSecurityProfile{
								Type: configv1.TLSProfileCustomType,
								Custom: &configv1.CustomTLSProfile{
									TLSProfileSpec: configv1.TLSProfileSpec{
										Ciphers: []string{
											"ECDHE-ECDSA-AES128-GCM-SHA256",
											"ECDHE-RSA-AES256-GCM-SHA384",
										},
										MinTLSVersion: configv1.VersionTLS13,
									},
								},
							},
						},
					},
				},
			},
			expectedTLSMinVersion: "--tls-min-version=VersionTLS13",
			expectedCipherSuites:  "--tls-cipher-suites=TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384",
		},
		{
			name: "When TLS profile type is Modern, it should set min version to TLS 1.3 and not add cipher suites flag",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Name: "test-hcp", Namespace: "test-ns"},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform},
					Configuration: &hyperv1.ClusterConfiguration{
						APIServer: &configv1.APIServerSpec{
							TLSSecurityProfile: &configv1.TLSSecurityProfile{
								Type:   configv1.TLSProfileModernType,
								Modern: &configv1.ModernTLSProfile{},
							},
						},
					},
				},
			},
			expectedTLSMinVersion: "--tls-min-version=VersionTLS13",
			expectedCipherSuites:  "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			deployment, err := assets.LoadDeploymentManifest(ComponentName)
			g.Expect(err).ToNot(HaveOccurred())

			cpContext := createTestContext(tc.hcp)

			cvo := &clusterVersionOperator{}
			err = cvo.adaptDeployment(cpContext, deployment)
			g.Expect(err).ToNot(HaveOccurred())

			container := podspec.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
			g.Expect(container).ToNot(BeNil())

			if tc.expectedTLSMinVersion != "" {
				g.Expect(container.Args).To(ContainElement(tc.expectedTLSMinVersion))
			} else {
				g.Expect(container.Args).ToNot(ContainElement(ContainSubstring("--tls-min-version")))
			}

			if tc.expectedCipherSuites != "" {
				g.Expect(container.Args).To(ContainElement(tc.expectedCipherSuites))
			} else {
				g.Expect(container.Args).ToNot(ContainElement(ContainSubstring("--tls-cipher-suites")))
			}
		})
	}
}
