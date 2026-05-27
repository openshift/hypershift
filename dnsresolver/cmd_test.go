package dnsresolver

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestParseServiceName(t *testing.T) {
	tests := []struct {
		name        string
		dnsName     string
		expected    string
		expectError bool
	}{
		{
			name:     "When given a standard headless service DNS name it should extract the service name",
			dnsName:  "etcd-0.etcd-discovery.my-namespace.svc",
			expected: "etcd-discovery",
		},
		{
			name:     "When given a fully qualified DNS name it should extract the service name",
			dnsName:  "etcd-0.etcd-discovery.my-namespace.svc.cluster.local",
			expected: "etcd-discovery",
		},
		{
			name:     "When given a DNS name with a long namespace it should extract the service name",
			dnsName:  "etcd-2.etcd-discovery.ocm-arohcpci01-2q7h5rjtm2oud3pn6i3890qa6p37sts3-i2y6k1a2u2a0z1h.svc",
			expected: "etcd-discovery",
		},
		{
			name:        "When given a DNS name with too few components it should return an error",
			dnsName:     "etcd-0.etcd-discovery",
			expectError: true,
		},
		{
			name:        "When given a single component it should return an error",
			dnsName:     "etcd-0",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			result, err := parseServiceName(tt.dnsName)
			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(result).To(Equal(tt.expected))
			}
		})
	}
}
