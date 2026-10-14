package gcp

import (
	"encoding/json"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/googleapis/gax-go/v2/apierror"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
)

// testNodePortRange is the default NodePort range used across firewall tests.
const testNodePortRange = "30000-32767"

func TestDesiredFirewall(t *testing.T) {
	const infraID = "example-abcde"
	const netLink = "projects/my-project-123/global/networks/example-abcde-network"

	t.Run("When network type is OVNKubernetes, it should include UDP 6081", func(t *testing.T) {
		g := NewWithT(t)
		fw := desiredFirewall(infraID, netLink, hyperv1.OVNKubernetes, testNodePortRange)

		g.Expect(fw.Name).To(Equal("example-abcde-internal-cluster"))
		g.Expect(fw.Direction).To(Equal("INGRESS"))
		g.Expect(fw.Priority).To(Equal(int64(1000)))
		g.Expect(fw.Disabled).To(BeFalse())
		g.Expect(fw.SourceTags).To(ConsistOf("example-abcde-worker"))
		g.Expect(fw.TargetTags).To(ConsistOf("example-abcde-worker"))
		g.Expect(fw.SourceRanges).To(BeEmpty())

		// ForceSendFields must include Disabled so disabled:false is sent on the
		// wire, plus every mutually-exclusive selector field so a Patch of an
		// owned rule clears any injected CIDRs or service-account selectors
		// instead of leaving them alongside our tags (which GCP rejects with 400).
		g.Expect(fw.ForceSendFields).To(ContainElements(
			"Disabled",
			"SourceRanges",
			"DestinationRanges",
			"SourceServiceAccounts",
			"TargetServiceAccounts",
		))

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
		fw := desiredFirewall(infraID, netLink, hyperv1.Other, testNodePortRange)

		udp := allowedByProto(fw.Allowed, "udp")
		g.Expect(udp).ToNot(BeNil())
		g.Expect(udp.Ports).ToNot(ContainElement("6081"))
		g.Expect(udp.Ports).To(ConsistOf("9000-9999", "30000-32767"))
	})

	t.Run("When the NodePort range is customized, it should use it for TCP and UDP", func(t *testing.T) {
		g := NewWithT(t)
		const customRange = "25000-35000"
		fw := desiredFirewall(infraID, netLink, hyperv1.OVNKubernetes, customRange)

		tcp := allowedByProto(fw.Allowed, "tcp")
		g.Expect(tcp).ToNot(BeNil())
		g.Expect(tcp.Ports).To(ContainElement(customRange))
		g.Expect(tcp.Ports).ToNot(ContainElement("30000-32767"))

		udp := allowedByProto(fw.Allowed, "udp")
		g.Expect(udp).ToNot(BeNil())
		g.Expect(udp.Ports).To(ContainElement(customRange))
		g.Expect(udp.Ports).ToNot(ContainElement("30000-32767"))
		// The overlay port is still present alongside the custom range for OVN.
		g.Expect(udp.Ports).To(ContainElement("6081"))
	})
}

func TestIsTransientBadRequest(t *testing.T) {
	tests := []struct {
		name     string
		reason   string
		expected bool
	}{
		{
			name:     "When the reason is RESOURCE_NOT_READY (upper snake), it should be transient",
			reason:   "RESOURCE_NOT_READY",
			expected: true,
		},
		{
			name:     "When the reason is resourceNotReady (camel), it should be transient",
			reason:   "resourceNotReady",
			expected: true,
		},
		{
			name:     "When the reason is RESOURCE_IN_USE_BY_ANOTHER_RESOURCE (catalog enum), it should be transient",
			reason:   "RESOURCE_IN_USE_BY_ANOTHER_RESOURCE",
			expected: true,
		},
		{
			name:     "When the reason is resourceInUseByAnotherResource (compute v1 REST spelling), it should be transient",
			reason:   "resourceInUseByAnotherResource",
			expected: true,
		},
		{
			name:     "When the reason is a validation error, it should be terminal",
			reason:   "invalid",
			expected: false,
		},
		{
			name:     "When there is no reason, it should be terminal",
			reason:   "",
			expected: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			err := &googleapi.Error{Code: 400, Errors: []googleapi.ErrorItem{{Reason: tc.reason}}}
			g.Expect(isTransientBadRequest(err)).To(Equal(tc.expected))
		})
	}

	t.Run("When a response mixes a transient and a terminal reason, it should be terminal", func(t *testing.T) {
		g := NewWithT(t)
		// A single terminal reason means the request cannot succeed on retry, so
		// the whole response must be classified terminal even though one reason is
		// transient.
		err := &googleapi.Error{Code: 400, Errors: []googleapi.ErrorItem{
			{Reason: "resourceNotReady"},
			{Reason: "invalid"},
		}}
		g.Expect(isTransientBadRequest(err)).To(BeFalse())
	})

	t.Run("When a response has multiple transient reasons, it should be transient", func(t *testing.T) {
		g := NewWithT(t)
		err := &googleapi.Error{Code: 400, Errors: []googleapi.ErrorItem{
			{Reason: "resourceNotReady"},
			{Reason: "resourceInUseByAnotherResource"},
		}}
		g.Expect(isTransientBadRequest(err)).To(BeTrue())
	})

	t.Run("When the transient reason is only in the structured ErrorInfo, it should be transient", func(t *testing.T) {
		g := NewWithT(t)
		// Mirror how the compute/v1 REST client surfaces a structured ErrorInfo: a
		// *googleapi.Error whose JSON Body carries the v2 error schema with a
		// google.rpc.ErrorInfo detail, wrapped in an apierror.APIError the same way
		// gensupport.WrapError does on every compute .Do() call. Here the reason is
		// only in the structured details (no legacy Errors[]), proving the structured
		// surface is consulted.
		body := `{"error":{"code":400,"message":"resource not ready",` +
			`"details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo",` +
			`"reason":"RESOURCE_NOT_READY","domain":"compute.googleapis.com"}]}}`
		gErr := &googleapi.Error{Code: 400, Body: body}
		apiErr, ok := apierror.ParseError(gErr, false)
		g.Expect(ok).To(BeTrue())
		gErr.Wrap(apiErr)

		g.Expect(apiErr.Reason()).To(Equal("RESOURCE_NOT_READY"))
		g.Expect(isTransientBadRequest(gErr)).To(BeTrue())
	})

	t.Run("When a terminal legacy reason accompanies a transient structured reason, it should be terminal", func(t *testing.T) {
		g := NewWithT(t)
		// Both surfaces are consulted and every reason must be transient, so a
		// terminal reason on either surface makes the whole error terminal.
		body := `{"error":{"code":400,"message":"resource not ready",` +
			`"details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo",` +
			`"reason":"RESOURCE_NOT_READY","domain":"compute.googleapis.com"}]}}`
		gErr := &googleapi.Error{Code: 400, Body: body, Errors: []googleapi.ErrorItem{{Reason: "invalid"}}}
		apiErr, ok := apierror.ParseError(gErr, false)
		g.Expect(ok).To(BeTrue())
		gErr.Wrap(apiErr)

		g.Expect(isTransientBadRequest(gErr)).To(BeFalse())
	})
}

func TestValidateFirewallName(t *testing.T) {
	tests := []struct {
		name      string
		fwName    string
		wantError bool
	}{
		{
			name:      "When the name is a valid RFC1035 name, it should pass",
			fwName:    "example-abcde-internal-cluster",
			wantError: false,
		},
		{
			name:      "When the name exceeds 63 characters, it should fail",
			fwName:    firewallRuleName("infra-" + strings.Repeat("a", 60)),
			wantError: true,
		},
		{
			name:      "When the name has uppercase letters, it should fail",
			fwName:    "Example-internal-cluster",
			wantError: true,
		},
		{
			name:      "When the name starts with a digit, it should fail",
			fwName:    "1example-internal-cluster",
			wantError: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			err := validateFirewallName(tc.fwName)
			if tc.wantError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
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
	validMarker, err := ownershipMarker("example-abcde")
	NewWithT(t).Expect(err).ToNot(HaveOccurred())

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
		{
			name:     "When both are qualified with the same name but different projects, it should not be equal",
			a:        "projects/project-a/global/networks/shared",
			b:        "projects/project-b/global/networks/shared",
			expected: false,
		},
		{
			name:     "When one is a bare name matching the other's trailing name, it should be equal",
			a:        "example-network",
			b:        "projects/my-project-123/global/networks/example-network",
			expected: true,
		},
		{
			name:     "When a bare name differs from the other's trailing name, it should not be equal",
			a:        "other-network",
			b:        "projects/my-project-123/global/networks/example-network",
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
	desired := desiredFirewall(infraID, netLink, hyperv1.OVNKubernetes, testNodePortRange)

	t.Run("When an existing rule equals desired, it should match", func(t *testing.T) {
		g := NewWithT(t)
		existing := desiredFirewall(infraID, netLink, hyperv1.OVNKubernetes, testNodePortRange)
		existing.Network = netLink
		g.Expect(firewallMatchesDesired(existing, desired)).To(BeTrue())
	})

	t.Run("When an existing rule has injected source ranges, it should not match", func(t *testing.T) {
		g := NewWithT(t)
		existing := desiredFirewall(infraID, netLink, hyperv1.OVNKubernetes, testNodePortRange)
		existing.SourceRanges = []string{"10.0.0.0/8"}
		g.Expect(firewallMatchesDesired(existing, desired)).To(BeFalse())
	})

	t.Run("When an existing rule has destination ranges, it should not match", func(t *testing.T) {
		g := NewWithT(t)
		existing := desiredFirewall(infraID, netLink, hyperv1.OVNKubernetes, testNodePortRange)
		existing.DestinationRanges = []string{"10.1.0.0/16"}
		g.Expect(firewallMatchesDesired(existing, desired)).To(BeFalse())
	})

	t.Run("When an existing rule uses source service accounts, it should not match", func(t *testing.T) {
		g := NewWithT(t)
		existing := desiredFirewall(infraID, netLink, hyperv1.OVNKubernetes, testNodePortRange)
		existing.SourceServiceAccounts = []string{"sa@my-project-123.iam.gserviceaccount.com"}
		g.Expect(firewallMatchesDesired(existing, desired)).To(BeFalse())
	})

	t.Run("When an existing rule uses target service accounts, it should not match", func(t *testing.T) {
		g := NewWithT(t)
		existing := desiredFirewall(infraID, netLink, hyperv1.OVNKubernetes, testNodePortRange)
		existing.TargetServiceAccounts = []string{"sa@my-project-123.iam.gserviceaccount.com"}
		g.Expect(firewallMatchesDesired(existing, desired)).To(BeFalse())
	})

	t.Run("When an existing rule is disabled, it should not match", func(t *testing.T) {
		g := NewWithT(t)
		existing := desiredFirewall(infraID, netLink, hyperv1.OVNKubernetes, testNodePortRange)
		existing.Disabled = true
		g.Expect(firewallMatchesDesired(existing, desired)).To(BeFalse())
	})

	t.Run("When an existing rule is missing UDP 6081, it should not match", func(t *testing.T) {
		g := NewWithT(t)
		existing := desiredFirewall(infraID, netLink, hyperv1.Other, testNodePortRange)
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
