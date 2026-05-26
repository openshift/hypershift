package pkioperator

import (
	"testing"
	"time"

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
		name              string
		hcpName           string
		certRotationScale time.Duration
		httpProxy         string
		httpsProxy        string
		noProxy           string
		validate          func(*WithT, *corev1.Container)
	}{
		{
			name:              "When HCP name is set, it should add HOSTED_CONTROL_PLANE_NAME env var",
			hcpName:           "test-hcp",
			certRotationScale: time.Hour,
			validate: func(g *WithT, container *corev1.Container) {
				g.Expect(container.Env).To(ContainElement(corev1.EnvVar{
					Name:  "HOSTED_CONTROL_PLANE_NAME",
					Value: "test-hcp",
				}))
			},
		},
		{
			name:              "When different HCP name is set, it should use that name",
			hcpName:           "another-hcp",
			certRotationScale: time.Hour,
			validate: func(g *WithT, container *corev1.Container) {
				g.Expect(container.Env).To(ContainElement(corev1.EnvVar{
					Name:  "HOSTED_CONTROL_PLANE_NAME",
					Value: "another-hcp",
				}))
			},
		},
		{
			name:              "When cert rotation scale is 1 hour, it should add CERT_ROTATION_SCALE env var",
			hcpName:           "test-hcp",
			certRotationScale: time.Hour,
			validate: func(g *WithT, container *corev1.Container) {
				g.Expect(container.Env).To(ContainElement(corev1.EnvVar{
					Name:  "CERT_ROTATION_SCALE",
					Value: "1h0m0s",
				}))
			},
		},
		{
			name:              "When cert rotation scale is 24 hours, it should format correctly",
			hcpName:           "test-hcp",
			certRotationScale: 24 * time.Hour,
			validate: func(g *WithT, container *corev1.Container) {
				g.Expect(container.Env).To(ContainElement(corev1.EnvVar{
					Name:  "CERT_ROTATION_SCALE",
					Value: "24h0m0s",
				}))
			},
		},
		{
			name:              "When cert rotation scale is 30 minutes, it should format correctly",
			hcpName:           "test-hcp",
			certRotationScale: 30 * time.Minute,
			validate: func(g *WithT, container *corev1.Container) {
				g.Expect(container.Env).To(ContainElement(corev1.EnvVar{
					Name:  "CERT_ROTATION_SCALE",
					Value: "30m0s",
				}))
			},
		},
		{
			name:              "When proxy environment variables are set, it should add proxy env vars to container",
			hcpName:           "test-hcp",
			certRotationScale: time.Hour,
			httpProxy:         "http://proxy.example.com:8080",
			httpsProxy:        "https://proxy.example.com:8443",
			noProxy:           "localhost,127.0.0.1",
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
			name:              "When no proxy is set, it should not add proxy env vars",
			hcpName:           "test-hcp",
			certRotationScale: time.Hour,
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
					Name:      tc.hcpName,
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{},
			}

			operator := &pkiOperator{
				certRotationScale: tc.certRotationScale,
			}

			cpContext := component.WorkloadContext{
				HCP: hcp,
			}

			deployment, err := assets.LoadDeploymentManifest(ComponentName)
			g.Expect(err).ToNot(HaveOccurred())

			err = operator.adaptDeployment(cpContext, deployment)
			g.Expect(err).ToNot(HaveOccurred())

			// Find the control-plane-pki-operator container
			pkiContainer := podspec.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
			g.Expect(pkiContainer).ToNot(BeNil(), "control-plane-pki-operator container should exist")

			tc.validate(g, pkiContainer)
		})
	}
}

func TestAdaptDeploymentCombinedEnvVars(t *testing.T) {
	g := NewWithT(t)

	t.Setenv("HTTP_PROXY", "http://proxy.example.com:8080")
	t.Setenv("HTTPS_PROXY", "https://proxy.example.com:8443")
	t.Setenv("NO_PROXY", "localhost,127.0.0.1")

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "combined-test-hcp",
			Namespace: "test-namespace",
		},
		Spec: hyperv1.HostedControlPlaneSpec{},
	}

	operator := &pkiOperator{
		certRotationScale: 2 * time.Hour,
	}

	cpContext := component.WorkloadContext{
		HCP: hcp,
	}

	deployment, err := assets.LoadDeploymentManifest(ComponentName)
	g.Expect(err).ToNot(HaveOccurred())

	err = operator.adaptDeployment(cpContext, deployment)
	g.Expect(err).ToNot(HaveOccurred())

	// Find the control-plane-pki-operator container
	pkiContainer := podspec.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
	g.Expect(pkiContainer).ToNot(BeNil())

	t.Run("When all configuration is set, it should include all environment variables", func(t *testing.T) {
		g.Expect(pkiContainer.Env).To(ContainElements(
			corev1.EnvVar{
				Name:  "HOSTED_CONTROL_PLANE_NAME",
				Value: "combined-test-hcp",
			},
			corev1.EnvVar{
				Name:  "CERT_ROTATION_SCALE",
				Value: "2h0m0s",
			},
			corev1.EnvVar{
				Name:  "HTTP_PROXY",
				Value: "http://proxy.example.com:8080",
			},
			corev1.EnvVar{
				Name:  "HTTPS_PROXY",
				Value: "https://proxy.example.com:8443",
			},
		))
		// Check NO_PROXY contains expected values (it will also have kube-apiserver added)
		var foundNoProxy bool
		for _, env := range pkiContainer.Env {
			if env.Name == "NO_PROXY" {
				foundNoProxy = true
				g.Expect(env.Value).To(ContainSubstring("localhost"))
				g.Expect(env.Value).To(ContainSubstring("127.0.0.1"))
				g.Expect(env.Value).To(ContainSubstring("kube-apiserver"))
			}
		}
		g.Expect(foundNoProxy).To(BeTrue())
	})
}

func TestAdaptDeploymentReturnsNoError(t *testing.T) {
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

		operator := &pkiOperator{
			certRotationScale: time.Hour,
		}

		cpContext := component.WorkloadContext{
			HCP: hcp,
		}

		deployment, err := assets.LoadDeploymentManifest(ComponentName)
		g.Expect(err).ToNot(HaveOccurred())

		err = operator.adaptDeployment(cpContext, deployment)
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

		operator := &pkiOperator{
			certRotationScale: time.Hour,
		}

		cpContext := component.WorkloadContext{
			HCP: hcp,
		}

		deployment, err := assets.LoadDeploymentManifest(ComponentName)
		g.Expect(err).ToNot(HaveOccurred())

		err = operator.adaptDeployment(cpContext, deployment)
		g.Expect(err).ToNot(HaveOccurred())

		g.Expect(len(deployment.Spec.Template.Spec.Containers)).To(BeNumerically(">", 0))
	})
}
