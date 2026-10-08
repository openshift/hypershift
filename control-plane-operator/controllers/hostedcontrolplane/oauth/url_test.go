package oauth

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestExternalURL(t *testing.T) {
	tests := []struct {
		name string
		host string
		port int32
		want string
	}{
		{
			name: "When port is 443, it should omit the port",
			host: "oauth-clusters-example.apps.mgmt.example.com",
			port: 443,
			want: "https://oauth-clusters-example.apps.mgmt.example.com",
		},
		{
			name: "When port is not 443, it should keep the port",
			host: "oauth-clusters-example.apps.mgmt.example.com",
			port: 6443,
			want: "https://oauth-clusters-example.apps.mgmt.example.com:6443",
		},
		{
			name: "When host is IPv4 with a NodePort, it should keep the port",
			host: "10.0.0.5",
			port: 31234,
			want: "https://10.0.0.5:31234",
		},
		{
			name: "When host is IPv6 and port is 443, it should bracket the host and omit the port",
			host: "fd00::5",
			port: 443,
			want: "https://[fd00::5]",
		},
		{
			name: "When host is IPv6 and port is not 443, it should bracket the host and keep the port",
			host: "fd00::5",
			port: 31234,
			want: "https://[fd00::5]:31234",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(ExternalURL(tt.host, tt.port)).To(Equal(tt.want))
		})
	}
}
