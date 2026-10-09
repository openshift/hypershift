package config

import (
	"context"
	"crypto/tls"
	"fmt"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/library-go/pkg/crypto"

	ctrl "sigs.k8s.io/controller-runtime"

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

// fipsTLSGroups are the groups our services are allowed to use when running
// with FIPS enabled. X25519 and X25519MLKEM768 aren't yet formally validated
// by NIST, that may take some time. The default behavior for new Curves is to
// start as "not validated by NIST" so this list here works as an allow list.
var fipsTLSGroups = map[configv1.TLSGroup]struct{}{
	configv1.TLSGroupSecP256r1: {},
	configv1.TLSGroupSecP384r1: {},
	configv1.TLSGroupSecP521r1: {},
}

// TLSGroups returns the TLS groups that should be used based on the profile
// and if we must or not restrict the groups based on FIPS.
func TLSGroups(securityProfile *configv1.TLSSecurityProfile, fips bool) ([]configv1.TLSGroup, error) {
	groups, err := tlsGroupsFromProfile(securityProfile)
	if !fips || err != nil {
		return groups, err
	}
	return fipsApprovedTLSGroups(groups), nil
}

// fipsApprovedTLSGroups filters the provided list of TLS groups, only FIPS
// approved groups are returned.
func fipsApprovedTLSGroups(groups []configv1.TLSGroup) []configv1.TLSGroup {
	approved := make([]configv1.TLSGroup, 0, len(groups))
	for _, g := range groups {
		if _, ok := fipsTLSGroups[g]; !ok {
			continue
		}
		approved = append(approved, g)
	}
	return approved
}

// tlsGroupsFromProfile returns the configured TLS groups for the provided
// TLSSecurityProfile, if no Profile has been selected returns the Groups
// belonging to default profile.
func tlsGroupsFromProfile(securityProfile *configv1.TLSSecurityProfile) ([]configv1.TLSGroup, error) {
	// Directly returns the groups of the default profile type.
	if securityProfile == nil {
		return configv1.TLSProfiles[crypto.DefaultTLSProfileType].Groups, nil
	}

	// Return the predefined groups for the known types.
	if securityProfile.Type != configv1.TLSProfileCustomType {
		return configv1.TLSProfiles[securityProfile.Type].Groups, nil
	}

	if securityProfile.Custom == nil {
		return nil, fmt.Errorf("TLS profile type is Custom but Custom field is nil")
	}

	// Return the list of custom groups.
	return securityProfile.Custom.Groups, nil
}

func MinTLSVersion(securityProfile *configv1.TLSSecurityProfile) (string, error) {
	if securityProfile == nil {
		securityProfile = &configv1.TLSSecurityProfile{
			Type: configv1.TLSProfileIntermediateType,
		}
	}
	if securityProfile.Type == configv1.TLSProfileCustomType {
		if securityProfile.Custom == nil {
			return "", fmt.Errorf("TLS profile type is Custom but Custom field is nil")
		}
		return string(securityProfile.Custom.MinTLSVersion), nil
	}
	return string(configv1.TLSProfiles[securityProfile.Type].MinTLSVersion), nil
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

func CipherSuites(securityProfile *configv1.TLSSecurityProfile) ([]string, error) {
	if securityProfile == nil {
		securityProfile = &configv1.TLSSecurityProfile{
			Type: configv1.TLSProfileIntermediateType,
		}
	}
	var ciphers []string
	if securityProfile.Type == configv1.TLSProfileCustomType {
		if securityProfile.Custom == nil {
			return nil, fmt.Errorf("TLS profile type is Custom but Custom field is nil")
		}
		ciphers = securityProfile.Custom.Ciphers
	} else {
		ciphers = configv1.TLSProfiles[securityProfile.Type].Ciphers
	}
	return OpenSSLToIANACipherSuites(ciphers), nil
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
	minVersion, err := MinTLSVersion(apiServerConfig.Spec.TLSSecurityProfile)
	if err != nil {
		return nil, err
	}
	version, err := crypto.TLSVersion(minVersion)
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
	cipherSuites, err := CipherSuites(apiServerConfig.Spec.TLSSecurityProfile)
	if err != nil {
		return nil, err
	}
	var suites []uint16
	for _, suiteString := range cipherSuites {
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
