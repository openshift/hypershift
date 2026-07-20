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

func TestAppendTLSArgs(t *testing.T) {
	tests := []struct {
		name         string
		inputArgs    []string
		profile      *configv1.TLSSecurityProfile
		expectedArgs []string
	}{
		{
			name:      "When profile is nil it should append intermediate defaults",
			inputArgs: []string{"--namespace=test"},
			profile:   nil,
			expectedArgs: []string{
				"--namespace=test",
				"--tls-min-version=VersionTLS12",
				"--tls-cipher-suites=TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256",
			},
		},
		{
			name:      "When using Modern profile it should append only min-version",
			inputArgs: []string{"--namespace=test"},
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileModernType,
			},
			expectedArgs: []string{
				"--namespace=test",
				"--tls-min-version=VersionTLS13",
			},
		},
		{
			name:      "When using Intermediate profile it should append min-version and cipher-suites",
			inputArgs: []string{"--namespace=test"},
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileIntermediateType,
			},
			expectedArgs: []string{
				"--namespace=test",
				"--tls-min-version=VersionTLS12",
				"--tls-cipher-suites=TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256",
			},
		},
		{
			name:      "When using Custom profile with specific ciphers it should append custom TLS args",
			inputArgs: []string{"--namespace=test"},
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
				"--namespace=test",
				"--tls-min-version=VersionTLS12",
				"--tls-cipher-suites=TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
			},
		},
		{
			name:      "When input args is empty it should work correctly",
			inputArgs: []string{},
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileModernType,
			},
			expectedArgs: []string{
				"--tls-min-version=VersionTLS13",
			},
		},
		{
			name:      "When using Old profile it should append min-version and cipher-suites",
			inputArgs: []string{},
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileOldType,
			},
			expectedArgs: []string{
				"--tls-min-version=VersionTLS10",
				"--tls-cipher-suites=TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA,TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA,TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)

			result := AppendTLSArgs(test.inputArgs, test.profile)

			g.Expect(result).To(Equal(test.expectedArgs))
		})
	}
}
