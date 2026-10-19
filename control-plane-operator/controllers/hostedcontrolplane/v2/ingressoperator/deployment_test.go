package ingressoperator

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"
	"github.com/openshift/hypershift/support/testutil"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAdaptDeployment(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		fips     bool
		version  string
		images   map[string]string
		validate func(*WithT, *corev1.Container)
	}{
		{
			name:    "When an older payload has no versioned images, it should use the legacy command",
			version: "4.22.17",
			images:  map[string]string{"haproxy-router": "router-image", "cluster-ingress-operator": "operator-image"},
			validate: func(g *WithT, container *corev1.Container) {
				g.Expect(container.Command[0]).To(Equal("ingress-operator"))
				g.Expect(container.Command).NotTo(ContainElement("--haproxy-image"))
				g.Expect(container.Env).To(ContainElement(corev1.EnvVar{Name: "IMAGE", Value: "router-image"}))
			},
		},
		{
			name:    "When a future OCP release retains versioned images, it should detect capabilities without a version cutoff",
			version: "6.0.0",
			images:  map[string]string{"haproxy-router": "router-image", "haproxy-router-haproxy32": "haproxy32-image", "cluster-ingress-operator": "operator-image"},
			validate: func(g *WithT, container *corev1.Container) {
				g.Expect(container.Command[0]).To(Equal("/bin/bash"))
				g.Expect(container.Args).To(ContainElement("--haproxy-image=3.2=haproxy32-image"))
				g.Expect(container.Env).To(ContainElement(corev1.EnvVar{Name: "RELEASE_VERSION", Value: "6.0.0"}))
			},
		},
		{
			name:    "When FIPS is enabled, it should set FIPS_ENABLED env var to true",
			fips:    true,
			version: "4.18.0",
			images:  map[string]string{"haproxy-router": "router-image", "cluster-ingress-operator": "operator-image"},
			validate: func(g *WithT, container *corev1.Container) {
				g.Expect(container.Env).To(ContainElement(corev1.EnvVar{
					Name:  "FIPS_ENABLED",
					Value: "true",
				}))
			},
		},
		{
			name:    "When FIPS is not enabled, it should not set FIPS_ENABLED env var",
			fips:    false,
			version: "4.18.0",
			images:  map[string]string{"haproxy-router": "router-image", "cluster-ingress-operator": "operator-image"},
			validate: func(g *WithT, container *corev1.Container) {
				for _, env := range container.Env {
					g.Expect(env.Name).ToNot(Equal("FIPS_ENABLED"))
				}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					FIPS: tc.fips,
				},
			}

			cpContext := component.WorkloadContext{
				HCP:                      hcp,
				UserReleaseImageProvider: testutil.FakeImageProvider(testutil.WithVersion(tc.version), testutil.WithImages(tc.images)),
			}

			deployment, err := assets.LoadDeploymentManifest(ComponentName)
			g.Expect(err).ToNot(HaveOccurred())

			err = adaptDeployment(cpContext, deployment)
			g.Expect(err).ToNot(HaveOccurred())

			container := podspec.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
			g.Expect(container).ToNot(BeNil(), "ingress-operator container should exist")

			tc.validate(g, container)
		})
	}
}
