package gcputil

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestTruncateZoneName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxLen   int
		expected string
	}{
		{name: "When input is shorter than max, it should be unchanged", input: "short-name", maxLen: 63, expected: "short-name"},
		{name: "When input equals max, it should be unchanged", input: "exactly-ten", maxLen: 11, expected: "exactly-ten"},
		{name: "When input is longer than max, it should be truncated", input: "this-is-a-very-long-name", maxLen: 10, expected: "this-is-a-"},
		{name: "When max is zero, it should return empty", input: "any-name", maxLen: 0, expected: ""},
		{name: "When input is empty, it should return empty", input: "", maxLen: 63, expected: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			result := truncateZoneName(tt.input, tt.maxLen)
			g.Expect(result).To(Equal(tt.expected))
			g.Expect(len(result)).To(BeNumerically("<=", tt.maxLen))
		})
	}
}

func TestValidateZoneName(t *testing.T) {
	tests := []struct {
		name      string
		zoneName  string
		expectErr bool
	}{
		{name: "When name has lowercase letters, it should be valid", zoneName: "myzone", expectErr: false},
		{name: "When name has letters and hyphens, it should be valid", zoneName: "my-cluster-hypershift-local", expectErr: false},
		{name: "When name has letters digits and hyphens, it should be valid", zoneName: "my-zone-123", expectErr: false},
		{name: "When name starts with a digit, it should be invalid", zoneName: "123-zone", expectErr: true},
		{name: "When name starts with a hyphen, it should be invalid", zoneName: "-my-zone", expectErr: true},
		{name: "When name has uppercase, it should be invalid", zoneName: "My-Zone", expectErr: true},
		{name: "When name has an underscore, it should be invalid", zoneName: "my_zone", expectErr: true},
		{name: "When name has a dot, it should be invalid", zoneName: "my.zone", expectErr: true},
		{name: "When name is empty, it should be invalid", zoneName: "", expectErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			err := ValidateZoneName(tt.zoneName)
			if tt.expectErr {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}
		})
	}
}

func TestIngressZoneNames(t *testing.T) {
	tests := []struct {
		name            string
		baseDomain      string
		expectedPublic  string
		expectedPrivate string
		expectErr       bool
	}{
		{
			name:            "When domain is simple, it should map dots to hyphens and append the suffix",
			baseDomain:      "example.com",
			expectedPublic:  "example-com-public",
			expectedPrivate: "example-com-private",
		},
		{
			name:            "When domain is multi-label, it should map dots to hyphens and append the suffix",
			baseDomain:      "my-cluster.gcp.example.com",
			expectedPublic:  "my-cluster-gcp-example-com-public",
			expectedPrivate: "my-cluster-gcp-example-com-private",
		},
		{
			name:       "When domain starts with a digit, it should produce an invalid zone name",
			baseDomain: "9bad.example.com",
			expectErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			pub, pubErr := PublicIngressZoneName(tt.baseDomain)
			priv, privErr := PrivateIngressZoneName(tt.baseDomain)
			if tt.expectErr {
				g.Expect(pubErr).To(HaveOccurred())
				g.Expect(privErr).To(HaveOccurred())
				return
			}
			g.Expect(pubErr).NotTo(HaveOccurred())
			g.Expect(privErr).NotTo(HaveOccurred())
			g.Expect(pub).To(Equal(tt.expectedPublic))
			g.Expect(priv).To(Equal(tt.expectedPrivate))
			g.Expect(len(pub)).To(BeNumerically("<=", maxZoneNameLength))
			g.Expect(len(priv)).To(BeNumerically("<=", maxZoneNameLength))
		})
	}
}

func TestIngressZoneNamesLengthConstraint(t *testing.T) {
	g := NewWithT(t)
	// A base domain long enough to force truncation of the base portion.
	longDomain := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.com"

	pub, err := PublicIngressZoneName(longDomain)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(len(pub)).To(BeNumerically("<=", maxZoneNameLength))
	g.Expect(pub).To(HaveSuffix("-public"))

	priv, err := PrivateIngressZoneName(longDomain)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(len(priv)).To(BeNumerically("<=", maxZoneNameLength))
	g.Expect(priv).To(HaveSuffix("-private"))
}

func TestHypershiftLocalZoneName(t *testing.T) {
	g := NewWithT(t)
	name, err := HypershiftLocalZoneName("my-cluster")
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(name).To(Equal("my-cluster-hypershift-local"))

	_, err = HypershiftLocalZoneName("9bad")
	g.Expect(err).To(HaveOccurred())
}
