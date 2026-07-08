package config

import (
	"crypto/tls"
	"testing"

	. "github.com/onsi/gomega"

	configv1 "github.com/openshift/api/config/v1"

	"sigs.k8s.io/yaml"
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

func TestBuildGenericControllerConfigData(t *testing.T) {
	t.Parallel()

	intermediateCiphers := []string{
		"TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256",
		"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
		"TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384",
		"TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384",
		"TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256",
		"TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256",
	}

	oldCiphers := []string{
		"TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256",
		"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
		"TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384",
		"TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384",
		"TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256",
		"TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256",
		"TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA",
		"TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA",
		"TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA",
		"TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA",
	}

	customCiphers := []string{
		"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
		"TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384",
	}

	testCases := []struct {
		name                 string
		bindAddress          string
		bindNetwork          string
		profile              *configv1.TLSSecurityProfile
		expectedMinTLS       string
		expectedCipherSuites []string
	}{
		{
			name:                 "Nil profile uses intermediate defaults",
			bindAddress:          "0.0.0.0:8443",
			bindNetwork:          "tcp",
			expectedMinTLS:       "VersionTLS12",
			expectedCipherSuites: intermediateCiphers,
		},
		{
			name:        "Modern profile has TLS13 and no ciphers",
			bindAddress: "127.0.0.1:8443",
			bindNetwork: "tcp",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileModernType,
			},
			expectedMinTLS: "VersionTLS13",
		},
		{
			name:        "Intermediate profile has TLS12 and intermediate ciphers",
			bindAddress: "0.0.0.0:6443",
			bindNetwork: "tcp4",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileIntermediateType,
			},
			expectedMinTLS:       "VersionTLS12",
			expectedCipherSuites: intermediateCiphers,
		},
		{
			name:        "Old profile has TLS10 and old ciphers",
			bindAddress: "0.0.0.0:8443",
			bindNetwork: "tcp",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileOldType,
			},
			expectedMinTLS:       "VersionTLS10",
			expectedCipherSuites: oldCiphers,
		},
		{
			name:        "Custom profile uses custom TLS version and ciphers",
			bindAddress: "0.0.0.0:8080",
			bindNetwork: "tcp",
			profile: &configv1.TLSSecurityProfile{
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
			},
			expectedMinTLS:       "VersionTLS12",
			expectedCipherSuites: customCiphers,
		},
		{
			name:                 "Different bind address and network",
			bindAddress:          "192.168.1.1:9443",
			bindNetwork:          "unix",
			expectedMinTLS:       "VersionTLS12",
			expectedCipherSuites: intermediateCiphers,
		},
		{
			name:        "Custom profile with unknown ciphers results in empty list",
			bindAddress: "0.0.0.0:8443",
			bindNetwork: "tcp",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{
					TLSProfileSpec: configv1.TLSProfileSpec{
						MinTLSVersion: configv1.VersionTLS12,
						Ciphers: []string{
							"INVALID_CIPHER_SUITE",
							"UNKNOWN_CIPHER",
						},
					},
				},
			},
			expectedMinTLS: "VersionTLS12",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			yamlStr, err := BuildGenericControllerConfigData(tc.bindAddress, tc.bindNetwork, tc.profile)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(yamlStr).ToNot(BeEmpty())

			var config map[string]interface{}
			err = yaml.Unmarshal([]byte(yamlStr), &config)
			g.Expect(err).ToNot(HaveOccurred())

			g.Expect(config["apiVersion"]).To(Equal("config.openshift.io/v1"))
			g.Expect(config["kind"]).To(Equal("GenericControllerConfig"))

			servingInfo := config["servingInfo"].(map[string]interface{})
			g.Expect(servingInfo["bindAddress"]).To(Equal(tc.bindAddress))
			g.Expect(servingInfo["bindNetwork"]).To(Equal(tc.bindNetwork))
			g.Expect(servingInfo["minTLSVersion"]).To(Equal(tc.expectedMinTLS))

			if len(tc.expectedCipherSuites) == 0 {
				cipherSuites := servingInfo["cipherSuites"]
				g.Expect(cipherSuites).To(BeNil())
			} else {
				cipherSuites := servingInfo["cipherSuites"].([]interface{})
				g.Expect(cipherSuites).To(HaveLen(len(tc.expectedCipherSuites)))
				for i, expectedCipher := range tc.expectedCipherSuites {
					g.Expect(cipherSuites[i]).To(Equal(expectedCipher))
				}
			}
		})
	}
}
