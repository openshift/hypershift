package aws

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestProxyURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, host, authority string
	}{
		{name: "When the proxy has IPv4, it should preserve the existing URL", host: "192.0.2.1", authority: "192.0.2.1:3128"},
		{name: "When the proxy has raw IPv6, it should bracket the authority", host: "2001:db8::1", authority: "[2001:db8::1]:3128"},
		{name: "When the proxy has DNS, it should preserve the hostname", host: "proxy.example.test", authority: "proxy.example.test:3128"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			for _, scheme := range []string{"http", "https"} {
				g.Expect(proxyURL(scheme, test.host)).To(Equal(scheme+"://"+test.authority), "production %s proxy URL must retain fixed port 3128", scheme)
			}
		})
	}
}
