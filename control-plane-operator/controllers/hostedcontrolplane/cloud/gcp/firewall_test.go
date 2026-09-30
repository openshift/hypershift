package gcp

import (
	"encoding/json"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	. "github.com/onsi/gomega"

	"google.golang.org/api/compute/v1"
)

func TestDesiredFirewall(t *testing.T) {
	const infraID = "example-abcde"
	const netLink = "projects/my-project-123/global/networks/example-abcde-network"

	t.Run("When network type is OVNKubernetes, it should include UDP 6081", func(t *testing.T) {
		g := NewWithT(t)
		fw := desiredFirewall(infraID, netLink, hyperv1.OVNKubernetes)

		g.Expect(fw.Name).To(Equal("example-abcde-internal-cluster"))
		g.Expect(fw.Direction).To(Equal("INGRESS"))
		g.Expect(fw.Priority).To(Equal(int64(1000)))
		g.Expect(fw.Disabled).To(BeFalse())
		g.Expect(fw.SourceTags).To(ConsistOf("example-abcde-worker"))
		g.Expect(fw.TargetTags).To(ConsistOf("example-abcde-worker"))
		g.Expect(fw.SourceRanges).To(BeEmpty())

		// ForceSendFields must include Disabled and SourceRanges so disabled:false
		// is sent on the wire and any injected CIDRs are cleared.
		g.Expect(fw.ForceSendFields).To(ContainElements("Disabled", "SourceRanges"))

		udp := allowedByProto(fw.Allowed, "udp")
		g.Expect(udp).ToNot(BeNil())
		g.Expect(udp.Ports).To(ContainElement("6081"))
		g.Expect(udp.Ports).To(ContainElements("9000-9999", "30000-32767"))

		tcp := allowedByProto(fw.Allowed, "tcp")
		g.Expect(tcp).ToNot(BeNil())
		g.Expect(tcp.Ports).To(ConsistOf("10250", "9000-9999", "30000-32767"))
	})

	t.Run("When network type is not OVNKubernetes, it should omit UDP 6081", func(t *testing.T) {
		g := NewWithT(t)
		fw := desiredFirewall(infraID, netLink, hyperv1.Other)

		udp := allowedByProto(fw.Allowed, "udp")
		g.Expect(udp).ToNot(BeNil())
		g.Expect(udp.Ports).ToNot(ContainElement("6081"))
		g.Expect(udp.Ports).To(ConsistOf("9000-9999", "30000-32767"))
	})
}

func allowedByProto(allowed []*compute.FirewallAllowed, proto string) *compute.FirewallAllowed {
	for _, a := range allowed {
		if a.IPProtocol == proto {
			return a
		}
	}
	return nil
}

func TestOwnershipMarker(t *testing.T) {
	g := NewWithT(t)
	marker, err := ownershipMarker("example-abcde")
	g.Expect(err).ToNot(HaveOccurred())

	var parsed map[string]string
	g.Expect(json.Unmarshal([]byte(marker), &parsed)).To(Succeed())
	g.Expect(parsed).To(HaveKeyWithValue("hypershift.openshift.io/managed-by", "control-plane-operator"))
	g.Expect(parsed).To(HaveKeyWithValue("hypershift.openshift.io/infra-id", "example-abcde"))
}

func TestIsOwnedBy(t *testing.T) {
	validMarker, _ := ownershipMarker("example-abcde")

	tests := []struct {
		name        string
		description string
		infraID     string
		expected    bool
	}{
		{
			name:        "When description carries a matching marker, it should be owned",
			description: validMarker,
			infraID:     "example-abcde",
			expected:    true,
		},
		{
			name:        "When description is empty, it should not be owned",
			description: "",
			infraID:     "example-abcde",
			expected:    false,
		},
		{
			name:        "When description is not JSON, it should not be owned",
			description: "Allow kubelet API access",
			infraID:     "example-abcde",
			expected:    false,
		},
		{
			name:        "When infra-id in the marker mismatches, it should not be owned",
			description: validMarker,
			infraID:     "other-infra",
			expected:    false,
		},
		{
			name:        "When managed-by is a different owner, it should not be owned",
			description: `{"hypershift.openshift.io/managed-by":"someone-else","hypershift.openshift.io/infra-id":"example-abcde"}`,
			infraID:     "example-abcde",
			expected:    false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			fw := &compute.Firewall{Description: tc.description}
			g.Expect(isOwnedBy(fw, tc.infraID)).To(Equal(tc.expected))
		})
	}
}

func TestNetworkRefsEqual(t *testing.T) {
	tests := []struct {
		name     string
		a        string
		b        string
		expected bool
	}{
		{
			name:     "When one is a full self-link and the other partial, it should be equal",
			a:        "https://www.googleapis.com/compute/v1/projects/my-project-123/global/networks/example-network",
			b:        "projects/my-project-123/global/networks/example-network",
			expected: true,
		},
		{
			name:     "When both are identical partial refs, it should be equal",
			a:        "projects/my-project-123/global/networks/example-network",
			b:        "projects/my-project-123/global/networks/example-network",
			expected: true,
		},
		{
			name:     "When network names differ, it should not be equal",
			a:        "projects/my-project-123/global/networks/example-network",
			b:        "projects/my-project-123/global/networks/other-network",
			expected: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(networkRefsEqual(tc.a, tc.b)).To(Equal(tc.expected))
		})
	}
}

func TestFirewallMatchesDesired(t *testing.T) {
	const infraID = "example-abcde"
	const netLink = "projects/my-project-123/global/networks/example-abcde-network"
	desired := desiredFirewall(infraID, netLink, hyperv1.OVNKubernetes)

	t.Run("When an existing rule equals desired, it should match", func(t *testing.T) {
		g := NewWithT(t)
		existing := desiredFirewall(infraID, netLink, hyperv1.OVNKubernetes)
		existing.Network = netLink
		g.Expect(firewallMatchesDesired(existing, desired)).To(BeTrue())
	})

	t.Run("When an existing rule has injected source ranges, it should not match", func(t *testing.T) {
		g := NewWithT(t)
		existing := desiredFirewall(infraID, netLink, hyperv1.OVNKubernetes)
		existing.SourceRanges = []string{"10.0.0.0/8"}
		g.Expect(firewallMatchesDesired(existing, desired)).To(BeFalse())
	})

	t.Run("When an existing rule is disabled, it should not match", func(t *testing.T) {
		g := NewWithT(t)
		existing := desiredFirewall(infraID, netLink, hyperv1.OVNKubernetes)
		existing.Disabled = true
		g.Expect(firewallMatchesDesired(existing, desired)).To(BeFalse())
	})

	t.Run("When an existing rule is missing UDP 6081, it should not match", func(t *testing.T) {
		g := NewWithT(t)
		existing := desiredFirewall(infraID, netLink, hyperv1.Other)
		g.Expect(firewallMatchesDesired(existing, desired)).To(BeFalse())
	})
}

func TestIsCompatible(t *testing.T) {
	const netLink = "projects/my-project-123/global/networks/example-network"

	tests := []struct {
		name     string
		firewall *compute.Firewall
		expected bool
	}{
		{
			name:     "When the rule is INGRESS ALLOW in the right VPC, it should be compatible",
			firewall: &compute.Firewall{Direction: "INGRESS", Network: netLink},
			expected: true,
		},
		{
			name:     "When the rule is a DENY rule, it should be incompatible",
			firewall: &compute.Firewall{Direction: "INGRESS", Network: netLink, Denied: []*compute.FirewallDenied{{IPProtocol: "tcp"}}},
			expected: false,
		},
		{
			name:     "When the rule is EGRESS, it should be incompatible",
			firewall: &compute.Firewall{Direction: "EGRESS", Network: netLink},
			expected: false,
		},
		{
			name:     "When the rule is in a different VPC, it should be incompatible",
			firewall: &compute.Firewall{Direction: "INGRESS", Network: "projects/my-project-123/global/networks/other"},
			expected: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			ok, _ := isCompatible(tc.firewall, netLink)
			g.Expect(ok).To(Equal(tc.expected))
		})
	}
}
