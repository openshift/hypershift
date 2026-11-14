package cvo

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/omnicpo"
	"github.com/openshift/hypershift/support/podspec"
	"github.com/openshift/hypershift/support/testutil"
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
				g.Expect(script).NotTo(ContainSubstring("rm -f /var/payload/release-manifests/0000_50_cluster-kube-storage-version-migrator-operator_07_deployment-ibm-cloud-managed.yaml"))
				g.Expect(script).NotTo(ContainSubstring("rm -f /var/payload/release-manifests/0000_50_cluster-kube-storage-version-migrator-operator_09_clusteroperator.yaml"))
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
				g.Expect(script).NotTo(ContainSubstring("rm -f /var/payload/release-manifests/0000_50_cluster-kube-storage-version-migrator-operator_07_deployment-ibm-cloud-managed.yaml"))
				g.Expect(script).NotTo(ContainSubstring("rm -f /var/payload/release-manifests/0000_50_cluster-kube-storage-version-migrator-operator_09_clusteroperator.yaml"))
			},
		},
		{
			name:         "When platform is AWS (default), it should remove the kube-storage-version-migrator-operator manifests",
			platformType: hyperv1.AWSPlatform,
			oauthEnabled: true,
			featureSet:   configv1.Default,
			assertions: func(g Gomega, script string) {
				g.Expect(script).To(ContainSubstring("rm -f /var/payload/release-manifests/0000_50_cluster-storage-operator_10_deployment-ibm-cloud-managed.yaml"))
				g.Expect(script).To(ContainSubstring("rm -f /var/payload/release-manifests/0000_50_cluster-csi-snapshot-controller-operator_07_deployment-ibm-cloud-managed.yaml"))
				g.Expect(script).To(ContainSubstring("rm -f /var/payload/release-manifests/0000_50_cluster-kube-storage-version-migrator-operator_07_deployment-ibm-cloud-managed.yaml"))
				g.Expect(script).To(ContainSubstring("rm -f /var/payload/release-manifests/0000_50_cluster-kube-storage-version-migrator-operator_09_clusteroperator.yaml"))
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
		{
			// Payloads built after openshift/api#3155 ship the CVO CRDs
			// (clusterversions, clusteroperators, clusterversionoperators) from the
			// openshift/api image via /release-manifests only, while
			// cluster-version-operator#1507 removes them from the CVO image and
			// therefore from /manifests. The bootstrap init container applies CRDs
			// from /var/payload/manifests only, so they must be copied over. The
			// guards keep this a no-op for older payloads that already ship the
			// CRDs in /manifests, and tolerate payloads that ship them in neither
			// directory.
			name:         "When CVO CRDs are missing from manifests, it should copy them from release-manifests",
			platformType: hyperv1.AWSPlatform,
			oauthEnabled: true,
			featureSet:   configv1.Default,
			assertions: func(g Gomega, script string) {
				g.Expect(script).To(ContainSubstring("for crd in /var/payload/release-manifests/0000_00_cluster-version-operator_01_*.crd.yaml; do"))
				g.Expect(script).To(ContainSubstring(`if [[ -f "$crd" ]] && [[ ! -f "/var/payload/manifests/${crd##*/}" ]]; then`))
				g.Expect(script).To(ContainSubstring(`cp "$crd" /var/payload/manifests/`))
			},
		},
		{
			// The copy must happen after /release-manifests is staged into
			// /var/payload and before the feature-set filter, so the filter prunes
			// the copied clusterversions CRD variants (keeping the one matching the
			// requested feature set) exactly like it does for older payloads.
			name:         "When called, the CRD copy should run after staging release-manifests and before the feature-set filter",
			platformType: hyperv1.AWSPlatform,
			oauthEnabled: true,
			featureSet:   configv1.Default,
			assertions: func(g Gomega, script string) {
				crdCopyIdx := strings.Index(script, "for crd in /var/payload/release-manifests/0000_00_cluster-version-operator_01_*.crd.yaml")
				releaseStagingIdx := strings.Index(script, "cp -R /release-manifests /var/payload/")
				filterIdx := strings.Index(script, "for file in $(find /var/payload/manifests/")
				g.Expect(releaseStagingIdx).To(BeNumerically(">=", 0))
				g.Expect(crdCopyIdx).To(BeNumerically(">=", 0))
				g.Expect(filterIdx).To(BeNumerically(">=", 0))
				g.Expect(crdCopyIdx).To(BeNumerically(">", releaseStagingIdx))
				g.Expect(crdCopyIdx).To(BeNumerically("<", filterIdx))
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

func TestBootstrapScriptAppliesClusterVersionCRDBeforeClusterVersion(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	deployment, err := assets.LoadDeploymentManifest(ComponentName)
	g.Expect(err).ToNot(HaveOccurred())

	bootstrap := podspec.FindContainer("bootstrap", deployment.Spec.Template.Spec.InitContainers)
	g.Expect(bootstrap).ToNot(BeNil())
	g.Expect(bootstrap.Args).To(HaveLen(2))
	script := bootstrap.Args[1]

	// The ClusterVersion CRD must be applied before the ClusterVersion object is
	// created. The prepare-payload init container copies the CVO CRDs into
	// /var/payload/manifests regardless of whether the payload ships them in
	// /manifests (older payloads) or only in /release-manifests (payloads built
	// after openshift/api#3155 and cluster-version-operator#1507).
	g.Expect(script).To(ContainSubstring("clusterversion_crds=(/var/payload/manifests/0000_00_cluster-version-operator_01_clusterversions*)"))
	crdApplyIdx := strings.Index(script, `oc apply -f "${clusterversion_crds[@]}"`)
	clusterVersionApplyIdx := strings.Index(script, "oc apply -f /tmp/clusterversion.json")
	g.Expect(crdApplyIdx).To(BeNumerically(">=", 0))
	g.Expect(clusterVersionApplyIdx).To(BeNumerically(">=", 0))
	g.Expect(crdApplyIdx).To(BeNumerically("<", clusterVersionApplyIdx))

	// An unexpectedly missing CRD must not crash the bootstrap container on an
	// unexpanded glob (a crashlooping init container is a total control plane
	// outage); the clusterversion apply below surfaces a clear error instead.
	g.Expect(script).To(ContainSubstring("shopt -s nullglob"))
	g.Expect(script).To(ContainSubstring("if [[ ${#clusterversion_crds[@]} -gt 0 ]]; then"))
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
				g.Expect(resources).NotTo(ContainElement("kube-storage-version-migrator-operator"))
				g.Expect(resources).NotTo(ContainElement("migrator"))
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
				g.Expect(resources).To(ContainElement("kube-storage-version-migrator-operator"))
				g.Expect(resources).To(ContainElement("migrator"))
			},
		},
		{
			name:         "When platform is GCP, it should return the full list including GCP PD CSI driver operator and controller",
			platformType: hyperv1.GCPPlatform,
			assertions: func(g Gomega, resources []string) {
				g.Expect(resources).To(ContainElement("cluster-storage-operator"))
				g.Expect(resources).To(ContainElement("csi-snapshot-controller-operator"))
				g.Expect(resources).To(ContainElement("gcp-pd-csi-driver-operator"))
				g.Expect(resources).To(ContainElement("gcp-pd-csi-driver-controller"))
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
		ReleaseImageProvider:  testutil.FakeImageProvider(),
		ImageMetadataProvider: fakeImageProvider,
	}
}

func TestAdaptDeploymentClusterVersionCapabilities(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                string
		featureSet          configv1.FeatureSet
		omitConfiguration   bool
		releaseVersion      string
		expectCompatibility bool
	}{
		{name: "When configuration is omitted on 5.1, it should enable compatibility requirements", omitConfiguration: true, releaseVersion: "5.1.0", expectCompatibility: true},
		{name: "When the release is before 5.1, it should not enable compatibility requirements", featureSet: configv1.Default, releaseVersion: "5.0.0", expectCompatibility: false},
		{name: "When the feature set is Default on 5.1, it should enable compatibility requirements", featureSet: configv1.Default, releaseVersion: "5.1.0", expectCompatibility: true},
		{name: "When the feature set is TechPreviewNoUpgrade on 5.1, it should enable compatibility requirements", featureSet: configv1.TechPreviewNoUpgrade, releaseVersion: "5.1.0", expectCompatibility: true},
		{name: "When the feature set is DevPreviewNoUpgrade on 5.1, it should enable compatibility requirements", featureSet: configv1.DevPreviewNoUpgrade, releaseVersion: "5.1.0", expectCompatibility: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			deployment, err := assets.LoadDeploymentManifest(ComponentName)
			g.Expect(err).ToNot(HaveOccurred())
			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Name: "test-hcp", Namespace: "test-ns"},
				Spec: hyperv1.HostedControlPlaneSpec{
					ClusterID: "test-cluster-id",
					Platform:  hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform},
					Configuration: &hyperv1.ClusterConfiguration{FeatureGate: &configv1.FeatureGateSpec{
						FeatureGateSelection: configv1.FeatureGateSelection{FeatureSet: test.featureSet},
					}},
				},
			}
			if test.omitConfiguration {
				hcp.Spec.Configuration = nil
			}
			cpContext := createTestContext(hcp)
			cpContext.ReleaseImageProvider = testutil.FakeImageProvider(testutil.WithVersion(test.releaseVersion))

			err = (&clusterVersionOperator{}).adaptDeployment(cpContext, deployment)
			g.Expect(err).ToNot(HaveOccurred())

			bootstrap := podspec.FindContainer("bootstrap", deployment.Spec.Template.Spec.InitContainers)
			g.Expect(bootstrap).ToNot(BeNil())
			var clusterVersion configv1.ClusterVersion
			for _, env := range bootstrap.Env {
				if env.Name == "CLUSTER_VERSION_JSON" {
					err = json.Unmarshal([]byte(env.Value), &clusterVersion)
					break
				}
			}
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(clusterVersion.Spec.Capabilities.AdditionalEnabledCapabilities).To(ContainElement(configv1.ClusterVersionCapabilityBuild))
			if test.expectCompatibility {
				g.Expect(clusterVersion.Spec.Capabilities.AdditionalEnabledCapabilities).To(ContainElement(configv1.ClusterVersionCapabilityCompatibilityRequirements))
			} else {
				g.Expect(clusterVersion.Spec.Capabilities.AdditionalEnabledCapabilities).ToNot(ContainElement(configv1.ClusterVersionCapabilityCompatibilityRequirements))
			}
		})
	}
}

func TestAdaptDeployment(t *testing.T) {
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

	// OMNI-CPO drift gating: a main-built CPO driving a pre-4.23 control plane must strip the
	// CVO TLS args, which a pre-4.23 CVO binary rejects. 4.23 and 5.0 are the same release.
	omniHCP := func() *hyperv1.HostedControlPlane {
		return &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{Name: "test-hcp", Namespace: "test-ns"},
			Spec: hyperv1.HostedControlPlaneSpec{
				Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform},
				Configuration: &hyperv1.ClusterConfiguration{
					APIServer: &configv1.APIServerSpec{
						TLSSecurityProfile: &configv1.TLSSecurityProfile{Type: configv1.TLSProfileIntermediateType},
					},
				},
			},
		}
	}
	t.Run("When OMNI-CPO drives a pre-4.23 control plane, it should strip the CVO TLS args", func(t *testing.T) {
		g := NewWithT(t)
		t.Setenv(config.OmniCPOEnvVar, "1")
		cpContext := createTestContext(omniHCP())
		cpContext.ReleaseImageProvider = testutil.FakeImageProvider(testutil.WithVersion("4.20.0"))
		deployment, err := assets.LoadDeploymentManifest(ComponentName)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect((&clusterVersionOperator{}).adaptDeployment(cpContext, deployment)).ToNot(HaveOccurred())
		g.Expect(omnicpo.Apply(cpContext, deployment, omniCPOTransforms())).ToNot(HaveOccurred())
		c := podspec.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
		g.Expect(c).ToNot(BeNil())
		args := strings.Join(c.Args, " ")
		g.Expect(args).ToNot(ContainSubstring("--tls-min-version"))
		g.Expect(args).ToNot(ContainSubstring("--tls-cipher-suites"))
	})
	t.Run("When OMNI-CPO drives a 4.23/5.0 control plane, it should keep the CVO TLS args", func(t *testing.T) {
		g := NewWithT(t)
		t.Setenv(config.OmniCPOEnvVar, "1")
		cpContext := createTestContext(omniHCP())
		cpContext.ReleaseImageProvider = testutil.FakeImageProvider(testutil.WithVersion("5.0.0"))
		deployment, err := assets.LoadDeploymentManifest(ComponentName)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect((&clusterVersionOperator{}).adaptDeployment(cpContext, deployment)).ToNot(HaveOccurred())
		g.Expect(omnicpo.Apply(cpContext, deployment, omniCPOTransforms())).ToNot(HaveOccurred())
		c := podspec.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
		g.Expect(c).ToNot(BeNil())
		g.Expect(strings.Join(c.Args, " ")).To(ContainSubstring("--tls-min-version"))
	})
}
