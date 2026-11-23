package console

import (
	"testing"

	. "github.com/onsi/gomega"

	operatorv1 "github.com/openshift/api/operator/v1"
)

func TestReconcileConsoleAuthProxy(t *testing.T) {
	testCases := []struct {
		name          string
		override      operatorv1.AuthenticationProxyConfig
		trustedCAName string
		expected      operatorv1.ConsoleAuthProxyConfig
	}{
		{
			name: "When the override carries http and https proxies with a noProxy slice, it should preserve the slice",
			override: operatorv1.AuthenticationProxyConfig{
				HTTPProxy:  "http://proxy.example.com:8080",
				HTTPSProxy: "https://proxy.example.com:8443",
				NoProxy:    []string{".svc", "10.0.0.0/8", "example.com"},
			},
			expected: operatorv1.ConsoleAuthProxyConfig{
				HTTPProxy:  "http://proxy.example.com:8080",
				HTTPSProxy: "https://proxy.example.com:8443",
				NoProxy:    []string{".svc", "10.0.0.0/8", "example.com"},
			},
		},
		{
			name: "When a guest trusted CA name is supplied, it should rewire the trustedCA reference to it",
			override: operatorv1.AuthenticationProxyConfig{
				HTTPSProxy: "https://proxy.example.com:8443",
				TrustedCA:  operatorv1.AuthenticationConfigMapReference{Name: "source-ca"},
			},
			trustedCAName: "console-auth-proxy-trusted-ca",
			expected: operatorv1.ConsoleAuthProxyConfig{
				HTTPSProxy: "https://proxy.example.com:8443",
				TrustedCA:  operatorv1.ConsoleAuthProxyTrustedCAConfigMapReference{Name: "console-auth-proxy-trusted-ca"},
			},
		},
		{
			name: "When the guest configmap name is empty, it should not set a trustedCA",
			override: operatorv1.AuthenticationProxyConfig{
				HTTPProxy: "http://proxy.example.com:8080",
				TrustedCA: operatorv1.AuthenticationConfigMapReference{Name: "source-ca"},
			},
			expected: operatorv1.ConsoleAuthProxyConfig{
				HTTPProxy: "http://proxy.example.com:8080",
			},
		},
		{
			name: "When the console carries a previous authProxy, it should overwrite it entirely",
			override: operatorv1.AuthenticationProxyConfig{
				HTTPSProxy: "https://new.example.com:8443",
			},
			expected: operatorv1.ConsoleAuthProxyConfig{
				HTTPSProxy: "https://new.example.com:8443",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			console := &operatorv1.Console{}
			if tc.name == "When the console carries a previous authProxy, it should overwrite it entirely" {
				console.Spec.AuthProxy = operatorv1.ConsoleAuthProxyConfig{
					HTTPProxy: "http://old.example.com:8080",
					NoProxy:   []string{"stale"},
				}
			}
			ReconcileConsoleAuthProxy(console, tc.override, tc.trustedCAName)
			g.Expect(console.Spec.AuthProxy).To(Equal(tc.expected))
		})
	}
}

func TestClearConsoleAuthProxy(t *testing.T) {
	g := NewWithT(t)
	console := &operatorv1.Console{
		Spec: operatorv1.ConsoleSpec{
			AuthProxy: operatorv1.ConsoleAuthProxyConfig{
				HTTPProxy: "http://proxy.example.com:8080",
			},
		},
	}

	ClearConsoleAuthProxy(console)

	g.Expect(console.Spec.AuthProxy).To(Equal(operatorv1.ConsoleAuthProxyConfig{}))
}
