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
		name              string
		featureGates      []string
		configuration     *hyperv1.ClusterConfiguration
		expectedFG        []string
		expectedMinTLS    string
		expectedCiphers   []string
		expectError       bool
		expectedErrSubstr string
	}{
		{
			name:            "When feature gates are provided, it should set them on the config",
			featureGates:    []string{"FeatureA=true", "FeatureB=false"},
			configuration:   nil,
			expectedFG:      []string{"FeatureA=true", "FeatureB=false"},
			expectedMinTLS:  string(configv1.TLSProfiles[configv1.TLSProfileIntermediateType].MinTLSVersion),
			expectedCiphers: config.OpenSSLToIANACipherSuites(configv1.TLSProfiles[configv1.TLSProfileIntermediateType].Ciphers),
		},
		{
			name:            "When feature gates are empty, it should set empty slice",
			featureGates:    []string{},
			configuration:   nil,
			expectedFG:      []string{},
			expectedMinTLS:  string(configv1.TLSProfiles[configv1.TLSProfileIntermediateType].MinTLSVersion),
			expectedCiphers: config.OpenSSLToIANACipherSuites(configv1.TLSProfiles[configv1.TLSProfileIntermediateType].Ciphers),
		},
		{
			name:            "When configuration is nil, it should set default TLS values",
			featureGates:    []string{"SomeGate=true"},
			configuration:   nil,
			expectedFG:      []string{"SomeGate=true"},
			expectedMinTLS:  string(configv1.TLSProfiles[configv1.TLSProfileIntermediateType].MinTLSVersion),
			expectedCiphers: config.OpenSSLToIANACipherSuites(configv1.TLSProfiles[configv1.TLSProfileIntermediateType].Ciphers),
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
			expectedFG:      []string{"Gate1=true"},
			expectedMinTLS:  string(configv1.TLSProfiles[configv1.TLSProfileOldType].MinTLSVersion),
			expectedCiphers: config.OpenSSLToIANACipherSuites(configv1.TLSProfiles[configv1.TLSProfileOldType].Ciphers),
		},
		{
			name:         "When TLS profile is Custom with nil Custom field, it should return error",
			featureGates: []string{"Gate1=true"},
			configuration: &hyperv1.ClusterConfiguration{
				APIServer: &configv1.APIServerSpec{
					TLSSecurityProfile: &configv1.TLSSecurityProfile{
						Type: configv1.TLSProfileCustomType,
					},
				},
			},
			expectError:       true,
			expectedErrSubstr: "Custom but Custom field is nil",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			cfg := &openshiftcpv1.OpenShiftControllerManagerConfig{
				ServingInfo: &configv1.HTTPServingInfo{},
			}

			err := adaptConfig(cfg, tc.configuration, tc.featureGates)

			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tc.expectedErrSubstr))
				return
			}

			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(cfg.FeatureGates).To(Equal(tc.expectedFG))
			g.Expect(cfg.ServingInfo.MinTLSVersion).To(Equal(tc.expectedMinTLS))
			g.Expect(cfg.ServingInfo.CipherSuites).To(Equal(tc.expectedCiphers))
		})
	}
}
