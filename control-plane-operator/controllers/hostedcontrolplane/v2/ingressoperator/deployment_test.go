package ingressoperator

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"
	releaseinfotestutils "github.com/openshift/hypershift/support/releaseinfo/testutils"
	"github.com/openshift/hypershift/support/testutil"

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
				UserReleaseImageProvider: testutil.FakeImageProvider(),
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

// newReleaseImageProvider builds a real SimpleReleaseImageProvider for the given version
// whose component-image catalog contains exactly the supplied image keys. This mirrors the
// production tripwire: GetImage() of an absent key records it on the missing-images list
// (which flips IgnitionServerValidReleaseInfo to False), whereas the fake provider swallows
// missing lookups and cannot exercise this behavior.
func newReleaseImageProvider(version string, imageKeys ...string) *imageprovider.SimpleReleaseImageProvider {
	images := map[string]string{}
	for _, k := range imageKeys {
		images[k] = "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:" + k
	}
	p := imageprovider.NewFromImages(images)
	p.ReleaseImage = releaseinfotestutils.InitReleaseImageOrDie(version)
	return p
}

// TestAdaptDeploymentHAProxyImagesByPayloadVersion is the main-CPO-on-4.20 regression test:
// a main-era CPO driving an OLD (< 5.0) payload must demand only the images that payload
// ships (haproxy-router), and must NOT request the per-version haproxy tags that only exist
// in >= 5.0 payloads. Requesting an absent tag would be recorded as a missing release image
// and flip IgnitionServerValidReleaseInfo=False (MissingReleaseImages).
func TestAdaptDeploymentHAProxyImagesByPayloadVersion(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name                 string
		provider             *imageprovider.SimpleReleaseImageProvider
		expectVersionedImage bool
	}{
		{
			name: "4.20 payload (no per-version haproxy tags): demands only haproxy-router, records no missing images",
			// A realistic 4.20 catalog: has haproxy-router + cluster-ingress-operator, but NOT
			// the haproxy-router-haproxy32 tag that #9040 introduced in >= 5.0.
			provider:             newReleaseImageProvider("4.20.42", "haproxy-router", "cluster-ingress-operator"),
			expectVersionedImage: false,
		},
		{
			name:                 "5.0 payload: demands the per-version haproxy image",
			provider:             newReleaseImageProvider("5.0.0", "haproxy-router", "cluster-ingress-operator", "haproxy-router-haproxy32"),
			expectVersionedImage: true,
		},
		{
			// Pre-GA 5.0 payloads (nightly/EC/RC) are the norm in CI; semver ranks any
			// pre-release BELOW the release version, so without stripping .Pre the gate would
			// wrongly treat this as < 5.0 and skip the versioned haproxy wiring.
			name:                 "5.0 pre-release payload (nightly): still demands the per-version haproxy image",
			provider:             newReleaseImageProvider("5.0.0-0.nightly-2026-07-20-000000", "haproxy-router", "cluster-ingress-operator", "haproxy-router-haproxy32"),
			expectVersionedImage: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Name: "test-hcp", Namespace: "test-namespace"},
			}
			cpContext := component.WorkloadContext{
				HCP:                      hcp,
				UserReleaseImageProvider: tc.provider,
			}

			deployment, err := assets.LoadDeploymentManifest(ComponentName)
			g.Expect(err).ToNot(HaveOccurred())

			err = adaptDeployment(cpContext, deployment)
			g.Expect(err).ToNot(HaveOccurred())

			// The core assertion: no image the CPO asked for is absent from the payload.
			// This is exactly what IgnitionServerValidReleaseInfo evaluates.
			g.Expect(tc.provider.GetMissingImages()).To(BeEmpty(),
				"CPO must not request images absent from the payload it is driving")

			container := podspec.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
			g.Expect(container).ToNot(BeNil())

			hasVersionedImage := false
			hasDefaultHAProxyVersion := false
			for _, env := range container.Env {
				if env.Name == "HAPROXY_32_IMAGE" {
					hasVersionedImage = true
				}
				if env.Name == "DEFAULT_HAPROXY_VERSION" {
					hasDefaultHAProxyVersion = true
				}
			}
			g.Expect(hasVersionedImage).To(Equal(tc.expectVersionedImage),
				"HAPROXY_32_IMAGE env var presence should match payload version")
			g.Expect(hasDefaultHAProxyVersion).To(Equal(tc.expectVersionedImage),
				"DEFAULT_HAPROXY_VERSION env var presence should match payload version")

			// The base haproxy-router image must always be demanded (it exists in every payload).
			g.Expect(container.Env).To(ContainElement(HaveField("Name", "IMAGE")))
		})
	}
}
