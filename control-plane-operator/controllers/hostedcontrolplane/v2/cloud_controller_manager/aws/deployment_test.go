package aws

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAdaptDeployment(t *testing.T) {
	testCases := []struct {
		name       string
		httpProxy  string
		httpsProxy string
		noProxy    string
		validate   func(*WithT, *corev1.Container)
	}{
		{
			name:       "When proxy environment variables are set, it should add proxy env vars to container",
			httpProxy:  "http://proxy.example.com:8080",
			httpsProxy: "https://proxy.example.com:8443",
			noProxy:    "localhost,127.0.0.1",
			validate: func(g *WithT, container *corev1.Container) {
				httpProxy := podspec.FindEnvVar("HTTP_PROXY", container.Env)
				g.Expect(httpProxy).ToNot(BeNil())
				g.Expect(httpProxy.Value).To(Equal("http://proxy.example.com:8080"))

				httpsProxy := podspec.FindEnvVar("HTTPS_PROXY", container.Env)
				g.Expect(httpsProxy).ToNot(BeNil())
				g.Expect(httpsProxy.Value).To(Equal("https://proxy.example.com:8443"))

				noProxy := podspec.FindEnvVar("NO_PROXY", container.Env)
				g.Expect(noProxy).ToNot(BeNil())
				g.Expect(noProxy.Value).To(ContainSubstring("localhost"))
				g.Expect(noProxy.Value).To(ContainSubstring("127.0.0.1"))
				g.Expect(noProxy.Value).To(ContainSubstring("kube-apiserver"))

				g.Expect(podspec.FindEnvVar("AWS_SHARED_CREDENTIALS_FILE", container.Env)).ToNot(BeNil())
				g.Expect(podspec.FindEnvVar("AWS_SDK_LOAD_CONFIG", container.Env)).ToNot(BeNil())
				g.Expect(podspec.FindEnvVar("AWS_EC2_METADATA_DISABLED", container.Env)).ToNot(BeNil())
			},
		},
		{
			name:      "When only HTTP_PROXY is set, it should add HTTP_PROXY and NO_PROXY but not HTTPS_PROXY",
			httpProxy: "http://proxy.example.com:8080",
			noProxy:   "localhost,127.0.0.1",
			validate: func(g *WithT, container *corev1.Container) {
				httpProxy := podspec.FindEnvVar("HTTP_PROXY", container.Env)
				g.Expect(httpProxy).ToNot(BeNil())
				g.Expect(httpProxy.Value).To(Equal("http://proxy.example.com:8080"))

				g.Expect(podspec.FindEnvVar("HTTPS_PROXY", container.Env)).To(BeNil())

				noProxy := podspec.FindEnvVar("NO_PROXY", container.Env)
				g.Expect(noProxy).ToNot(BeNil())
				g.Expect(noProxy.Value).To(ContainSubstring("localhost"))
				g.Expect(noProxy.Value).To(ContainSubstring("127.0.0.1"))
				g.Expect(noProxy.Value).To(ContainSubstring("kube-apiserver"))
			},
		},
		{
			name:       "When only HTTPS_PROXY is set, it should add HTTPS_PROXY and NO_PROXY but not HTTP_PROXY",
			httpsProxy: "https://proxy.example.com:8443",
			noProxy:    "localhost,127.0.0.1",
			validate: func(g *WithT, container *corev1.Container) {
				g.Expect(podspec.FindEnvVar("HTTP_PROXY", container.Env)).To(BeNil())

				httpsProxy := podspec.FindEnvVar("HTTPS_PROXY", container.Env)
				g.Expect(httpsProxy).ToNot(BeNil())
				g.Expect(httpsProxy.Value).To(Equal("https://proxy.example.com:8443"))

				noProxy := podspec.FindEnvVar("NO_PROXY", container.Env)
				g.Expect(noProxy).ToNot(BeNil())
				g.Expect(noProxy.Value).To(ContainSubstring("localhost"))
				g.Expect(noProxy.Value).To(ContainSubstring("127.0.0.1"))
				g.Expect(noProxy.Value).To(ContainSubstring("kube-apiserver"))
			},
		},
		{
			name: "When no proxy is set, it should not add proxy env vars",
			validate: func(g *WithT, container *corev1.Container) {
				g.Expect(podspec.FindEnvVar("HTTP_PROXY", container.Env)).To(BeNil())
				g.Expect(podspec.FindEnvVar("HTTPS_PROXY", container.Env)).To(BeNil())
				g.Expect(podspec.FindEnvVar("NO_PROXY", container.Env)).To(BeNil())
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			t.Setenv("HTTP_PROXY", tc.httpProxy)
			t.Setenv("HTTPS_PROXY", tc.httpsProxy)
			t.Setenv("NO_PROXY", tc.noProxy)

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{},
			}

			cpContext := component.WorkloadContext{HCP: hcp}

			deployment, err := assets.LoadDeploymentManifest(ComponentName)
			g.Expect(err).ToNot(HaveOccurred())

			err = adaptDeployment(cpContext, deployment)
			g.Expect(err).ToNot(HaveOccurred())

			container := podspec.FindContainer(containerName, deployment.Spec.Template.Spec.Containers)
			g.Expect(container).ToNot(BeNil(), "cloud-controller-manager container should exist")

			tc.validate(g, container)
		})
	}
}
