package configuration

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"
)

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
