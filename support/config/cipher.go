package config

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/library-go/pkg/crypto"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/yaml"

	"go.etcd.io/etcd/client/pkg/v3/tlsutil"
)

// openSSLToIANACiphersMap maps OpenSSL cipher suite names to IANA names
// ref: https://www.iana.org/assignments/tls-parameters/tls-parameters.xml
var openSSLToIANACiphersMap = map[string]string{
	// TLS 1.3 ciphers - not configurable in go 1.13, all of them are used in TLSv1.3 flows
	//	"TLS_AES_128_GCM_SHA256":       "TLS_AES_128_GCM_SHA256",       // 0x13,0x01
	//	"TLS_AES_256_GCM_SHA384":       "TLS_AES_256_GCM_SHA384",       // 0x13,0x02
	//	"TLS_CHACHA20_POLY1305_SHA256": "TLS_CHACHA20_POLY1305_SHA256", // 0x13,0x03

	// TLS 1.2
	"ECDHE-ECDSA-AES128-GCM-SHA256": "TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256",       // 0xC0,0x2B
	"ECDHE-RSA-AES128-GCM-SHA256":   "TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",         // 0xC0,0x2F
	"ECDHE-ECDSA-AES256-GCM-SHA384": "TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384",       // 0xC0,0x2C
	"ECDHE-RSA-AES256-GCM-SHA384":   "TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384",         // 0xC0,0x30
	"ECDHE-ECDSA-CHACHA20-POLY1305": "TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256", // 0xCC,0xA9
	"ECDHE-RSA-CHACHA20-POLY1305":   "TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256",   // 0xCC,0xA8

	// TLS 1
	"ECDHE-ECDSA-AES128-SHA": "TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA", // 0xC0,0x09
	"ECDHE-RSA-AES128-SHA":   "TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA",   // 0xC0,0x13
	"ECDHE-ECDSA-AES256-SHA": "TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA", // 0xC0,0x0A
	"ECDHE-RSA-AES256-SHA":   "TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA",   // 0xC0,0x14
}

func MinTLSVersion(securityProfile *configv1.TLSSecurityProfile) string {
	if securityProfile == nil {
		securityProfile = &configv1.TLSSecurityProfile{
			Type: configv1.TLSProfileIntermediateType,
		}
	}
	if securityProfile.Type == configv1.TLSProfileCustomType {
		return string(securityProfile.Custom.MinTLSVersion)
	}
	return string(configv1.TLSProfiles[securityProfile.Type].MinTLSVersion)
}

// OpenSSLToIANACipherSuites maps input OpenSSL Cipher Suite names to their
// IANA counterparts.
// Unknown ciphers are left out.
func OpenSSLToIANACipherSuites(ciphers []string) []string {
	ianaCiphers := make([]string, 0, len(ciphers))

	for _, c := range ciphers {
		ianaCipher, found := openSSLToIANACiphersMap[c]
		if found {
			ianaCiphers = append(ianaCiphers, ianaCipher)
		}
	}

	return ianaCiphers
}

func CipherSuites(securityProfile *configv1.TLSSecurityProfile) []string {
	if securityProfile == nil {
		securityProfile = &configv1.TLSSecurityProfile{
			Type: configv1.TLSProfileIntermediateType,
		}
	}
	var ciphers []string
	if securityProfile.Type == configv1.TLSProfileCustomType {
		ciphers = securityProfile.Custom.Ciphers
	} else {
		ciphers = configv1.TLSProfiles[securityProfile.Type].Ciphers
	}
	return OpenSSLToIANACipherSuites(ciphers)
}

// SupportedEtcdCipherSuites filters the input cipher suites to only those supported by
// etcd. It validates each cipher against etcd's tlsutil.GetCipherSuite(). Unknown suites
// are logged.
func SupportedEtcdCipherSuites(ctx context.Context, cipherSuites []string) []string {
	log := ctrl.LoggerFrom(ctx)
	allowedCiphers := []string{}
	for _, cipher := range cipherSuites {
		if _, ok := tlsutil.GetCipherSuite(cipher); !ok {
			log.Info("cipher is not supported for use with etcd, skipping", "cipher", cipher)
			continue
		}
		allowedCiphers = append(allowedCiphers, cipher)
	}
	return allowedCiphers
}

// SetMinTLSVersionUsingAPIServer returns a function capable of setting the min
// tls version on a provided tls config struct. If the provided api server has
// an invalid tls version this function returns an error.
func SetMinTLSVersionUsingAPIServer(apiServerConfig *configv1.APIServer) (func(*tls.Config), error) {
	version, err := crypto.TLSVersion(MinTLSVersion(apiServerConfig.Spec.TLSSecurityProfile))
	if err != nil {
		return nil, err
	}
	return func(tlsConfig *tls.Config) {
		tlsConfig.MinVersion = version
	}, nil
}

// SetCipherSuitesUsingAPIServer returns a function that is capable of setting
// the right cipher suites on a tls config using the provided api server as
// input. Returns an error if the provided api server contains an invalid
// suite.
func SetCipherSuitesUsingAPIServer(apiServerConfig *configv1.APIServer) (func(*tls.Config), error) {
	var suites []uint16
	for _, suiteString := range CipherSuites(apiServerConfig.Spec.TLSSecurityProfile) {
		suite, err := crypto.CipherSuite(suiteString)
		if err != nil {
			return nil, err
		}
		suites = append(suites, suite)
	}
	return func(tlsConfig *tls.Config) {
		tlsConfig.CipherSuites = suites
	}, nil
}

// BuildGenericControllerConfigData builds a GenericControllerConfig YAML string
// with the specified bind address, bind network, and TLS security profile.
// This is used by various control plane operators to configure their serving info.
func BuildGenericControllerConfigData(bindAddress, bindNetwork string, profile *configv1.TLSSecurityProfile) (string, error) {
	controllerConfig := configv1.GenericControllerConfig{
		ServingInfo: configv1.HTTPServingInfo{
			ServingInfo: configv1.ServingInfo{
				BindAddress:   bindAddress,
				BindNetwork:   bindNetwork,
				CipherSuites:  CipherSuites(profile),
				MinTLSVersion: MinTLSVersion(profile),
			},
		},
	}

	asJSON, err := json.Marshal(controllerConfig)
	if err != nil {
		return "", fmt.Errorf("failed to json marshal config: %w", err)
	}

	asMap := map[string]any{}
	if err := json.Unmarshal(asJSON, &asMap); err != nil {
		return "", fmt.Errorf("failed to json unmarshal config: %w", err)
	}

	asMap["apiVersion"] = configv1.GroupVersion.String()
	asMap["kind"] = "GenericControllerConfig"

	data, err := yaml.Marshal(asMap)
	if err != nil {
		return "", fmt.Errorf("failed to yaml marshal config: %w", err)
	}

	return string(data), nil
}
