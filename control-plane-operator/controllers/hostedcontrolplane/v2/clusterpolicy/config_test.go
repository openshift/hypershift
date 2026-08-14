package clusterpolicy

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/config"

	configv1 "github.com/openshift/api/config/v1"
	openshiftcpv1 "github.com/openshift/api/openshiftcontrolplane/v1"
)

func TestAdaptConfig(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name            string
		featureGates    []string
		configuration   *hyperv1.ClusterConfiguration
		expectedFG      []string
		expectedMinTLS  string
		expectedCiphers []string
	}{
		{
			name:            "When feature gates are provided, it should set them on the config",
			featureGates:    []string{"FeatureA=true", "FeatureB=false"},
			configuration:   nil,
			expectedFG:      []string{"FeatureA=true", "FeatureB=false"},
			expectedMinTLS:  config.MinTLSVersion(nil),
			expectedCiphers: config.CipherSuites(nil),
		},
		{
			name:            "When feature gates are empty, it should set empty slice",
			featureGates:    []string{},
			configuration:   nil,
			expectedFG:      []string{},
			expectedMinTLS:  config.MinTLSVersion(nil),
			expectedCiphers: config.CipherSuites(nil),
		},
		{
			name:            "When configuration is nil, it should set default TLS values",
			featureGates:    []string{"SomeGate=true"},
			configuration:   nil,
			expectedFG:      []string{"SomeGate=true"},
			expectedMinTLS:  string(configv1.TLSProfiles[configv1.TLSProfileIntermediateType].MinTLSVersion),
			expectedCiphers: config.CipherSuites(nil),
		},
		{
			name:         "When configuration has a TLS security profile, it should set MinTLSVersion and CipherSuites",
			featureGates: []string{"Gate1=true"},
			configuration: &hyperv1.ClusterConfiguration{
				APIServer: &configv1.APIServerSpec{
					TLSSecurityProfile: &configv1.TLSSecurityProfile{
						Type: configv1.TLSProfileOldType,
					},
				},
			},
			expectedFG:     []string{"Gate1=true"},
			expectedMinTLS: string(configv1.TLSProfiles[configv1.TLSProfileOldType].MinTLSVersion),
			expectedCiphers: config.CipherSuites(&configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileOldType,
			}),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			cfg := &openshiftcpv1.OpenShiftControllerManagerConfig{
				ServingInfo: &configv1.HTTPServingInfo{},
			}

			adaptConfig(cfg, tc.configuration, tc.featureGates)

			g.Expect(cfg.FeatureGates).To(Equal(tc.expectedFG))
			g.Expect(cfg.ServingInfo.MinTLSVersion).To(Equal(tc.expectedMinTLS))
			g.Expect(cfg.ServingInfo.CipherSuites).To(Equal(tc.expectedCiphers))
		})
	}
}
