package config

import (
	"fmt"
	"strconv"
	"strings"

	configv1 "github.com/openshift/api/config/v1"
	libgocrypto "github.com/openshift/library-go/pkg/crypto"
)

// TLSArgs returns TLS-related command-line arguments for a given TLS security profile.
// It returns a slice of strings containing --tls-min-version,
// --tls-cipher-suites, and --tls-curve-preferences flags
// based on the provided profile. The caller is responsible for appending these to their
// argument list using the spread operator (...).
func TLSArgs(profile *configv1.TLSSecurityProfile) ([]string, error) {
	var tlsArgs []string
	tlsMinVersion, err := MinTLSVersion(profile)
	if err != nil {
		return nil, fmt.Errorf("failed to get min TLS version: %w", err)
	}

	cipherSuites, err := CipherSuites(profile)
	if err != nil {
		return nil, fmt.Errorf("failed to get cipher suites: %w", err)
	}

	groups, err := TLSGroups(profile)
	if err != nil {
		return nil, fmt.Errorf("failed to get TLS groups: %w", err)
	}
	curveIDs, unrecognizedGroups := libgocrypto.TLSGroupsToCurveIDs(groups)
	if len(unrecognizedGroups) > 0 {
		return nil, fmt.Errorf("unrecognized TLS groups: %v", unrecognizedGroups)
	}

	if tlsMinVersion != "" {
		tlsArgs = append(tlsArgs, fmt.Sprintf("--tls-min-version=%s", tlsMinVersion))
	}
	if len(cipherSuites) != 0 {
		tlsArgs = append(tlsArgs, fmt.Sprintf("--tls-cipher-suites=%s", strings.Join(cipherSuites, ",")))
	}
	if len(curveIDs) != 0 {
		curvePreferences := make([]string, 0, len(curveIDs))
		for _, curveID := range curveIDs {
			curvePreferences = append(curvePreferences, strconv.Itoa(int(curveID)))
		}
		tlsArgs = append(tlsArgs, fmt.Sprintf("--tls-curve-preferences=%s", strings.Join(curvePreferences, ",")))
	}
	return tlsArgs, nil
}

func TLSGroups(profile *configv1.TLSSecurityProfile) ([]configv1.TLSGroup, error) {
	if profile == nil {
		profile = &configv1.TLSSecurityProfile{Type: configv1.TLSProfileIntermediateType}
	}
	if profile.Type == configv1.TLSProfileCustomType {
		if profile.Custom == nil {
			return nil, fmt.Errorf("TLS profile type is Custom but Custom field is nil")
		}
		return profile.Custom.Groups, nil
	}
	return configv1.TLSProfiles[profile.Type].Groups, nil
}
