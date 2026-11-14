package extoidc

import (
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/api/util/ipnet"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestAdaptDeployment(t *testing.T) {
	testCases := []struct {
		name                 string
		profile              *configv1.TLSSecurityProfile
		managementHTTPProxy  string
		managementHTTPSProxy string
		managementNoProxy    string
		hostedProxy          *configv1.ProxySpec
		expectedProxyEnv     []corev1.EnvVar
		expectedError        string
	}{
		{
			name: "When the default TLS profile is used, it should configure the expected TLS arguments",
		},
		{
			name: "When the modern TLS profile is used, it should configure the expected TLS arguments",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileModernType,
			},
		},
		{
			name: "When a valid custom TLS profile is used, it should configure the custom TLS arguments",
			profile: &configv1.TLSSecurityProfile{
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
			},
		},
		{
			name: "When a custom TLS profile has no custom settings, it should return an error",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
			},
			expectedError: "TLS profile type is Custom but Custom field is nil",
		},
		{
			name: "When only a hosted cluster proxy is configured, it should leave management connections direct",
			hostedProxy: &configv1.ProxySpec{
				HTTPProxy:  "http://proxy.guest.example.com:3128",
				HTTPSProxy: "http://proxy.guest.example.com:3129",
				NoProxy:    ".guest.example.com",
			},
		},
		{
			name:                 "When management and hosted proxies differ, it should use management proxies and combine management and hosted CIDR bypasses",
			managementHTTPProxy:  "http://proxy.mgmt.example.com:3128",
			managementHTTPSProxy: "http://proxy.mgmt.example.com:3129",
			managementNoProxy:    ".mgmt.example.com,kube-apiserver,10.132.0.0/14",
			hostedProxy: &configv1.ProxySpec{
				HTTPProxy:  "http://proxy.guest.example.com:3128",
				HTTPSProxy: "http://proxy.guest.example.com:3129",
				NoProxy:    ".guest.example.com",
			},
			expectedProxyEnv: []corev1.EnvVar{
				{Name: "HTTP_PROXY", Value: "http://proxy.mgmt.example.com:3128"},
				{Name: "HTTPS_PROXY", Value: "http://proxy.mgmt.example.com:3129"},
				{Name: "NO_PROXY", Value: ".mgmt.example.com,10.132.0.0/14,172.30.0.0/16,kube-apiserver"},
			},
		},
		{
			name:                "When only the management HTTP proxy is configured, it should propagate it with hosted CIDR bypasses",
			managementHTTPProxy: "http://proxy.mgmt.example.com:3128",
			expectedProxyEnv: []corev1.EnvVar{
				{Name: "HTTP_PROXY", Value: "http://proxy.mgmt.example.com:3128"},
				{Name: "NO_PROXY", Value: "10.132.0.0/14,172.30.0.0/16,kube-apiserver"},
			},
		},
		{
			name:                 "When only the management HTTPS proxy is configured, it should propagate it with hosted CIDR bypasses",
			managementHTTPSProxy: "http://proxy.mgmt.example.com:3129",
			expectedProxyEnv: []corev1.EnvVar{
				{Name: "HTTPS_PROXY", Value: "http://proxy.mgmt.example.com:3129"},
				{Name: "NO_PROXY", Value: "10.132.0.0/14,172.30.0.0/16,kube-apiserver"},
			},
		},
		{
			name:              "When management NO_PROXY is set without a proxy, it should leave proxy environment variables unset",
			managementNoProxy: ".mgmt.example.com",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			t.Setenv("HTTP_PROXY", tc.managementHTTPProxy)
			t.Setenv("HTTPS_PROXY", tc.managementHTTPSProxy)
			t.Setenv("NO_PROXY", tc.managementNoProxy)

			deployment, err := assets.LoadDeploymentManifest(ComponentName)
			g.Expect(err).ToNot(HaveOccurred())

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-ns",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Networking: hyperv1.ClusterNetworking{
						ClusterNetwork: []hyperv1.ClusterNetworkEntry{{CIDR: *ipnet.MustParseCIDR("10.132.0.0/14")}},
						ServiceNetwork: []hyperv1.ServiceNetworkEntry{{CIDR: *ipnet.MustParseCIDR("172.30.0.0/16")}},
					},
					Configuration: &hyperv1.ClusterConfiguration{
						Proxy: tc.hostedProxy,
						APIServer: &configv1.APIServerSpec{
							TLSSecurityProfile: tc.profile,
						},
					},
				},
			}

			cpContext := component.WorkloadContext{
				Client: fake.NewClientBuilder().WithScheme(api.Scheme).Build(),
				HCP:    hcp,
			}
			err = adaptDeployment(cpContext, deployment)
			if tc.expectedError != "" {
				g.Expect(err).To(MatchError(ContainSubstring(tc.expectedError)))
				return
			}
			g.Expect(err).ToNot(HaveOccurred())

			container := podspec.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
			g.Expect(container).ToNot(BeNil())
			g.Expect(container.Env).To(ConsistOf(tc.expectedProxyEnv))

			expectedMinTLSVersion, err := config.MinTLSVersion(hcp.Spec.Configuration.GetTLSSecurityProfile())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(container.Args).To(ContainElement(fmt.Sprintf("--tls-min-version=%s", expectedMinTLSVersion)))

			expectedCipherSuites, err := config.CipherSuites(hcp.Spec.Configuration.GetTLSSecurityProfile())
			g.Expect(err).ToNot(HaveOccurred())
			for _, cipherSuite := range expectedCipherSuites {
				g.Expect(container.Args).To(ContainElement(fmt.Sprintf("--tls-cipher-suites=%s", cipherSuite)))
			}
		})
	}
}

func TestDeploymentManifestProxyConfiguration(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	deployment, err := assets.LoadDeploymentManifest(ComponentName)
	g.Expect(err).ToNot(HaveOccurred())

	container := podspec.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
	g.Expect(container).ToNot(BeNil())
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY"} {
		g.Expect(podspec.FindEnvVar(name, container.Env)).To(BeNil(), "%s must not point to a removed sidecar", name)
	}
	g.Expect(podspec.FindVolume("kubeconfig", deployment.Spec.Template.Spec.Volumes)).To(BeNil())
	g.Expect(podspec.FindVolumeMount("openshift-config-managed-trusted-ca-bundle", container.VolumeMounts)).To(Equal(&corev1.VolumeMount{
		Name:      "openshift-config-managed-trusted-ca-bundle",
		MountPath: "/etc/pki/ca-trust/extracted/pem",
		ReadOnly:  true,
	}))
	trustVolume := podspec.FindVolume("openshift-config-managed-trusted-ca-bundle", deployment.Spec.Template.Spec.Volumes)
	g.Expect(trustVolume).ToNot(BeNil())
	g.Expect(trustVolume.ConfigMap).To(Equal(&corev1.ConfigMapVolumeSource{
		LocalObjectReference: corev1.LocalObjectReference{Name: "openshift-config-managed-trusted-ca-bundle"},
		DefaultMode:          ptr.To[int32](420),
		Items:                []corev1.KeyToPath{{Key: "ca-bundle.crt", Path: "tls-ca-bundle.pem"}},
		Optional:             ptr.To(true),
	}))
}
