package ingressoperator

import (
	"maps"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/testutil"

	imagev1 "github.com/openshift/api/image/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAdaptDeployment(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		fips     bool
		validate func(*WithT, *corev1.Container)
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
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			const (
				routerImage   = "quay.io/openshift-release-dev/ocp-v4.0-art-dev:haproxy-router"
				router28Image = "quay.io/openshift-release-dev/ocp-v4.0-art-dev:haproxy-router-haproxy28"
				router32Image = "quay.io/openshift-release-dev/ocp-v4.0-art-dev:haproxy-router-haproxy32"
				canaryImage   = "quay.io/openshift-release-dev/ocp-v4.0-art-dev:cluster-ingress-operator"
			)
			selectionImages := map[string]string{"haproxy-router-haproxy28": router28Image, "haproxy-router-haproxy32": router32Image}
			for _, release := range []struct {
				name           string
				version        string
				userVersion    string
				images         map[string]string
				selection      bool
				missing        []string
				missingLegacy  bool
				errorSubstring string
			}{
				{name: "When both payloads are 4.21 prereleases, it should use the legacy image", version: "4.21.32-candidate", userVersion: "4.21.32-candidate"},
				{name: "When both payloads are 4.21, it should use the legacy image", version: "4.21.32", userVersion: "4.21.32"},
				{name: "When both payloads are 4.22, it should use the legacy image", version: "4.22.0", userVersion: "4.22.0"},
				{name: "When the binary is old and user payload is new, it should omit unsupported flags", version: "4.22.0", userVersion: "4.23.0", images: selectionImages},
				{name: "When the binary is old and newer selection images are missing, it should not require them", version: "4.22.0", userVersion: "4.23.0"},
				{name: "When the binary is new and user payload is old, it should use the legacy image", version: "4.23.0", userVersion: "4.22.0"},
				{name: "When an older user payload has only HAProxy 2.8, it should use the legacy image", version: "4.23.0", userVersion: "4.22.0", images: map[string]string{"haproxy-router-haproxy28": router28Image}},
				{name: "When an older user payload has an empty selection image, it should use the legacy image", version: "4.23.0", userVersion: "4.22.0", images: map[string]string{"haproxy-router-haproxy28": router28Image, "haproxy-router-haproxy32": ""}},
				{name: "When an older user payload has both selection images, it should enable selection", version: "4.23.0", userVersion: "4.22.0", images: selectionImages, selection: true},
				{name: "When both payloads are 4.23 prereleases, it should enable selection", version: "4.23.0-rc.0", userVersion: "4.23.0-rc.0", images: selectionImages, selection: true},
				{name: "When both payloads are 4.23, it should enable selection", version: "4.23.0", userVersion: "4.23.0", images: selectionImages, selection: true},
				{name: "When both payloads are 5.0, it should enable selection", version: "5.0.0", userVersion: "5.0.0", images: selectionImages, selection: true},
				{name: "When newer payload selection images are absent, it should record both missing images", version: "4.23.0", userVersion: "4.23.0", selection: true, missing: []string{"haproxy-router-haproxy28", "haproxy-router-haproxy32"}},
				{name: "When newer payload selection images are empty, it should record both missing images", version: "4.23.0", userVersion: "4.23.0", images: map[string]string{"haproxy-router-haproxy28": "", "haproxy-router-haproxy32": ""}, selection: true, missing: []string{"haproxy-router-haproxy28", "haproxy-router-haproxy32"}},
				{name: "When a newer payload lacks HAProxy 3.2, it should record the missing image", version: "4.23.0", userVersion: "4.23.0", images: map[string]string{"haproxy-router-haproxy28": router28Image}, selection: true, missing: []string{"haproxy-router-haproxy32"}},
				{name: "When a newer payload lacks HAProxy 2.8, it should record the missing image", version: "4.23.0", userVersion: "4.23.0", images: map[string]string{"haproxy-router-haproxy32": router32Image}, selection: true, missing: []string{"haproxy-router-haproxy28"}},
				{name: "When a newer payload has empty HAProxy 3.2, it should record the missing image", version: "4.23.0", userVersion: "4.23.0", images: map[string]string{"haproxy-router-haproxy28": router28Image, "haproxy-router-haproxy32": ""}, selection: true, missing: []string{"haproxy-router-haproxy32"}},
				{name: "When a newer payload has empty HAProxy 2.8, it should record the missing image", version: "4.23.0", userVersion: "4.23.0", images: map[string]string{"haproxy-router-haproxy28": "", "haproxy-router-haproxy32": router32Image}, selection: true, missing: []string{"haproxy-router-haproxy28"}},
				{name: "When the legacy image is absent in an old payload, it should record the missing image", version: "4.22.0", userVersion: "4.22.0", missingLegacy: true, missing: []string{"haproxy-router"}},
				{name: "When the legacy image is absent during fallback, it should record the missing image", version: "4.23.0", userVersion: "4.22.0", missingLegacy: true, missing: []string{"haproxy-router"}},
				{name: "When the legacy image is absent with selection enabled, it should still record the missing image", version: "4.23.0", userVersion: "4.23.0", images: selectionImages, selection: true, missingLegacy: true, missing: []string{"haproxy-router"}},
				{name: "When the legacy image is empty, it should record the missing image", version: "4.23.0", userVersion: "4.22.0", images: map[string]string{"haproxy-router": ""}, missing: []string{"haproxy-router"}},
				{name: "When the control plane version is malformed, it should return a parse error", version: "invalid", userVersion: "4.23.0", errorSubstring: "failed to parse control plane release version"},
				{name: "When the control plane version is empty, it should return a parse error", userVersion: "4.23.0", errorSubstring: "failed to parse control plane release version"},
				{name: "When the user version is malformed, it should return a parse error", version: "4.23.0", userVersion: "invalid", errorSubstring: "failed to parse user release version"},
				{name: "When the user version is empty, it should return a parse error", version: "4.23.0", errorSubstring: "failed to parse user release version"},
			} {
				t.Run(release.name, func(t *testing.T) {
					t.Parallel()
					g := NewWithT(t)
					images := map[string]string{"haproxy-router": routerImage, "cluster-ingress-operator": canaryImage}
					maps.Copy(images, release.images)
					if release.missingLegacy {
						delete(images, "haproxy-router")
					}
					// Use the real provider to exercise missing-image accounting, including empty values.
					userProvider := imageprovider.NewFromImages(images)
					userProvider.ReleaseImage = &releaseinfo.ReleaseImage{ImageStream: &imagev1.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: release.userVersion}}}
					cpContext := component.WorkloadContext{
						HCP:                      &hyperv1.HostedControlPlane{Spec: hyperv1.HostedControlPlaneSpec{FIPS: tc.fips}},
						ReleaseImageProvider:     testutil.FakeImageProvider(testutil.WithVersion(release.version)),
						UserReleaseImageProvider: userProvider,
					}
					deployment, err := assets.LoadDeploymentManifest(ComponentName)
					g.Expect(err).ToNot(HaveOccurred())
					err = adaptDeployment(cpContext, deployment)
					if release.errorSubstring != "" {
						g.Expect(err).To(MatchError(ContainSubstring(release.errorSubstring)))
						return
					}
					g.Expect(err).ToNot(HaveOccurred())
					g.Expect(userProvider.GetMissingImages()).To(ConsistOf(release.missing))
					container := podspec.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
					g.Expect(container).ToNot(BeNil())
					tc.validate(g, container)
					g.Expect(container.Env).To(ContainElements(
						corev1.EnvVar{Name: "IMAGE", Value: images["haproxy-router"]},
						corev1.EnvVar{Name: "CANARY_IMAGE", Value: canaryImage},
						corev1.EnvVar{Name: "RELEASE_VERSION", Value: release.userVersion},
					))
					expectedCommand := []string{
						"ingress-operator", "start", "--namespace", "openshift-ingress-operator",
						"--image", "$(IMAGE)", "--canary-image", "$(CANARY_IMAGE)",
						"--release-version", "$(RELEASE_VERSION)", "--metrics-listen-addr", "0.0.0.0:60000",
					}
					if release.selection {
						expectedCommand = append(expectedCommand,
							"--haproxy-image", "2.8=$(HAPROXY_28_IMAGE)",
							"--haproxy-image", "3.2=$(HAPROXY_32_IMAGE)",
							"--default-haproxy-version", "$(DEFAULT_HAPROXY_VERSION)",
						)
						g.Expect(container.Env).To(ContainElements(
							corev1.EnvVar{Name: "HAPROXY_28_IMAGE", Value: images["haproxy-router-haproxy28"]},
							corev1.EnvVar{Name: "HAPROXY_32_IMAGE", Value: images["haproxy-router-haproxy32"]},
							corev1.EnvVar{Name: "DEFAULT_HAPROXY_VERSION", Value: "3.2"},
						))
					} else {
						for _, env := range container.Env {
							g.Expect(env.Name).NotTo(BeElementOf("HAPROXY_28_IMAGE", "HAPROXY_32_IMAGE", "DEFAULT_HAPROXY_VERSION"))
						}
					}
					// Exact equality checks both occurrences of --haproxy-image and their value pairing.
					g.Expect(container.Command).To(Equal(expectedCommand))
				})
			}
		})
	}
}
