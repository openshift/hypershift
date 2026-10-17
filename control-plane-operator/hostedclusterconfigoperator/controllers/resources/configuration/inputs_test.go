package configuration

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"
)

func TestHostedControlPlane(t *testing.T) {
	t.Run("When policy inputs contain mutable values, it should create an independent compatibility snapshot", func(t *testing.T) {
		assert := NewWithT(t)
		hcp := configurationHCP()
		hcp.Spec.Platform = hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform, AWS: &hyperv1.AWSPlatformSpec{Region: "us-east-1"}}
		hcp.Spec.Configuration = &hyperv1.ClusterConfiguration{Proxy: &configv1.ProxySpec{HTTPProxy: "http://proxy.example.com:8080"}}
		hcp.Spec.ImageContentSources = []hyperv1.ImageContentSource{{Source: "quay.io/openshift-release-dev/ocp-release", Mirrors: []string{"mirror.example.com/release"}}}
		hcp.Annotations = map[string]string{hyperv1.SwiftPodNetworkInstanceAnnotation: "swift-instance"}
		hcp.Spec.KubeAPIServerDNSName = "api.custom.example.com"
		hcp.Spec.InfrastructureAvailabilityPolicy = hyperv1.HighlyAvailable
		hcp.Spec.IssuerURL = "https://issuer.example.com"
		original := hcp.DeepCopy()
		params := globalParams(hcp)
		snapshot := params.hostedControlPlane()
		assert.Expect(snapshot.Spec).To(Equal(hcp.Spec))
		assert.Expect(snapshot.Status).To(Equal(hcp.Status))
		assert.Expect(snapshot.Annotations).To(Equal(hcp.Annotations))
		snapshot.Spec.Platform.AWS.Region = "us-west-2"
		snapshot.Spec.Configuration.Proxy.HTTPProxy = ""
		snapshot.Spec.ImageContentSources[0].Mirrors[0] = "changed"
		assert.Expect(hcp).To(Equal(original))
		assert.Expect(params.hostedControlPlane().Spec).To(Equal(original.Spec))
	})
}

func TestProxyParamsFor(t *testing.T) {
	for _, testCase := range []struct {
		name          string
		configuration *hyperv1.ClusterConfiguration
		expected      string
	}{
		{name: "When configuration is missing, it should retain namespace without a CA"},
		{name: "When proxy is missing, it should retain namespace without a CA", configuration: &hyperv1.ClusterConfiguration{}},
		{name: "When proxy is configured, it should select only the CA reference", configuration: &hyperv1.ClusterConfiguration{Proxy: &configv1.ProxySpec{TrustedCA: configv1.ConfigMapNameReference{Name: "custom-ca"}}}, expected: "custom-ca"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			NewWithT(t).Expect(ProxyParamsFor("control-plane", testCase.configuration)).To(Equal(ProxyParams{Namespace: "control-plane", TrustedCA: testCase.expected}))
		})
	}
}
