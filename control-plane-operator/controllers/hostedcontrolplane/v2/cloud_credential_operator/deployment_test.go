package cco

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAdaptDeployment(t *testing.T) {
	testCases := []struct {
		name               string
		releaseVersion     string
		userReleaseVersion string
		httpProxy          string
		httpsProxy         string
		noProxy            string
		validate           func(*WithT, *corev1.Container)
	}{
		{
			name:               "When release version is set, it should add RELEASE_VERSION env var",
			releaseVersion:     "4.17.0",
			userReleaseVersion: "4.17.0",
			validate: func(g *WithT, container *corev1.Container) {
				g.Expect(container.Env).To(ContainElement(corev1.EnvVar{
					Name:  "RELEASE_VERSION",
					Value: "4.17.0",
				}))
			},
		},
		{
			name:               "When different release version is set, it should use that version",
			releaseVersion:     "4.18.0-rc.1",
			userReleaseVersion: "4.18.0-rc.1",
			validate: func(g *WithT, container *corev1.Container) {
				g.Expect(container.Env).To(ContainElement(corev1.EnvVar{
					Name:  "RELEASE_VERSION",
					Value: "4.18.0-rc.1",
				}))
			},
		},
		{
			name:               "When the control plane payload is skewed, it should set RELEASE_VERSION from the user payload",
			releaseVersion:     "4.23.0-0.nightly-2026-11-13-121834",
			userReleaseVersion: "4.22.10",
			validate: func(g *WithT, container *corev1.Container) {
				g.Expect(container.Env).To(ContainElement(corev1.EnvVar{
					Name:  "RELEASE_VERSION",
					Value: "4.22.10",
				}))
				g.Expect(container.Env).ToNot(ContainElement(corev1.EnvVar{
					Name:  "RELEASE_VERSION",
					Value: "4.23.0-0.nightly-2026-11-13-121834",
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
			userReleaseImageProvider := testutil.FakeImageProvider(testutil.WithVersion(tc.userReleaseVersion))

			cpContext := component.WorkloadContext{
				HCP:                      hcp,
				ReleaseImageProvider:     releaseImageProvider,
				UserReleaseImageProvider: userReleaseImageProvider,
			}

			deployment, err := assets.LoadDeploymentManifest(ComponentName)
			g.Expect(err).ToNot(HaveOccurred())

			err = adaptDeployment(cpContext, deployment)
			g.Expect(err).ToNot(HaveOccurred())

			// Find the cloud-credential-operator container
			ccoContainer := util.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
			g.Expect(ccoContainer).ToNot(BeNil(), "cloud-credential-operator container should exist")

			tc.validate(g, ccoContainer)
		})
	}
}
