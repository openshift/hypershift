package config

import (
	"fmt"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	configv1 "github.com/openshift/api/config/v1"
)

func TestTLSArgs(t *testing.T) {
	// Helper to build expected args from TLS profile
	buildExpectedArgs := func(profileType configv1.TLSProfileType) []string {
		profile := configv1.TLSProfiles[profileType]
		args := []string{
			fmt.Sprintf("--tls-min-version=%s", profile.MinTLSVersion),
		}
		ciphers := OpenSSLToIANACipherSuites(profile.Ciphers)
		if len(ciphers) > 0 {
			args = append(args, fmt.Sprintf("--tls-cipher-suites=%s", strings.Join(ciphers, ",")))
		}
		return args
	}

	tests := []struct {
		name              string
		profile           *configv1.TLSSecurityProfile
		expectedArgs      []string
		expectError       bool
		expectedErrSubstr string
	}{
		{
			name:         "When profile is nil it should return intermediate defaults",
			profile:      nil,
			expectedArgs: buildExpectedArgs(configv1.TLSProfileIntermediateType),
		},
		{
			name: "When using Modern profile it should return only min-version",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileModernType,
			},
			expectedArgs: buildExpectedArgs(configv1.TLSProfileModernType),
		},
		{
			name: "When using Intermediate profile it should return min-version and cipher-suites",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileIntermediateType,
			},
			expectedArgs: buildExpectedArgs(configv1.TLSProfileIntermediateType),
		},
		{
			name: "When using Custom profile with specific ciphers it should return custom TLS args",
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
			expectedArgs: []string{
				"--tls-min-version=VersionTLS12",
				fmt.Sprintf("--tls-cipher-suites=%s", strings.Join(OpenSSLToIANACipherSuites([]string{
					"ECDHE-ECDSA-AES128-GCM-SHA256",
					"ECDHE-RSA-AES128-GCM-SHA256",
				}), ",")),
			},
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
			name: "When using Old profile it should return min-version and cipher-suites",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileOldType,
			},
			expectedArgs: buildExpectedArgs(configv1.TLSProfileOldType),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)

			result, err := TLSArgs(test.profile)
			if test.expectError {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(test.expectedErrSubstr))
				return
			}
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(result).To(Equal(test.expectedArgs))
		})
	}
}
