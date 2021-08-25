package config

import (
	configv1 "github.com/openshift/api/config/v1"
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
	"ECDHE-ECDSA-AES128-SHA256":     "TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA256",       // 0xC0,0x23
	"ECDHE-RSA-AES128-SHA256":       "TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA256",         // 0xC0,0x27
	"AES128-GCM-SHA256":             "TLS_RSA_WITH_AES_128_GCM_SHA256",               // 0x00,0x9C
	"AES256-GCM-SHA384":             "TLS_RSA_WITH_AES_256_GCM_SHA384",               // 0x00,0x9D
	"AES128-SHA256":                 "TLS_RSA_WITH_AES_128_CBC_SHA256",               // 0x00,0x3C

	// TLS 1
	"ECDHE-ECDSA-AES128-SHA": "TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA", // 0xC0,0x09
	"ECDHE-RSA-AES128-SHA":   "TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA",   // 0xC0,0x13
	"ECDHE-ECDSA-AES256-SHA": "TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA", // 0xC0,0x0A
	"ECDHE-RSA-AES256-SHA":   "TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA",   // 0xC0,0x14

	// SSL 3
	"AES128-SHA":   "TLS_RSA_WITH_AES_128_CBC_SHA",  // 0x00,0x2F
	"AES256-SHA":   "TLS_RSA_WITH_AES_256_CBC_SHA",  // 0x00,0x35
	"DES-CBC3-SHA": "TLS_RSA_WITH_3DES_EDE_CBC_SHA", // 0x00,0x0A
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
