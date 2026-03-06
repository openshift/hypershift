package certs

import (
	"net"
	"testing"
)

func TestCompareIPAddresses_IPv6Normalization(t *testing.T) {
	tests := []struct {
		name        string
		actual      []string
		expected    []string
		shouldError bool
	}{
		{
			name: "IPv6 compressed vs expanded - should match",
			actual: []string{
				"127.0.0.1",
				"0:0:0:0:0:0:0:1", // expanded
				"172.31.0.1",
				"FD05:0:0:0:0:0:0:1", // expanded, uppercase
				"172.20.0.1",
				"2620:52:0:2EF8:0:0:0:9F", // expanded, mixed case
			},
			expected: []string{
				"127.0.0.1",
				"::1", // compressed
				"172.31.0.1",
				"fd05::1", // compressed, lowercase
				"172.20.0.1",
				"2620:52:0:2ef8::9f", // compressed, lowercase
			},
			shouldError: false,
		},
		{
			name: "Different IP count - should fail",
			actual: []string{
				"127.0.0.1",
				"::1",
			},
			expected: []string{
				"127.0.0.1",
			},
			shouldError: true,
		},
		{
			name: "Actually different IPs - should fail",
			actual: []string{
				"127.0.0.1",
				"::1",
			},
			expected: []string{
				"127.0.0.1",
				"::2", // different IP
			},
			shouldError: true,
		},
		{
			name: "Order doesn't matter - should match",
			actual: []string{
				"172.31.0.1",
				"127.0.0.1",
				"::1",
			},
			expected: []string{
				"127.0.0.1",
				"::1",
				"172.31.0.1",
			},
			shouldError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Convert strings to net.IP
			actualIPs := make([]net.IP, len(tt.actual))
			for i, ipStr := range tt.actual {
				actualIPs[i] = net.ParseIP(ipStr)
				if actualIPs[i] == nil {
					t.Fatalf("Failed to parse actual IP: %s", ipStr)
				}
			}

			expectedIPs := make([]net.IP, len(tt.expected))
			for i, ipStr := range tt.expected {
				expectedIPs[i] = net.ParseIP(ipStr)
				if expectedIPs[i] == nil {
					t.Fatalf("Failed to parse expected IP: %s", ipStr)
				}
			}

			// Test the comparison function
			err := compareIPAddresses(actualIPs, expectedIPs)

			if tt.shouldError && err == nil {
				t.Errorf("Expected error but got none")
			}
			if !tt.shouldError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}
		})
	}
}

func TestCompareIPAddresses_DualStackScenario(t *testing.T) {
	// This test specifically recreates the dualstack-420 cluster scenario
	t.Run("dualstack-420 cluster scenario", func(t *testing.T) {
		// IPs as they appear in the certificate (expanded format from x509)
		actualIPs := []net.IP{
			net.ParseIP("127.0.0.1"),
			net.ParseIP("0:0:0:0:0:0:0:1"),
			net.ParseIP("172.31.0.1"),
			net.ParseIP("FD05:0:0:0:0:0:0:1"),
			net.ParseIP("172.20.0.1"),
			net.ParseIP("2620:52:0:2EF8:0:0:0:9F"),
		}

		// IPs as they're calculated by GetKASServerCertificatesSANs (compressed format)
		expectedIPs := []net.IP{
			net.ParseIP("127.0.0.1"),
			net.ParseIP("0:0:0:0:0:0:0:1"), // Note: this is already expanded in the code
			net.ParseIP("172.31.0.1"),
			net.ParseIP("fd05::1"),
			net.ParseIP("172.20.0.1"),
			net.ParseIP("2620:52:0:2ef8::9f"),
		}

		err := compareIPAddresses(actualIPs, expectedIPs)
		if err != nil {
			t.Errorf("Comparison should succeed for dualstack-420 scenario, but got error: %v", err)
		}
	})
}
