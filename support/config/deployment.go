package config

import (
	"fmt"
	"strings"

	configv1 "github.com/openshift/api/config/v1"
)

// TLSArgs returns TLS-related command-line arguments for a given TLS security profile.
// It returns a slice of strings containing --tls-min-version and --tls-cipher-suites flags
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

	if tlsMinVersion != "" {
		tlsArgs = append(tlsArgs, fmt.Sprintf("--tls-min-version=%s", tlsMinVersion))
	}
	if len(cipherSuites) != 0 {
		tlsArgs = append(tlsArgs, fmt.Sprintf("--tls-cipher-suites=%s", strings.Join(cipherSuites, ",")))
	}
	return tlsArgs, nil
}
