package config

import (
	"testing"

	. "github.com/onsi/gomega"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"

	"sigs.k8s.io/yaml"
)

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
			name:                 "When TLS profile is nil, it should use intermediate defaults",
			bindAddress:          "0.0.0.0:8443",
			bindNetwork:          "tcp",
			expectedMinTLS:       "VersionTLS12",
			expectedCipherSuites: intermediateCiphers,
		},
		{
			name:        "When using modern profile, it should set TLS 1.3 with no ciphers",
			bindAddress: "127.0.0.1:8443",
			bindNetwork: "tcp",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileModernType,
			},
			expectedMinTLS: "VersionTLS13",
		},
		{
			name:        "When using intermediate profile, it should set TLS 1.2 and intermediate ciphers",
			bindAddress: "0.0.0.0:6443",
			bindNetwork: "tcp4",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileIntermediateType,
			},
			expectedMinTLS:       "VersionTLS12",
			expectedCipherSuites: intermediateCiphers,
		},
		{
			name:        "When using old profile, it should set TLS 1.0 and old ciphers",
			bindAddress: "0.0.0.0:8443",
			bindNetwork: "tcp",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileOldType,
			},
			expectedMinTLS:       "VersionTLS10",
			expectedCipherSuites: oldCiphers,
		},
		{
			name:        "When using custom profile with valid ciphers, it should use custom TLS version and ciphers",
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
			name:                 "When using different bind address and network, it should set them correctly",
			bindAddress:          "192.168.1.1:9443",
			bindNetwork:          "unix",
			expectedMinTLS:       "VersionTLS12",
			expectedCipherSuites: intermediateCiphers,
		},
		{
			name:        "When using custom profile with unknown ciphers, it should result in no cipher suites",
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

			var config map[string]any
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

func TestSetGenericControllerConfig(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name              string
		existingData      map[string]string
		shouldPreserveKey string
	}{
		{
			name: "When ConfigMap is empty, it should set config.yaml",
		},
		{
			name:              "When ConfigMap has existing data, it should preserve other keys",
			existingData:      map[string]string{"other-key": "other-value"},
			shouldPreserveKey: "other-key",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			cm := &corev1.ConfigMap{Data: tc.existingData}

			err := SetGenericControllerConfig("0.0.0.0:8443", "tcp4", nil, cm)
			g.Expect(err).ToNot(HaveOccurred())

			g.Expect(cm.Data).To(HaveKey("config.yaml"))

			var config map[string]any
			err = yaml.Unmarshal([]byte(cm.Data["config.yaml"]), &config)
			g.Expect(err).ToNot(HaveOccurred())

			servingInfo := config["servingInfo"].(map[string]interface{})
			g.Expect(servingInfo["bindAddress"]).To(Equal("0.0.0.0:8443"))
			g.Expect(servingInfo["bindNetwork"]).To(Equal("tcp4"))

			if tc.shouldPreserveKey != "" {
				g.Expect(cm.Data).To(HaveKeyWithValue(tc.shouldPreserveKey, tc.existingData[tc.shouldPreserveKey]))
			}
		})
	}
}
