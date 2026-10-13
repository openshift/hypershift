package registryoperator

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"

	"sigs.k8s.io/yaml"
)

func Test_adaptControllerConfig(t *testing.T) {
	intermediate := &configv1.TLSSecurityProfile{
		Type:         configv1.TLSProfileIntermediateType,
		Intermediate: &configv1.IntermediateTLSProfile{},
	}

	modern := &configv1.TLSSecurityProfile{
		Type:   configv1.TLSProfileModernType,
		Modern: &configv1.ModernTLSProfile{},
	}

	old := &configv1.TLSSecurityProfile{
		Type: configv1.TLSProfileOldType,
		Old:  &configv1.OldTLSProfile{},
	}

	custom := &configv1.TLSSecurityProfile{
		Type: configv1.TLSProfileCustomType,
		Custom: &configv1.CustomTLSProfile{
			TLSProfileSpec: configv1.TLSProfileSpec{
				MinTLSVersion: configv1.VersionTLS12,
				Ciphers: []string{
					"ECDHE-RSA-AES128-GCM-SHA256",
					"ECDHE-RSA-AES256-GCM-SHA384",
				},
			},
		},
	}

	testCases := []struct {
		name              string
		tlsProfile        *configv1.TLSSecurityProfile
		existingData      map[string]string
		expectedCiphers   []string
		expectedMinTLS    configv1.TLSProtocolVersion
		shouldPreserveKey string
	}{
		{
			name:            "When TLS profile is Intermediate it should use Intermediate ciphers and TLS 1.2",
			tlsProfile:      intermediate,
			expectedCiphers: config.OpenSSLToIANACipherSuites(configv1.TLSProfiles[configv1.TLSProfileIntermediateType].Ciphers),
			expectedMinTLS:  configv1.TLSProfiles[configv1.TLSProfileIntermediateType].MinTLSVersion,
		},
		{
			name:           "When TLS profile is Modern it should use TLS 1.3 with empty cipher list",
			tlsProfile:     modern,
			expectedMinTLS: configv1.TLSProfiles[configv1.TLSProfileModernType].MinTLSVersion,
		},
		{
			name:            "When TLS profile is Old it should use Old ciphers and TLS 1.0",
			tlsProfile:      old,
			expectedCiphers: config.OpenSSLToIANACipherSuites(configv1.TLSProfiles[configv1.TLSProfileOldType].Ciphers),
			expectedMinTLS:  configv1.TLSProfiles[configv1.TLSProfileOldType].MinTLSVersion,
		},
		{
			name:            "When TLS profile is Custom it should use custom ciphers and TLS version",
			tlsProfile:      custom,
			expectedCiphers: config.OpenSSLToIANACipherSuites(custom.Custom.Ciphers),
			expectedMinTLS:  custom.Custom.MinTLSVersion,
		},
		{
			name:              "When ConfigMap has existing data it should preserve other keys",
			tlsProfile:        intermediate,
			existingData:      map[string]string{"other-key": "other-value"},
			expectedCiphers:   config.OpenSSLToIANACipherSuites(configv1.TLSProfiles[configv1.TLSProfileIntermediateType].Ciphers),
			expectedMinTLS:    configv1.TLSProfiles[configv1.TLSProfileIntermediateType].MinTLSVersion,
			shouldPreserveKey: "other-key",
		},
		{
			name:            "When TLS profile is nil it should use Intermediate profile",
			expectedCiphers: config.OpenSSLToIANACipherSuites(configv1.TLSProfiles[configv1.TLSProfileIntermediateType].Ciphers),
			expectedMinTLS:  configv1.TLSProfiles[configv1.TLSProfileIntermediateType].MinTLSVersion,
		},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			hcp := &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Configuration: &hyperv1.ClusterConfiguration{
						APIServer: &configv1.APIServerSpec{
							TLSSecurityProfile: tt.tlsProfile,
						},
					},
				},
			}

			cm := &corev1.ConfigMap{Data: tt.existingData}
			cpContext := component.WorkloadContext{HCP: hcp}

			err := adaptControllerConfig(cpContext, cm)
			g.Expect(err).ToNot(HaveOccurred())

			g.Expect(cm.Data).To(HaveKey("config.yaml"))
			configYAML := cm.Data["config.yaml"]

			var controllerConfig map[string]any
			err = yaml.Unmarshal([]byte(configYAML), &controllerConfig)
			g.Expect(err).ToNot(HaveOccurred())

			g.Expect(controllerConfig).To(HaveKeyWithValue("apiVersion", configv1.GroupVersion.String()))
			g.Expect(controllerConfig).To(HaveKeyWithValue("kind", "GenericControllerConfig"))

			servingInfo := controllerConfig["servingInfo"].(map[string]any)
			g.Expect(servingInfo).To(HaveKeyWithValue("bindAddress", ":60000"))
			g.Expect(servingInfo).To(HaveKeyWithValue("minTLSVersion", string(tt.expectedMinTLS)))

			if tt.shouldPreserveKey != "" {
				g.Expect(cm.Data).To(HaveKeyWithValue(tt.shouldPreserveKey, tt.existingData[tt.shouldPreserveKey]))
			}

			if tt.expectedCiphers == nil {
				g.Expect(servingInfo).ToNot(HaveKey("cipherSuites"))
				return
			}

			cipherSuites := servingInfo["cipherSuites"].([]any)
			cipherStrings := make([]string, len(cipherSuites))
			for i, cipher := range cipherSuites {
				cipherStrings[i] = cipher.(string)
			}
			g.Expect(cipherStrings).To(Equal(tt.expectedCiphers))
		})
	}
}
