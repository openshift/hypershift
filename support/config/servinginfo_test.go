package config

import (
	"testing"

	. "github.com/onsi/gomega"

	configv1 "github.com/openshift/api/config/v1"
)

func TestApplyServingInfoFromTLSProfile(t *testing.T) {
	tests := []struct {
		name                  string
		profile               *configv1.TLSSecurityProfile
		expectedMinTLSVersion string
		expectedCipherSuites  []string
		expectError           bool
		expectedErrSubstr     string
	}{
		{
			name:                  "When profile is nil it should apply intermediate defaults",
			profile:               nil,
			expectedMinTLSVersion: string(configv1.TLSProfiles[configv1.TLSProfileIntermediateType].MinTLSVersion),
			expectedCipherSuites:  OpenSSLToIANACipherSuites(configv1.TLSProfiles[configv1.TLSProfileIntermediateType].Ciphers),
		},
		{
			name: "When using Modern profile it should apply only min-version",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileModernType,
			},
			expectedMinTLSVersion: string(configv1.TLSProfiles[configv1.TLSProfileModernType].MinTLSVersion),
			expectedCipherSuites:  OpenSSLToIANACipherSuites(configv1.TLSProfiles[configv1.TLSProfileModernType].Ciphers),
		},
		{
			name: "When using Intermediate profile it should apply min-version and cipher-suites",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileIntermediateType,
			},
			expectedMinTLSVersion: string(configv1.TLSProfiles[configv1.TLSProfileIntermediateType].MinTLSVersion),
			expectedCipherSuites:  OpenSSLToIANACipherSuites(configv1.TLSProfiles[configv1.TLSProfileIntermediateType].Ciphers),
		},
		{
			name: "When using Custom profile with specific ciphers it should apply custom TLS settings",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{
					TLSProfileSpec: configv1.TLSProfileSpec{
						MinTLSVersion: configv1.VersionTLS12,
						Ciphers: []string{
							"ECDHE-ECDSA-AES128-GCM-SHA256",
							"ECDHE-RSA-AES128-GCM-SHA256",
						},
					},
				},
			},
			expectedMinTLSVersion: string(configv1.VersionTLS12),
			expectedCipherSuites: OpenSSLToIANACipherSuites([]string{
				"ECDHE-ECDSA-AES128-GCM-SHA256",
				"ECDHE-RSA-AES128-GCM-SHA256",
			}),
		},
		{
			name: "When TLS profile is Custom with nil Custom field, it should return error",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
			},
			expectError:       true,
			expectedErrSubstr: "Custom but Custom field is nil",
		},
		{
			name: "When using Old profile it should apply min-version and cipher-suites",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileOldType,
			},
			expectedMinTLSVersion: string(configv1.TLSProfiles[configv1.TLSProfileOldType].MinTLSVersion),
			expectedCipherSuites:  OpenSSLToIANACipherSuites(configv1.TLSProfiles[configv1.TLSProfileOldType].Ciphers),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)

			servingInfo := &configv1.ServingInfo{}
			err := ApplyServingInfoFromTLSProfile(servingInfo, test.profile)
			if test.expectError {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(test.expectedErrSubstr))
				return
			}
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(servingInfo.MinTLSVersion).To(Equal(test.expectedMinTLSVersion))
			g.Expect(servingInfo.CipherSuites).To(Equal(test.expectedCipherSuites))
		})
	}
}
