package gcputil

import (
	"fmt"
	"regexp"
	"strings"
)

// maxZoneNameLength is the maximum length of a GCP Cloud DNS managed zone name.
const maxZoneNameLength = 63

// zoneNameSuffixReserve leaves room for the longest suffix appended to the base
// zone name ("-private" is 8 characters).
const zoneNameSuffixReserve = 8

// gcpZoneNameRegexp validates GCP Cloud DNS managed zone names: must start with a
// lowercase letter and contain only lowercase letters, numbers, and hyphens.
var gcpZoneNameRegexp = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// ValidateZoneName validates that a name meets GCP Cloud DNS managed zone naming constraints.
func ValidateZoneName(name string) error {
	if !gcpZoneNameRegexp.MatchString(name) {
		return fmt.Errorf("zone name %q is invalid: must start with a lowercase letter and contain only lowercase letters, numbers, and hyphens", name)
	}
	return nil
}

// truncateZoneName truncates a name to the specified maximum length.
func truncateZoneName(name string, maxLen int) string {
	if len(name) <= maxLen {
		return name
	}
	return name[:maxLen]
}

// baseZoneName converts a base domain into the zone-name prefix shared by the
// public and private ingress zones (dots replaced with hyphens, truncated to
// leave room for a suffix).
func baseZoneName(baseDomain string) string {
	s := strings.ReplaceAll(baseDomain, ".", "-")
	return truncateZoneName(s, maxZoneNameLength-zoneNameSuffixReserve)
}

// PublicIngressZoneName returns the GCP Cloud DNS managed-zone NAME of the public
// ingress zone for the given base domain. The input MUST be the raw
// HostedControlPlane base domain (hcp.Spec.DNS.BaseDomain), not a prefixed value,
// so that it matches the name the GCP Private Service Connect controller uses when
// it creates the zone.
func PublicIngressZoneName(baseDomain string) (string, error) {
	name := truncateZoneName(fmt.Sprintf("%s-public", baseZoneName(baseDomain)), maxZoneNameLength)
	if err := ValidateZoneName(name); err != nil {
		return "", fmt.Errorf("invalid public ingress zone name derived from baseDomain %q: %w", baseDomain, err)
	}
	return name, nil
}

// PrivateIngressZoneName returns the GCP Cloud DNS managed-zone NAME of the private
// ingress zone for the given base domain. See PublicIngressZoneName for input requirements.
func PrivateIngressZoneName(baseDomain string) (string, error) {
	name := truncateZoneName(fmt.Sprintf("%s-private", baseZoneName(baseDomain)), maxZoneNameLength)
	if err := ValidateZoneName(name); err != nil {
		return "", fmt.Errorf("invalid private ingress zone name derived from baseDomain %q: %w", baseDomain, err)
	}
	return name, nil
}

// HypershiftLocalZoneName returns the GCP Cloud DNS managed-zone NAME of the
// per-cluster hypershift.local private zone for the given cluster name.
func HypershiftLocalZoneName(clusterName string) (string, error) {
	name := truncateZoneName(fmt.Sprintf("%s-hypershift-local", clusterName), maxZoneNameLength)
	if err := ValidateZoneName(name); err != nil {
		return "", fmt.Errorf("invalid hypershift.local zone name derived from cluster %q: %w", clusterName, err)
	}
	return name, nil
}
