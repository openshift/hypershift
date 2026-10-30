package gcp

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"google.golang.org/api/compute/v1"
)

// rfc1035NameRegexp matches GCP's RFC1035 resource-name grammar: it must start
// with a lowercase letter, contain only lowercase letters, digits, and hyphens,
// and end with a letter or digit.
var rfc1035NameRegexp = regexp.MustCompile(`^[a-z]([-a-z0-9]*[a-z0-9])?$`)

const (
	// managedByMarkerKey and infraIDMarkerKey are the JSON keys written into the
	// firewall description to establish control-plane-operator ownership.
	managedByMarkerKey = "hypershift.openshift.io/managed-by"
	infraIDMarkerKey   = "hypershift.openshift.io/infra-id"

	// maxFirewallNameLength is GCP's RFC1035 limit on firewall resource names. A
	// derived name longer than this can never be created, so it is validated
	// up-front rather than surfaced as a doomed 400.
	maxFirewallNameLength = 63

	// managedByValue is the value written under managedByMarkerKey.
	managedByValue = "control-plane-operator"

	// firewallPriority is the priority of the managed worker firewall rule.
	firewallPriority = 1000

	// geneveOverlayPort is the UDP port used by the OVN-Kubernetes Geneve overlay.
	geneveOverlayPort = "6081"
)

// firewallRuleName returns the name of the managed worker firewall rule for the
// given infra ID.
func firewallRuleName(infraID string) string {
	return fmt.Sprintf("%s-internal-cluster", infraID)
}

// validateFirewallName checks that the derived firewall name satisfies GCP's
// RFC1035 grammar and length limit. A violation can never be created, so it is a
// terminal, actionable configuration error rather than a transient state.
func validateFirewallName(name string) error {
	if len(name) > maxFirewallNameLength {
		return fmt.Errorf("firewall name %q is %d characters, exceeding GCP's %d-character RFC1035 limit", name, len(name), maxFirewallNameLength)
	}
	if !rfc1035NameRegexp.MatchString(name) {
		return fmt.Errorf("firewall name %q does not match GCP's RFC1035 name grammar (lowercase letters, digits, and hyphens; must start with a letter)", name)
	}
	return nil
}

// workerTag returns the network tag applied to worker nodes, used as both the
// source and target selector of the managed firewall rule.
func workerTag(infraID string) string {
	return fmt.Sprintf("%s-worker", infraID)
}

// ownershipMarker builds the JSON ownership marker written into the firewall
// description on the initial create.
func ownershipMarker(infraID string) (string, error) {
	marker := map[string]string{
		managedByMarkerKey: managedByValue,
		infraIDMarkerKey:   infraID,
	}
	b, err := json.Marshal(marker)
	if err != nil {
		return "", fmt.Errorf("failed to marshal ownership marker: %w", err)
	}
	return string(b), nil
}

// isOwnedBy reports whether the firewall's description carries a well-formed
// ownership marker matching the control-plane-operator and the given infra ID.
// A missing, malformed, or mismatched marker means the rule is not owned by us.
func isOwnedBy(firewall *compute.Firewall, infraID string) bool {
	if firewall.Description == "" {
		return false
	}
	var marker map[string]string
	if err := json.Unmarshal([]byte(firewall.Description), &marker); err != nil {
		return false
	}
	return marker[managedByMarkerKey] == managedByValue && marker[infraIDMarkerKey] == infraID
}

// desiredAllowed returns the ALLOW rule set required by GCP-1221. The NodePort
// range is the effective, possibly-customized spec.configuration.network.
// serviceNodePortRange (already GCP "min-max" form) rather than a hard-coded
// default, so custom ranges are not silently blocked. UDP 6081 (Geneve overlay)
// is included only for OVNKubernetes.
func desiredAllowed(networkType hyperv1.NetworkType, nodePortRange string) []*compute.FirewallAllowed {
	udpPorts := []string{"9000-9999", nodePortRange}
	if networkType == hyperv1.OVNKubernetes {
		udpPorts = append(udpPorts, geneveOverlayPort)
	}
	return []*compute.FirewallAllowed{
		{
			IPProtocol: "tcp",
			Ports:      []string{"10250", "9000-9999", nodePortRange},
		},
		{
			IPProtocol: "udp",
			Ports:      udpPorts,
		},
	}
}

// desiredFirewall builds the target firewall resource. The description (with the
// ownership marker) is only written on create; updates never rewrite it, so the
// marker acts as a stable ownership record.
func desiredFirewall(infraID, networkSelfLink string, networkType hyperv1.NetworkType, nodePortRange string) *compute.Firewall {
	tag := workerTag(infraID)
	return &compute.Firewall{
		Name:       firewallRuleName(infraID),
		Network:    networkSelfLink,
		Direction:  "INGRESS",
		Priority:   firewallPriority,
		Allowed:    desiredAllowed(networkType, nodePortRange),
		SourceTags: []string{tag},
		TargetTags: []string{tag},
		// Disabled is a meaningful false: send it explicitly so a previously
		// disabled rule is re-enabled on update.
		Disabled: false,
		// The managed rule is strictly tag-scoped. Every other (mutually
		// exclusive) source/target selector must be cleared on the wire, even
		// when already empty, so that a Patch of an owned rule that was
		// hand-edited to use CIDRs or service accounts removes those incompatible
		// selectors instead of adding tags alongside them (which GCP rejects with
		// a persistent 400). All of these fields are omitempty, so they only
		// clear when listed in ForceSendFields.
		ForceSendFields: []string{
			"Disabled",
			"SourceRanges",
			"DestinationRanges",
			"SourceServiceAccounts",
			"TargetServiceAccounts",
		},
	}
}

// normalizeNetworkRef reduces a full or partial GCP network self-link to its
// trailing "<project>/global/networks/<name>" form so full and partial
// references compare equal.
func normalizeNetworkRef(ref string) string {
	ref = strings.TrimSuffix(ref, "/")
	idx := strings.Index(ref, "/projects/")
	if idx >= 0 {
		ref = ref[idx+1:]
	}
	return ref
}

// networkRefsEqual compares two network references, tolerating full vs partial
// self-link forms.
func networkRefsEqual(a, b string) bool {
	na, nb := normalizeNetworkRef(a), normalizeNetworkRef(b)
	if na == nb {
		return true
	}
	// The name-only fallback lets a bare network name compare equal to a
	// fully-qualified reference. It must only apply when at least one operand is
	// actually bare (no project/path qualifier): if both are qualified, they were
	// already required to match exactly above, so two same-name references in
	// different projects (e.g. projects/a/.../shared vs projects/b/.../shared)
	// must NOT be treated as equal — that would let us patch a firewall in the
	// wrong VPC/project.
	if isQualifiedNetworkRef(na) && isQualifiedNetworkRef(nb) {
		return false
	}
	return lastPathSegment(na) == lastPathSegment(nb) && lastPathSegment(na) != ""
}

// isQualifiedNetworkRef reports whether a normalized network reference carries a
// project/path qualifier (as opposed to being a bare network name).
func isQualifiedNetworkRef(ref string) bool {
	return strings.Contains(ref, "/")
}

func lastPathSegment(s string) string {
	if i := strings.LastIndex(s, "/"); i >= 0 {
		return s[i+1:]
	}
	return s
}

// isCompatible reports whether an existing rule is compatible enough for us to
// reconcile in place: it must be in the desired VPC, be an INGRESS ALLOW rule
// (never a DENY rule). Incompatible same-name resources are left untouched.
func isCompatible(firewall *compute.Firewall, desiredNetworkSelfLink string) (bool, string) {
	if len(firewall.Denied) > 0 {
		return false, "existing rule is a DENY rule"
	}
	if firewall.Direction != "" && firewall.Direction != "INGRESS" {
		return false, fmt.Sprintf("existing rule direction is %q, expected INGRESS", firewall.Direction)
	}
	if !networkRefsEqual(firewall.Network, desiredNetworkSelfLink) {
		return false, fmt.Sprintf("existing rule is in network %q, expected %q", firewall.Network, desiredNetworkSelfLink)
	}
	return true, ""
}

// firewallMatchesDesired reports whether the existing rule already matches the
// desired traffic policy, selectors, priority, and enabled state. The
// description/marker is intentionally not compared (it is create-only).
func firewallMatchesDesired(existing, desired *compute.Firewall) bool {
	if existing.Priority != desired.Priority {
		return false
	}
	if existing.Disabled != desired.Disabled {
		return false
	}
	if existing.Direction != "" && existing.Direction != desired.Direction {
		return false
	}
	// The managed rule is strictly tag-scoped. Any other (mutually exclusive)
	// source/target selector present on the existing rule is drift that must be
	// cleared, so report a mismatch to trigger a repairing Patch. Skipping these
	// checks would either leave incompatible selectors in place or let an
	// otherwise-matching rule pass while still carrying a selector that conflicts
	// with our tags.
	if len(existing.SourceRanges) != 0 {
		return false
	}
	if len(existing.DestinationRanges) != 0 {
		return false
	}
	if len(existing.SourceServiceAccounts) != 0 {
		return false
	}
	if len(existing.TargetServiceAccounts) != 0 {
		return false
	}
	if !stringSetsEqual(existing.SourceTags, desired.SourceTags) {
		return false
	}
	if !stringSetsEqual(existing.TargetTags, desired.TargetTags) {
		return false
	}
	return allowedEqual(existing.Allowed, desired.Allowed)
}

// allowedEqual compares two ALLOW rule sets independent of ordering.
func allowedEqual(a, b []*compute.FirewallAllowed) bool {
	if len(a) != len(b) {
		return false
	}
	toMap := func(rules []*compute.FirewallAllowed) map[string][]string {
		m := make(map[string][]string, len(rules))
		for _, r := range rules {
			proto := strings.ToLower(r.IPProtocol)
			ports := append([]string(nil), r.Ports...)
			sort.Strings(ports)
			m[proto] = ports
		}
		return m
	}
	ma, mb := toMap(a), toMap(b)
	if len(ma) != len(mb) {
		return false
	}
	for proto, portsA := range ma {
		portsB, ok := mb[proto]
		if !ok || !stringSlicesEqual(portsA, portsB) {
			return false
		}
	}
	return true
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// stringSetsEqual compares two string slices as sets (order independent).
func stringSetsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	sa := append([]string(nil), a...)
	sb := append([]string(nil), b...)
	sort.Strings(sa)
	sort.Strings(sb)
	return stringSlicesEqual(sa, sb)
}
