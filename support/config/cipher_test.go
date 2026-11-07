package config

import (
	"crypto/tls"
	"testing"

	. "github.com/onsi/gomega"

	configv1 "github.com/openshift/api/config/v1"
)

func TestSetMinTLSVersionUsingAPIServer(t *testing.T) {
	tests := []struct {
		name          string
		apiServer     *configv1.APIServer
		expectError   bool
		expectedValue uint16
	}{
		{
			name: "When using intermediate profile, it should set TLS 1.2",
			apiServer: &configv1.APIServer{
				Spec: configv1.APIServerSpec{
					TLSSecurityProfile: &configv1.TLSSecurityProfile{
						Type: configv1.TLSProfileIntermediateType,
					},
				},
			},
			expectError:   false,
			expectedValue: tls.VersionTLS12,
		},
		{
			name: "When using modern profile, it should set TLS 1.3",
			apiServer: &configv1.APIServer{
				Spec: configv1.APIServerSpec{
					TLSSecurityProfile: &configv1.TLSSecurityProfile{
						Type: configv1.TLSProfileModernType,
					},
				},
			},
			expectError:   false,
			expectedValue: tls.VersionTLS13,
		},
		{
			name: "When using custom profile with valid version, it should succeed",
			apiServer: &configv1.APIServer{
				Spec: configv1.APIServerSpec{
					TLSSecurityProfile: &configv1.TLSSecurityProfile{
						Type: configv1.TLSProfileCustomType,
						Custom: &configv1.CustomTLSProfile{
							TLSProfileSpec: configv1.TLSProfileSpec{
								MinTLSVersion: configv1.VersionTLS12,
							},
						},
					},
				},
			},
			expectError:   false,
			expectedValue: tls.VersionTLS12,
		},
		{
			name: "When using custom profile with invalid version, it should return error",
			apiServer: &configv1.APIServer{
				Spec: configv1.APIServerSpec{
					TLSSecurityProfile: &configv1.TLSSecurityProfile{
						Type: configv1.TLSProfileCustomType,
						Custom: &configv1.CustomTLSProfile{
							TLSProfileSpec: configv1.TLSProfileSpec{
								MinTLSVersion: "TLS99",
							},
						},
					},
				},
			},
			expectError: true,
		},
		{
			name:          "When TLS profile is nil, it should use intermediate defaults",
			apiServer:     &configv1.APIServer{},
			expectError:   false,
			expectedValue: tls.VersionTLS12,
		},
		{
			name: "When using custom profile with nil Custom field, it should return error",
			apiServer: &configv1.APIServer{
				Spec: configv1.APIServerSpec{
					TLSSecurityProfile: &configv1.TLSSecurityProfile{
						Type: configv1.TLSProfileCustomType,
					},
				},
			},
			expectError: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)

			setter, err := SetMinTLSVersionUsingAPIServer(test.apiServer)

			if test.expectError {
				g.Expect(err).To(HaveOccurred())
				g.Expect(setter).To(BeNil())
				return
			}

			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(setter).ToNot(BeNil())

			// Apply the setter and verify the config
			tlsConfig := &tls.Config{}
			setter(tlsConfig)
			g.Expect(tlsConfig.MinVersion).To(Equal(test.expectedValue))
		})
	}
}

func TestSetCipherSuitesUsingAPIServer(t *testing.T) {
	tests := []struct {
		name        string
		apiServer   *configv1.APIServer
		expectError bool
		expectEmpty bool
	}{
		{
			name: "When using intermediate profile, it should set valid cipher suites",
			apiServer: &configv1.APIServer{
				Spec: configv1.APIServerSpec{
					TLSSecurityProfile: &configv1.TLSSecurityProfile{
						Type: configv1.TLSProfileIntermediateType,
					},
				},
			},
			expectError: false,
		},
		{
			name: "When using modern profile, it should succeed even with empty cipher list",
			apiServer: &configv1.APIServer{
				Spec: configv1.APIServerSpec{
					TLSSecurityProfile: &configv1.TLSSecurityProfile{
						Type: configv1.TLSProfileModernType,
					},
				},
			},
			expectError: false,
			expectEmpty: true, // modern profile may have no ciphers (TLS 1.3)
		},
		{
			name: "When using custom profile with valid ciphers, it should succeed",
			apiServer: &configv1.APIServer{
				Spec: configv1.APIServerSpec{
					TLSSecurityProfile: &configv1.TLSSecurityProfile{
						Type: configv1.TLSProfileCustomType,
						Custom: &configv1.CustomTLSProfile{
							TLSProfileSpec: configv1.TLSProfileSpec{
								Ciphers: []string{
									"ECDHE-RSA-AES128-GCM-SHA256",
									"ECDHE-RSA-AES256-GCM-SHA384",
								},
							},
						},
					},
				},
			},
			expectError: false,
		},
		{
			name: "When using custom profile with unmapped cipher, it should succeed with empty list",
			apiServer: &configv1.APIServer{
				Spec: configv1.APIServerSpec{
					TLSSecurityProfile: &configv1.TLSSecurityProfile{
						Type: configv1.TLSProfileCustomType,
						Custom: &configv1.CustomTLSProfile{
							TLSProfileSpec: configv1.TLSProfileSpec{
								Ciphers: []string{
									"INVALID_CIPHER_SUITE",
								},
							},
						},
					},
				},
			},
			expectError: false,
			expectEmpty: true, // XXX unknown ciphers are filtered out
		},
		{
			name:        "When TLS profile is nil, it should use intermediate defaults",
			apiServer:   &configv1.APIServer{},
			expectError: false,
		},
		{
			name: "When using custom profile with nil Custom field, it should return error",
			apiServer: &configv1.APIServer{
				Spec: configv1.APIServerSpec{
					TLSSecurityProfile: &configv1.TLSSecurityProfile{
						Type: configv1.TLSProfileCustomType,
					},
				},
			},
			expectError: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)

			setter, err := SetCipherSuitesUsingAPIServer(test.apiServer)

			if test.expectError {
				g.Expect(err).To(HaveOccurred())
				g.Expect(setter).To(BeNil())
				return
			}

			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(setter).ToNot(BeNil())

			tlsConfig := &tls.Config{}
			setter(tlsConfig)
			if test.expectEmpty {
				g.Expect(tlsConfig.CipherSuites).To(BeEmpty())
				return
			}

			g.Expect(tlsConfig.CipherSuites).ToNot(BeEmpty())
		})
	}
}

func TestMinTLSVersion(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name        string
		profile     *configv1.TLSSecurityProfile
		expectedVer string
		expectError bool
	}{
		{
			name:        "When profile is nil, it should default to Intermediate",
			profile:     nil,
			expectedVer: "VersionTLS12",
		},
		{
			name: "When using Intermediate profile, it should return TLS 1.2",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileIntermediateType,
			},
			expectedVer: "VersionTLS12",
		},
		{
			name: "When using Modern profile, it should return TLS 1.3",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileModernType,
			},
			expectedVer: "VersionTLS13",
		},
		{
			name: "When using Custom profile with valid Custom field, it should return custom version",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{
					TLSProfileSpec: configv1.TLSProfileSpec{
						MinTLSVersion: configv1.VersionTLS12,
					},
				},
			},
			expectedVer: "VersionTLS12",
		},
		{
			name: "When using Custom profile with nil Custom field, it should return error",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
			},
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			result, err := MinTLSVersion(tc.profile)
			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("Custom but Custom field is nil"))
				return
			}
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(result).To(Equal(tc.expectedVer))
		})
	}
}

func TestCipherSuites(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name        string
		profile     *configv1.TLSSecurityProfile
		expected    []string
		expectError bool
	}{
		{
			name:    "When profile is nil, it should default to Intermediate ciphers",
			profile: nil,
			expected: []string{
				"TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256",
				"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
				"TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384",
				"TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384",
				"TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256",
				"TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256",
			},
		},
		{
			name: "When using Intermediate profile, it should return intermediate ciphers",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileIntermediateType,
			},
			expected: []string{
				"TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256",
				"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
				"TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384",
				"TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384",
				"TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256",
				"TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256",
			},
		},
		{
			name: "When using Modern profile, it should return empty ciphers (TLS 1.3 uses non-configurable ciphers)",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileModernType,
			},
			expected: []string{},
		},
		{
			name: "When using Custom profile with valid Custom field, it should return custom ciphers",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{
					TLSProfileSpec: configv1.TLSProfileSpec{
						Ciphers: []string{
							"ECDHE-RSA-AES128-GCM-SHA256",
							"ECDHE-RSA-AES256-GCM-SHA384",
						},
					},
				},
			},
			expected: []string{
				"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
				"TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384",
			},
		},
		{
			name: "When using Custom profile with nil Custom field, it should return error",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
			},
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			result, err := CipherSuites(tc.profile)
			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("Custom but Custom field is nil"))
				return
			}
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(result).To(Equal(tc.expected))
		})
	}
}

func TestTLSGroups(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name        string
		profile     *configv1.TLSSecurityProfile
		expected    []configv1.TLSGroup
		expectError bool
	}{
		{
			name:    "When profile is nil, it should default to Intermediate groups",
			profile: nil,
			expected: []configv1.TLSGroup{
				configv1.TLSGroupX25519MLKEM768,
				configv1.TLSGroupX25519,
				configv1.TLSGroupSecP256r1,
				configv1.TLSGroupSecP384r1,
			},
		},
		{
			name: "When using Custom profile without groups, it should return nil",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{
					TLSProfileSpec: configv1.TLSProfileSpec{
						MinTLSVersion: configv1.VersionTLS12,
						Ciphers:       []string{"ECDHE-RSA-AES128-GCM-SHA256"},
					},
				},
			},
			expected: nil,
		},
		{
			name: "When using Custom profile with groups, it should return configured groups",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{
					TLSProfileSpec: configv1.TLSProfileSpec{
						MinTLSVersion: configv1.VersionTLS12,
						Ciphers:       []string{"ECDHE-RSA-AES128-GCM-SHA256"},
						Groups: []configv1.TLSGroup{
							configv1.TLSGroupX25519,
							configv1.TLSGroupSecP256r1,
						},
					},
				},
			},
			expected: []configv1.TLSGroup{
				configv1.TLSGroupX25519,
				configv1.TLSGroupSecP256r1,
			},
		},
		{
			name: "When using Custom profile with nil Custom field, it should return error",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
			},
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			result, err := TLSGroups(tc.profile)
			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
				return
			}
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(result).To(Equal(tc.expected))
		})
	}
}

func TestSetCurvePreferencesUsingAPIServer(t *testing.T) {
	tests := []struct {
		name              string
		apiServer         *configv1.APIServer
		expectError       bool
		expectUnchanged   bool
		expectedCurvePref []tls.CurveID
	}{
		{
			name: "When using intermediate profile, it should set curve preferences from the profile",
			apiServer: &configv1.APIServer{
				Spec: configv1.APIServerSpec{
					TLSSecurityProfile: &configv1.TLSSecurityProfile{
						Type: configv1.TLSProfileIntermediateType,
					},
				},
			},
			expectedCurvePref: []tls.CurveID{
				tls.X25519MLKEM768,
				tls.X25519,
				tls.CurveP256,
				tls.CurveP384,
			},
		},
		{
			name: "When using custom profile without groups, it should not change curve preferences",
			apiServer: &configv1.APIServer{
				Spec: configv1.APIServerSpec{
					TLSSecurityProfile: &configv1.TLSSecurityProfile{
						Type: configv1.TLSProfileCustomType,
						Custom: &configv1.CustomTLSProfile{
							TLSProfileSpec: configv1.TLSProfileSpec{
								MinTLSVersion: configv1.VersionTLS12,
								Ciphers:       []string{"ECDHE-RSA-AES128-GCM-SHA256"},
							},
						},
					},
				},
			},
			expectUnchanged: true,
		},
		{
			name: "When using custom profile with groups, it should set configured curves",
			apiServer: &configv1.APIServer{
				Spec: configv1.APIServerSpec{
					TLSSecurityProfile: &configv1.TLSSecurityProfile{
						Type: configv1.TLSProfileCustomType,
						Custom: &configv1.CustomTLSProfile{
							TLSProfileSpec: configv1.TLSProfileSpec{
								MinTLSVersion: configv1.VersionTLS12,
								Ciphers:       []string{"ECDHE-RSA-AES128-GCM-SHA256"},
								Groups: []configv1.TLSGroup{
									configv1.TLSGroupX25519,
									configv1.TLSGroupSecP256r1,
								},
							},
						},
					},
				},
			},
			expectedCurvePref: []tls.CurveID{tls.X25519, tls.CurveP256},
		},
		{
			name: "When using custom profile with nil Custom field, it should return error",
			apiServer: &configv1.APIServer{
				Spec: configv1.APIServerSpec{
					TLSSecurityProfile: &configv1.TLSSecurityProfile{
						Type: configv1.TLSProfileCustomType,
					},
				},
			},
			expectError: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)

			setter, err := SetCurvePreferencesUsingAPIServer(test.apiServer)
			if test.expectError {
				g.Expect(err).To(HaveOccurred())
				g.Expect(setter).To(BeNil())
				return
			}

			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(setter).ToNot(BeNil())

			tlsConfig := &tls.Config{CurvePreferences: []tls.CurveID{tls.CurveP521}}
			setter(tlsConfig)

			if test.expectUnchanged {
				g.Expect(tlsConfig.CurvePreferences).To(Equal([]tls.CurveID{tls.CurveP521}))
				return
			}

			g.Expect(tlsConfig.CurvePreferences).To(Equal(test.expectedCurvePref))
		})
	}
}

func TestSupportedEtcdCipherSuites(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		input    []string
		expected []string
	}{
		{
			name: "When all ciphers are supported, it should return all",
			input: []string{
				"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
				"TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256",
			},
			expected: []string{
				"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
				"TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256",
			},
		},
		{
			name: "When cipher is unsupported, it should filter it out",
			input: []string{
				"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
				"TLS_UNSUPPORTED_CIPHER",
				"TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256",
			},
			expected: []string{
				"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
				"TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256",
			},
		},
		{
			name:     "When all ciphers are unsupported, it should return empty",
			input:    []string{"INVALID_1", "INVALID_2"},
			expected: []string{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			result := SupportedEtcdCipherSuites(t.Context(), tc.input)
			g.Expect(result).To(Equal(tc.expected))
		})
	}
}
