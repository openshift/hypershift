package controlplanecomponent

import (
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
)

func findEnvVar(envVars []corev1.EnvVar, name string) *corev1.EnvVar {
	for i := range envVars {
		if envVars[i].Name == name {
			return &envVars[i]
		}
	}
	return nil
}

func TestBuildContainer(t *testing.T) {
	hcp := &hyperv1.HostedControlPlane{}

	tests := []struct {
		name            string
		opts            KonnectivityContainerOptions
		proxyEnvs       map[string]string
		expectProxyVars bool
	}{
		{
			name: "When ConnectDirectlyToCloudAPIs is true and management proxy is configured it should set proxy env vars",
			opts: KonnectivityContainerOptions{
				Mode: HTTPS,
				HTTPSOptions: HTTPSOptions{
					ConnectDirectlyToCloudAPIs: ptr.To(true),
				},
			},
			proxyEnvs: map[string]string{
				"HTTP_PROXY":  "http://proxy.mgmt.example.com:3128",
				"HTTPS_PROXY": "https://proxy.mgmt.example.com:3129",
				"NO_PROXY":    "localhost,10.0.0.0/8",
			},
			expectProxyVars: true,
		},
		{
			name: "When ConnectDirectlyToCloudAPIs is true for Socks5 mode it should set proxy env vars",
			opts: KonnectivityContainerOptions{
				Mode: Socks5,
				Socks5Options: Socks5Options{
					ConnectDirectlyToCloudAPIs: ptr.To(true),
				},
			},
			proxyEnvs: map[string]string{
				"HTTP_PROXY":  "http://proxy.mgmt.example.com:3128",
				"HTTPS_PROXY": "https://proxy.mgmt.example.com:3129",
				"NO_PROXY":    "localhost,10.0.0.0/8",
			},
			expectProxyVars: true,
		},
		{
			name: "When ConnectDirectlyToCloudAPIs is false it should not set proxy env vars",
			opts: KonnectivityContainerOptions{
				Mode: HTTPS,
				HTTPSOptions: HTTPSOptions{
					ConnectDirectlyToCloudAPIs: ptr.To(false),
				},
			},
			proxyEnvs: map[string]string{
				"HTTP_PROXY":  "http://proxy.mgmt.example.com:3128",
				"HTTPS_PROXY": "https://proxy.mgmt.example.com:3129",
				"NO_PROXY":    "localhost,10.0.0.0/8",
			},
			expectProxyVars: false,
		},
		{
			name: "When ConnectDirectlyToCloudAPIs is not set it should not set proxy env vars",
			opts: KonnectivityContainerOptions{
				Mode: HTTPS,
			},
			proxyEnvs: map[string]string{
				"HTTP_PROXY":  "http://proxy.mgmt.example.com:3128",
				"HTTPS_PROXY": "https://proxy.mgmt.example.com:3129",
				"NO_PROXY":    "localhost,10.0.0.0/8",
			},
			expectProxyVars: false,
		},
		{
			name: "When ConnectDirectlyToCloudAPIs is true but no management proxy is configured it should not add proxy env vars",
			opts: KonnectivityContainerOptions{
				Mode: HTTPS,
				HTTPSOptions: HTTPSOptions{
					ConnectDirectlyToCloudAPIs: ptr.To(true),
				},
			},
			proxyEnvs:       map[string]string{},
			expectProxyVars: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			t.Setenv("HTTP_PROXY", "")
			t.Setenv("HTTPS_PROXY", "")
			t.Setenv("NO_PROXY", "")

			for k, v := range tt.proxyEnvs {
				t.Setenv(k, v)
			}

			container := tt.opts.buildContainer(hcp, "test-image:latest", nil)

			g.Expect(findEnvVar(container.Env, "KUBECONFIG")).NotTo(BeNil(), "KUBECONFIG should always be set")

			if tt.expectProxyVars {
				httpProxy := findEnvVar(container.Env, "HTTP_PROXY")
				g.Expect(httpProxy).NotTo(BeNil(), "HTTP_PROXY should be set")
				g.Expect(httpProxy.Value).To(Equal(tt.proxyEnvs["HTTP_PROXY"]))

				httpsProxy := findEnvVar(container.Env, "HTTPS_PROXY")
				g.Expect(httpsProxy).NotTo(BeNil(), "HTTPS_PROXY should be set")
				g.Expect(httpsProxy.Value).To(Equal(tt.proxyEnvs["HTTPS_PROXY"]))

				noProxy := findEnvVar(container.Env, "NO_PROXY")
				g.Expect(noProxy).NotTo(BeNil(), "NO_PROXY should be set")
				g.Expect(noProxy.Value).To(ContainSubstring("kube-apiserver"), "NO_PROXY should include kube-apiserver")
				for _, entry := range strings.Split(tt.proxyEnvs["NO_PROXY"], ",") {
					g.Expect(noProxy.Value).To(ContainSubstring(entry), "NO_PROXY should preserve original entry %q", entry)
				}
			} else {
				g.Expect(findEnvVar(container.Env, "HTTP_PROXY")).To(BeNil(), "HTTP_PROXY should not be set")
				g.Expect(findEnvVar(container.Env, "HTTPS_PROXY")).To(BeNil(), "HTTPS_PROXY should not be set")
				g.Expect(findEnvVar(container.Env, "NO_PROXY")).To(BeNil(), "NO_PROXY should not be set")
			}
		})
	}
}

func TestBuildContainerDualMode(t *testing.T) {
	g := NewGomegaWithT(t)
	hcp := &hyperv1.HostedControlPlane{}

	t.Setenv("HTTP_PROXY", "http://proxy.mgmt.example.com:3128")
	t.Setenv("HTTPS_PROXY", "https://proxy.mgmt.example.com:3129")
	t.Setenv("NO_PROXY", "localhost")

	// Simulate what injectKonnectivityContainer does for Dual mode:
	// it builds the HTTPS container first, then the Socks5 container.
	opts := KonnectivityContainerOptions{
		Mode: Dual,
		HTTPSOptions: HTTPSOptions{
			ConnectDirectlyToCloudAPIs: ptr.To(true),
		},
	}

	opts.Mode = HTTPS
	httpsContainer := opts.buildContainer(hcp, "test-image:latest", nil)

	opts.Mode = Socks5
	socks5Container := opts.buildContainer(hcp, "test-image:latest", nil)

	g.Expect(findEnvVar(httpsContainer.Env, "HTTP_PROXY")).NotTo(BeNil(),
		"HTTPS container should have HTTP_PROXY because ConnectDirectlyToCloudAPIs is set on HTTPSOptions")
	g.Expect(findEnvVar(httpsContainer.Env, "HTTPS_PROXY")).NotTo(BeNil(),
		"HTTPS container should have HTTPS_PROXY")

	g.Expect(findEnvVar(socks5Container.Env, "HTTP_PROXY")).To(BeNil(),
		"Socks5 container should not have HTTP_PROXY because ConnectDirectlyToCloudAPIs is not set on Socks5Options")
	g.Expect(findEnvVar(socks5Container.Env, "HTTPS_PROXY")).To(BeNil(),
		"Socks5 container should not have HTTPS_PROXY")
}
