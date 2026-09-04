package util

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestValidateIPv4CIDRs(t *testing.T) {
	tests := []struct {
		name        string
		cidrs       []string
		wantErr     bool
		errContains []string
	}{
		{
			name:    "When cidrs is empty, it should return no error",
			cidrs:   nil,
			wantErr: false,
		},
		{
			name:    "When cidrs contains a valid IPv4 CIDR, it should return no error",
			cidrs:   []string{"10.0.0.0/16"},
			wantErr: false,
		},
		{
			name:    "When cidrs contains multiple valid IPv4 CIDRs, it should return no error",
			cidrs:   []string{"10.0.0.0/16", "172.16.0.0/12", "192.168.0.0/24"},
			wantErr: false,
		},
		{
			name:        "When cidrs contains an invalid CIDR string, it should return an error",
			cidrs:       []string{"not-a-cidr"},
			wantErr:     true,
			errContains: []string{`"not-a-cidr"`},
		},
		{
			name:        "When cidrs contains an IPv6 CIDR, it should return an error",
			cidrs:       []string{"fd00::/64"},
			wantErr:     true,
			errContains: []string{`"fd00::/64": IPv6 CIDRs are not supported`},
		},
		{
			name:        "When cidrs contains multiple CIDRs including an IPv6 one, it should return an error for the IPv6 CIDR",
			cidrs:       []string{"10.0.0.0/16", "fd00::/64"},
			wantErr:     true,
			errContains: []string{`"fd00::/64": IPv6 CIDRs are not supported`},
		},
		{
			name:        "When cidrs contains multiple invalid CIDRs, it should return an error mentioning each one",
			cidrs:       []string{"not-a-cidr", "fd00::/64"},
			wantErr:     true,
			errContains: []string{`"not-a-cidr"`, `"fd00::/64": IPv6 CIDRs are not supported`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			err := ValidateIPv4CIDRs(tt.cidrs)
			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
				for _, substr := range tt.errContains {
					g.Expect(err.Error()).To(ContainSubstring(substr))
				}
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
}
