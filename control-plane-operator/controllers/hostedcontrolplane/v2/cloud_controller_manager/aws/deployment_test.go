package aws

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"

	configv1 "github.com/openshift/api/config/v1"

	appsv1 "k8s.io/api/apps/v1"
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

func buildHostedControlPlane(tlsProfile *configv1.TLSSecurityProfile) *hyperv1.HostedControlPlane {
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-namespace",
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.AWSPlatform,
			},
		},
	}

	if tlsProfile != nil {
		hcp.Spec.Configuration = &hyperv1.ClusterConfiguration{
			APIServer: &configv1.APIServerSpec{
				TLSSecurityProfile: tlsProfile,
			},
		}
	}

	return hcp
}

func buildDeployment(args []string) *appsv1.Deployment {
	return &appsv1.Deployment{
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "cloud-controller-manager",
							Args: append([]string{}, args...),
						},
					},
				},
			},
		},
	}
}

func TestAdaptDeploymentTLS(t *testing.T) {
	t.Parallel()

	baseArgs := []string{
		"--cloud-provider=aws",
		"--use-service-account-credentials=false",
	}

	customTLSProfile := &configv1.TLSSecurityProfile{
		Type: configv1.TLSProfileCustomType,
		Custom: &configv1.CustomTLSProfile{
			TLSProfileSpec: configv1.TLSProfileSpec{
				MinTLSVersion: configv1.VersionTLS12,
				Ciphers: []string{
					"ECDHE-ECDSA-AES128-GCM-SHA256",
					"ECDHE-RSA-AES128-GCM-SHA256",
				},
			},
		},
	}

	testCases := []struct {
		name         string
		tlsProfile   *configv1.TLSSecurityProfile
		expectedArgs []string
	}{
		{
			name:       "When TLS profile is nil it should append intermediate defaults",
			tlsProfile: nil,
			expectedArgs: append(baseArgs,
				"--tls-min-version=VersionTLS12",
				"--tls-cipher-suites=TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256",
			),
		},
		{
			name: "When using Modern TLS profile it should append only min-version",
			tlsProfile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileModernType,
			},
			expectedArgs: append(baseArgs, "--tls-min-version=VersionTLS13"),
		},
		{
			name:       "When using Custom TLS profile it should append custom TLS args",
			tlsProfile: customTLSProfile,
			expectedArgs: append(baseArgs,
				"--tls-min-version=VersionTLS12",
				"--tls-cipher-suites=TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
			),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			hcp := buildHostedControlPlane(tc.tlsProfile)
			deployment := buildDeployment(baseArgs)

			cpContext := component.WorkloadContext{
				HCP: hcp,
			}
			err := adaptDeployment(cpContext, deployment)
			g.Expect(err).ToNot(HaveOccurred())

			container := podspec.FindContainer("cloud-controller-manager", deployment.Spec.Template.Spec.Containers)
			g.Expect(container).ToNot(BeNil(), "cloud-controller-manager container should exist")
			g.Expect(container.Args).To(Equal(tc.expectedArgs))
		})
	}
}
