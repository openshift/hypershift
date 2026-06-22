package netutil

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestFirstUsableIP(t *testing.T) {
	tests := []struct {
		name    string
		cidr    string
		want    string
		wantErr bool
	}{
		{
			name:    "Given IPv4 CIDR, it should return the first ip of the network range",
			cidr:    "192.168.1.0/24",
			want:    "192.168.1.1",
			wantErr: false,
		},
		{
			name:    "Given IPv6 CIDR, it should return the first ip of the network range",
			cidr:    "2000::/3",
			want:    "2000::1",
			wantErr: false,
		},
		{
			name:    "Given a malformed IPv4 CIDR, it should return empty string and err",
			cidr:    "192.168.1.35.53/24",
			want:    "",
			wantErr: true,
		},
		{
			name:    "Given a malformed IPv6 CIDR, it should return empty string and err",
			cidr:    "2001::44444444444444/17",
			want:    "",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := FirstUsableIP(tt.cidr)
			if (err != nil) != tt.wantErr {
				t.Errorf("FirstUsableIP() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("FirstUsableIP() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsIPv4CIDR(t *testing.T) {
	tests := []struct {
		input       string
		expected    bool
		expectError bool
	}{
		// Valid IPv4 CIDRs
		{"192.168.1.0/24", true, false},
		{"10.0.0.0/8", true, false},

		// Valid IPv6 CIDRs
		{"2001:db8::/32", false, false},
		{"fd00::/8", false, false},

		// Invalid inputs
		{"invalid", false, true},
		{"192.168.1.1/33", false, true},  // Invalid CIDR prefix
		{"", false, true},                // Empty input
		{"1234::5678::/64", false, true}, // Malformed IP

		// Edge cases
		{"0.0.0.0/0", true, false},
		{"255.255.255.255/32", true, false},
	}

	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			g := NewWithT(t)
			result, err := IsIPv4CIDR(test.input)
			if test.expectError {
				g.Expect(err).To(HaveOccurred(), "Expected an error for input '%s'", test.input)
			} else {
				g.Expect(err).ToNot(HaveOccurred(), "Did not expect an error for input '%s'", test.input)
			}

			g.Expect(result).To(Equal(test.expected), "Unexpected result for input '%s'", test.input)
		})
	}
}

func TestIsIPv4Address(t *testing.T) {
	tests := []struct {
		input       string
		expected    bool
		expectError bool
	}{
		// Valid IPv4 addresses
		{"192.168.1.1", true, false},
		{"10.0.0.1", true, false},

		// Valid IPv6 addresses
		{"2001:db8::1", false, false},
		{"fd00::1", false, false},

		// Invalid inputs
		{"invalid", false, true},
		{"192.168.1.256", false, true}, // Invalid IPv4 address
		{"", false, true},              // Empty input
		{"1234::5678::1", false, true}, // Malformed IP

		// Edge cases
		{"0.0.0.0", true, false},
		{"255.255.255.255", true, false},
	}

	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			g := NewWithT(t)
			result, err := IsIPv4Address(test.input)
			if test.expectError {
				g.Expect(err).To(HaveOccurred(), "Expected an error for input '%s'", test.input)
			} else {
				g.Expect(err).ToNot(HaveOccurred(), "Did not expect an error for input '%s'", test.input)
			}

			g.Expect(result).To(Equal(test.expected), "Unexpected result for input '%s'", test.input)
		})
	}
}

func TestHostFromURL(t *testing.T) {
	tests := []struct {
		input    string
		expected string
		wantErr  bool
	}{
		{"http://example.com", "example.com", false},
		{"https://example.com:443", "example.com", false},
		{"http://localhost:8080", "localhost", false},
		{"https://127.0.0.1:9000", "127.0.0.1", false},
		{"ftp://example.org:21", "example.org", false},
		{"http://[::1]:8080", "::1", false},                // IPv6 localhost
		{"http://[2001:db8::1]:443", "2001:db8::1", false}, // IPv6 example
		{"??", "", true},           // Invalid URL
		{"http://:8080", "", true}, // Missing hostname
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			g := NewWithT(t)
			result, err := HostFromURL(tt.input)
			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}
