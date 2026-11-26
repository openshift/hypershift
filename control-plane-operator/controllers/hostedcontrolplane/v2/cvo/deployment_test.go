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
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/support/util/fakeimagemetadataprovider"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

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

func TestAdaptDeployment(t *testing.T) {
	t.Parallel()

	// The payload staged by the CVO (prepare-payload) and RELEASE_IMAGE must
	// always follow the data plane (spec.release) image, even when
	// controlPlaneReleaseImage points at a different (skewed) payload. The
	// fake metadata provider pins the configured digest onto the resolved
	// reference of whatever image was looked up, so the expected value proves
	// the lookup used the data plane release image.
	t.Run("When the control plane payload is skewed, it should stage the data plane payload", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		hcp := &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{Name: "test-hcp", Namespace: "test-ns"},
			Spec: hyperv1.HostedControlPlaneSpec{
				Platform:                 hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform},
				ReleaseImage:             "quay.io/openshift-release-dev/ocp-release:4.22.10-x86_64",
				ControlPlaneReleaseImage: ptr.To("quay.io/csrwng/release:4.22-kas-mask-nightly"),
			},
		}

		deployment, err := assets.LoadDeploymentManifest(ComponentName)
		g.Expect(err).ToNot(HaveOccurred())

		cpContext := createTestContext(hcp)

		cvo := &clusterVersionOperator{}
		err = cvo.adaptDeployment(cpContext, deployment)
		g.Expect(err).ToNot(HaveOccurred())

		expectedDataPlaneImage := "quay.io/openshift-release-dev/ocp-release@sha256:1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"

		preparePayload := podspec.FindContainer("prepare-payload", deployment.Spec.Template.Spec.InitContainers)
		g.Expect(preparePayload).ToNot(BeNil())
		g.Expect(preparePayload.Image).To(Equal(expectedDataPlaneImage))

		container := podspec.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
		g.Expect(container).ToNot(BeNil())
		g.Expect(container.Env).To(ContainElement(corev1.EnvVar{
			Name:  "RELEASE_IMAGE",
			Value: expectedDataPlaneImage,
		}))
	})
}
