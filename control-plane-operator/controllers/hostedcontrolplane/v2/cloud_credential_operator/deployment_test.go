package cco

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"
	"github.com/openshift/hypershift/support/testutil"

	configv1 "github.com/openshift/api/config/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAdaptDeployment(t *testing.T) {
	testCases := []struct {
		name           string
		releaseVersion string
		httpProxy      string
		httpsProxy     string
		noProxy        string
		validate       func(*WithT, *corev1.Container)
	}{
		{
			name:           "When release version is set, it should add RELEASE_VERSION env var",
			releaseVersion: "4.17.0",
			validate: func(g *WithT, container *corev1.Container) {
				g.Expect(container.Env).To(ContainElement(corev1.EnvVar{
					Name:  "RELEASE_VERSION",
					Value: "4.17.0",
				}))
			},
		},
		{
			name:           "When different release version is set, it should use that version",
			releaseVersion: "4.18.0-rc.1",
			validate: func(g *WithT, container *corev1.Container) {
				g.Expect(container.Env).To(ContainElement(corev1.EnvVar{
					Name:  "RELEASE_VERSION",
					Value: "4.18.0-rc.1",
				}))
			},
		},
		{
			name:           "When proxy environment variables are set, it should add proxy env vars to container",
			releaseVersion: "4.17.0",
			httpProxy:      "http://proxy.example.com:8080",
			httpsProxy:     "https://proxy.example.com:8443",
			noProxy:        "localhost,127.0.0.1",
			validate: func(g *WithT, container *corev1.Container) {
				g.Expect(container.Env).To(ContainElements(
					corev1.EnvVar{
						Name:  "HTTP_PROXY",
						Value: "http://proxy.example.com:8080",
					},
					corev1.EnvVar{
						Name:  "HTTPS_PROXY",
						Value: "https://proxy.example.com:8443",
					},
				))
				// NO_PROXY will have kube-apiserver added
				var foundNoProxy bool
				for _, env := range container.Env {
					if env.Name == "NO_PROXY" {
						foundNoProxy = true
						g.Expect(env.Value).To(ContainSubstring("localhost"))
						g.Expect(env.Value).To(ContainSubstring("127.0.0.1"))
						g.Expect(env.Value).To(ContainSubstring("kube-apiserver"))
					}
				}
				g.Expect(foundNoProxy).To(BeTrue())
			},
		},
		{
			name:           "When no proxy is set, it should not add proxy env vars",
			releaseVersion: "4.17.0",
			validate: func(g *WithT, container *corev1.Container) {
				for _, env := range container.Env {
					g.Expect(env.Name).ToNot(Equal("HTTP_PROXY"))
					g.Expect(env.Name).ToNot(Equal("HTTPS_PROXY"))
					g.Expect(env.Name).ToNot(Equal("NO_PROXY"))
				}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			// Set deterministic baseline for proxy environment variables
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

			releaseImageProvider := testutil.FakeImageProvider(testutil.WithVersion(tc.releaseVersion))

			cpContext := component.WorkloadContext{
				HCP:                  hcp,
				ReleaseImageProvider: releaseImageProvider,
			}

			deployment, err := assets.LoadDeploymentManifest(ComponentName)
			g.Expect(err).ToNot(HaveOccurred())

			err = adaptDeployment(cpContext, deployment)
			g.Expect(err).ToNot(HaveOccurred())

			// Find the cloud-credential-operator container
			ccoContainer := podspec.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
			g.Expect(ccoContainer).ToNot(BeNil(), "cloud-credential-operator container should exist")

			tc.validate(g, ccoContainer)
		})
	}
}

func TestAdaptDeploymentUpdatesContainer(t *testing.T) {
	t.Parallel()

	t.Run("When adaptDeployment is called, it should not return an error", func(t *testing.T) {
		g := NewWithT(t)

		hcp := &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-hcp",
				Namespace: "test-namespace",
			},
			Spec: hyperv1.HostedControlPlaneSpec{},
		}

		releaseImageProvider := testutil.FakeImageProvider(testutil.WithVersion("4.17.0"))

		cpContext := component.WorkloadContext{
			HCP:                  hcp,
			ReleaseImageProvider: releaseImageProvider,
		}

		deployment, err := assets.LoadDeploymentManifest(ComponentName)
		g.Expect(err).ToNot(HaveOccurred())

		err = adaptDeployment(cpContext, deployment)
		g.Expect(err).ToNot(HaveOccurred())
	})

	t.Run("When adaptDeployment is called, it should preserve other containers", func(t *testing.T) {
		g := NewWithT(t)

		hcp := &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-hcp",
				Namespace: "test-namespace",
			},
			Spec: hyperv1.HostedControlPlaneSpec{},
		}

		releaseImageProvider := testutil.FakeImageProvider(testutil.WithVersion("4.17.0"))

		cpContext := component.WorkloadContext{
			HCP:                  hcp,
			ReleaseImageProvider: releaseImageProvider,
		}

		deployment, err := assets.LoadDeploymentManifest(ComponentName)
		g.Expect(err).ToNot(HaveOccurred())

		originalContainerNames := make([]string, len(deployment.Spec.Template.Spec.Containers))
		for i, c := range deployment.Spec.Template.Spec.Containers {
			originalContainerNames[i] = c.Name
		}

		err = adaptDeployment(cpContext, deployment)
		g.Expect(err).ToNot(HaveOccurred())

		// The deployment should still have all its original containers
		g.Expect(deployment.Spec.Template.Spec.Containers).To(HaveLen(len(originalContainerNames)))
		for _, name := range originalContainerNames {
			found := false
			for _, c := range deployment.Spec.Template.Spec.Containers {
				if c.Name == name {
					found = true
					break
				}
			}
			g.Expect(found).To(BeTrue(), "container %s should be preserved after adaptDeployment", name)
		}
	})
}

func buildHostedControlPlane(tlsProfile *configv1.TLSSecurityProfile) *hyperv1.HostedControlPlane {
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-namespace",
		},
		Spec: hyperv1.HostedControlPlaneSpec{},
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
							Name: ComponentName,
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
		"operator",
		"--kubeconfig=/etc/kubernetes/kubeconfig",
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
			name: "When using Intermediate TLS profile it should append intermediate TLS args",
			tlsProfile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileIntermediateType,
			},
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
				HCP:                  hcp,
				ReleaseImageProvider: testutil.FakeImageProvider(testutil.WithVersion("4.17.0")),
			}
			err := adaptDeployment(cpContext, deployment)
			g.Expect(err).ToNot(HaveOccurred())

			container := podspec.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
			g.Expect(container).ToNot(BeNil(), "cloud-credential-operator container should exist")
			g.Expect(container.Args).To(Equal(tc.expectedArgs))
		})
	}
}
