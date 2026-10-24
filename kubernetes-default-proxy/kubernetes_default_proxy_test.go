package kubernetesdefaultproxy

import (
	"strings"
	"testing"
)

func TestNormalizeTCPAddress(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name  string
		input string
		want  string
	}{
		{name: "When IPv6 is unbracketed, it should add brackets", input: "fd00::1:6443", want: "[fd00::1]:6443"},
		{name: "When IPv6 has a zone, it should preserve the zone", input: "fe80::1%eth0:6443", want: "[fe80::1%eth0]:6443"},
		{name: "When IPv6 is IPv4 mapped, it should add brackets", input: "::ffff:192.0.2.1:443", want: "[::ffff:192.0.2.1]:443"},
		{name: "When IPv6 uses a service name, it should preserve the service", input: "::1:https", want: "[::1]:https"},
		{name: "When IPv6 uses an unknown service, it should not resolve it", input: "::1:unknown-service", want: "[::1]:unknown-service"},
		{name: "When IPv6 uses an out of range port, it should not validate it", input: "::1:65536", want: "[::1]:65536"},
		{name: "When IPv6 is bracketed, it should remain unchanged", input: "[FD00::0001]:06443", want: "[FD00::0001]:06443"},
		{name: "When IPv4 is supplied, it should remain unchanged", input: "127.0.0.1:6443", want: "127.0.0.1:6443"},
		{name: "When a hostname is supplied, it should remain unchanged", input: "api.openshift.test:https", want: "api.openshift.test:https"},
		{name: "When a wildcard listener is supplied, it should remain unchanged", input: ":0", want: ":0"},
		{name: "When the port is missing, it should remain unchanged", input: "api.openshift.test", want: "api.openshift.test"},
		{name: "When IPv6 cannot be split into host and port, it should remain unchanged", input: "::1", want: "::1"},
		{name: "When brackets are malformed, it should remain unchanged", input: "[::1:6443", want: "[::1:6443"},
		{name: "When a URL is supplied, it should remain unchanged", input: "http://[::1]:6443", want: "http://[::1]:6443"},
		{name: "When input contains whitespace, it should remain unchanged", input: "proxy\n:3128", want: "proxy\n:3128"},
		{name: "When input is empty, it should remain unchanged", input: "", want: ""},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			if got := normalizeTCPAddress(testCase.input); got != testCase.want {
				t.Fatalf("got %q, want %q", got, testCase.want)
			}
			if got := normalizeTCPAddress(testCase.want); got != testCase.want {
				t.Fatalf("normalization is not idempotent: got %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name    string
		address string
		want    string
	}{
		{name: "When addresses are unbracketed IPv6, it should normalize every field", address: "fd00::1:6443", want: "[fd00::1]:6443"},
		{name: "When addresses are arbitrary nonempty inputs, it should accept them unchanged", address: "not a TCP address", want: "not a TCP address"},
		{name: "When addresses contain URL syntax, it should accept them unchanged", address: "https://proxy:3128/path?query#fragment", want: "https://proxy:3128/path?query#fragment"},
		{name: "When ports are out of range, it should accept them unchanged", address: "proxy:65536", want: "proxy:65536"},
		{name: "When services are unknown, it should accept them unchanged", address: "proxy:unknown-service", want: "proxy:unknown-service"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			proxy := &server{listenAddr: testCase.address, proxyAddr: testCase.address, apiServerAddr: testCase.address}
			if err := proxy.validate(); err != nil {
				t.Fatal(err)
			}
			if proxy.listenAddr != testCase.want || proxy.proxyAddr != testCase.want || proxy.apiServerAddr != testCase.want {
				t.Fatalf("unexpected addresses: %#v", proxy)
			}
		})
	}
	t.Run("When required addresses are empty, it should report every mandatory flag", func(t *testing.T) {
		t.Parallel()
		err := (&server{}).validate()
		if err == nil {
			t.Fatal("expected mandatory field errors")
		}
		for _, flag := range []string{"listen-addr", "proxy-addr", "apiserver-addr"} {
			if !strings.Contains(err.Error(), "--"+flag+" is mandatory") {
				t.Fatalf("missing mandatory field error for %s: %v", flag, err)
			}
		}
	})
}
