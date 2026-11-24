package ingressoperator

import (
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/imageprovider"
	"github.com/openshift/hypershift/support/podspec"
	"github.com/openshift/hypershift/support/releaseinfo/testutils"
	"github.com/openshift/hypershift/support/testutil"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"github.com/spf13/pflag"
)

func TestAdaptDeployment(t *testing.T) {
	t.Parallel()

	const image34 = "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	testCases := []struct {
		name              string
		fips              bool
		guestVersion      string
		guestImage34      *string
		controlPlaneImage string
		expectImage34     bool
		missingImage32    bool
		validate          func(*WithT, *corev1.Container)
	}{
		{
			name: "When FIPS is enabled, it should set FIPS_ENABLED env var to true",
			fips: true,
			validate: func(g *WithT, container *corev1.Container) {
				g.Expect(container.Env).To(ContainElement(corev1.EnvVar{
					Name:  "FIPS_ENABLED",
					Value: "true",
				}))
			},
		},
		{
			name: "When FIPS is not enabled, it should not set FIPS_ENABLED env var",
			fips: false,
			validate: func(g *WithT, container *corev1.Container) {
				for _, env := range container.Env {
					g.Expect(env.Name).ToNot(Equal("FIPS_ENABLED"))
				}
			},
		},
		{
			name:          "When the guest payload has HAProxy 3.4, it should expose it without changing the default",
			guestVersion:  "5.1.0",
			guestImage34:  ptr.To(image34),
			expectImage34: true,
		},
		{
			name:         "When a legacy guest payload lacks HAProxy 3.4, it should omit the optional mapping",
			guestVersion: "4.18.0",
		},
		{
			name:         "When a 5.1 guest payload lacks HAProxy 3.4, it should not infer image availability from its version",
			guestVersion: "5.1.0",
		},
		{
			name:         "When the guest HAProxy 3.4 image is empty, it should omit both the environment and argument",
			guestVersion: "5.1.0",
			guestImage34: ptr.To(""),
		},
		{
			name:              "When only the control plane payload has HAProxy 3.4, it should not expose that image to the guest",
			controlPlaneImage: "registry.ci.openshift.org/ocp/5.1:haproxy-router-haproxy34",
		},
		{
			name:              "When split payloads have different HAProxy 3.4 images, it should use the guest image",
			guestVersion:      "5.1.0",
			guestImage34:      ptr.To(image34),
			controlPlaneImage: "registry.ci.openshift.org/ocp/5.1:haproxy-router-haproxy34",
			expectImage34:     true,
		},
		{
			name:           "When the mandatory HAProxy 3.2 image is missing, it should preserve missing image diagnostics",
			guestVersion:   "5.1.0",
			guestImage34:   ptr.To(image34),
			expectImage34:  true,
			missingImage32: true,
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

			guestImages := map[string]string{
				"haproxy-router":           "registry.ci.openshift.org/ocp/5.1:haproxy-router",
				"haproxy-router-haproxy32": "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				"cluster-ingress-operator": "registry.ci.openshift.org/ocp/5.1:cluster-ingress-operator",
			}
			if tc.guestImage34 != nil {
				guestImages["haproxy-router-haproxy34"] = *tc.guestImage34
			}
			if tc.missingImage32 {
				delete(guestImages, "haproxy-router-haproxy32")
			}
			guestProvider := imageprovider.NewFromImages(guestImages)
			guestProvider.ReleaseImage = testutils.InitReleaseImageOrDie("5.1.0")
			if tc.guestVersion != "" {
				guestProvider.ReleaseImage = testutils.InitReleaseImageOrDie(tc.guestVersion)
			}
			controlPlaneImages := map[string]string{}
			if tc.controlPlaneImage != "" {
				controlPlaneImages["haproxy-router-haproxy34"] = tc.controlPlaneImage
			}
			cpContext := component.WorkloadContext{
				HCP:                      hcp,
				ReleaseImageProvider:     testutil.FakeImageProvider(testutil.WithImages(controlPlaneImages)),
				UserReleaseImageProvider: guestProvider,
			}

			deployment, err := assets.LoadDeploymentManifest(ComponentName)
			g.Expect(err).ToNot(HaveOccurred())

			err = adaptDeployment(cpContext, deployment)
			g.Expect(err).ToNot(HaveOccurred())

			container := podspec.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
			g.Expect(container).ToNot(BeNil(), "ingress-operator container should exist")

			if tc.validate != nil {
				tc.validate(g, container)
			}
			g.Expect(container.Env).To(ContainElements(
				corev1.EnvVar{Name: "HAPROXY_32_IMAGE", Value: guestImages["haproxy-router-haproxy32"]},
				corev1.EnvVar{Name: "DEFAULT_HAPROXY_VERSION", Value: "3.2"},
				corev1.EnvVar{Name: "IMAGE", Value: guestImages["haproxy-router"]},
				corev1.EnvVar{Name: "CANARY_IMAGE", Value: guestImages["cluster-ingress-operator"]},
				corev1.EnvVar{Name: "RELEASE_VERSION", Value: guestProvider.Version()},
			))
			expectedMappings := map[string]string{"3.2": guestImages["haproxy-router-haproxy32"]}
			if tc.expectImage34 {
				g.Expect(container.Env).To(ContainElement(corev1.EnvVar{Name: "HAPROXY_34_IMAGE", Value: image34}))
				expectedMappings["3.4"] = image34
			} else {
				g.Expect(container.Env).ToNot(ContainElement(HaveField("Name", "HAPROXY_34_IMAGE")))
				g.Expect(strings.Join(container.Command, " ")).ToNot(ContainSubstring("HAPROXY_34_IMAGE"))
			}
			mappingArgs := []string{}
			for index, arg := range container.Command {
				if arg == "--haproxy-image" {
					g.Expect(index + 1).To(BeNumerically("<", len(container.Command)))
					mapping := strings.NewReplacer("$(HAPROXY_32_IMAGE)", guestImages["haproxy-router-haproxy32"], "$(HAPROXY_34_IMAGE)", image34).Replace(container.Command[index+1])
					g.Expect(mapping).ToNot(ContainSubstring("$("))
					mappingArgs = append(mappingArgs, arg, mapping)
				}
			}
			flags := pflag.NewFlagSet("ingress-operator", pflag.ContinueOnError)
			mappings := flags.StringToString("haproxy-image", nil, "")
			g.Expect(flags.Parse(mappingArgs)).To(Succeed())
			g.Expect(*mappings).To(Equal(expectedMappings))
			if tc.missingImage32 {
				g.Expect(guestProvider.GetMissingImages()).To(Equal([]string{"haproxy-router-haproxy32"}))
			} else {
				g.Expect(guestProvider.GetMissingImages()).To(BeEmpty())
			}
			first := deployment.DeepCopy()
			g.Expect(adaptDeployment(cpContext, deployment)).To(Succeed())
			g.Expect(deployment).To(Equal(first))
		})
	}
}
