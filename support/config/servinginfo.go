package config

import (
	"fmt"

	configv1 "github.com/openshift/api/config/v1"
)

// ApplyServingInfoFromTLSProfile sets the MinTLSVersion and CipherSuites fields on the provided
// ServingInfo struct based on the given TLS security profile.
// This is a convenience function for components that use ServingInfo configuration.
func ApplyServingInfoFromTLSProfile(servingInfo *configv1.ServingInfo, profile *configv1.TLSSecurityProfile) error {
	minTLSVersion, err := MinTLSVersion(profile)
	if err != nil {
		return fmt.Errorf("failed to get min TLS version: %w", err)
	}
	servingInfo.MinTLSVersion = minTLSVersion

	cipherSuites, err := CipherSuites(profile)
	if err != nil {
		return fmt.Errorf("failed to get cipher suites: %w", err)
	}
	servingInfo.CipherSuites = cipherSuites

	return nil
}
