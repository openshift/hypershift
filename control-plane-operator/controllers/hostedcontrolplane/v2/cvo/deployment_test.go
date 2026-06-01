package cvo

import (
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
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
