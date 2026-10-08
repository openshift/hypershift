package ingressoperator

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/blang/semver"
)

var _ imageprovider.ReleaseImageProvider = &trackingImageProvider{}
var _ imageprovider.ComponentImageOverrideProvider = &trackingImageProvider{}

func TestAdaptDeployment(t *testing.T) {
	t.Parallel()

	const (
		legacyHAProxyImage = "registry.ci.openshift.org/ocp/4.23:haproxy-router"
		haproxy28Image     = "registry.ci.openshift.org/ocp/4.23:haproxy-router-haproxy28"
		haproxy32Image     = "registry.ci.openshift.org/ocp/4.23:haproxy-router-haproxy32"
		canaryImage        = "registry.ci.openshift.org/ocp/4.23:cluster-ingress-operator"
	)

	testCases := []struct {
		name                          string
		controlPlaneVersion           string
		userVersion                   string
		controlPlaneImageOverridden   bool
		fips                          bool
		expectHAProxyVersionSelection bool
		expectedError                 string
	}{
		{
			name:                "When FIPS is enabled for an older release, it should preserve FIPS and the legacy HAProxy image configuration",
			controlPlaneVersion: "4.18.0",
			userVersion:         "4.18.0",
			fips:                true,
		},
		{
			name:                "When FIPS is not enabled, it should not set the FIPS environment variable",
			controlPlaneVersion: "4.18.0",
			userVersion:         "4.18.0",
		},
		{
			name:                "When both releases are 4.21, it should use the legacy HAProxy image configuration",
			controlPlaneVersion: "4.21.0",
			userVersion:         "4.21.0",
		},
		{
			name:                "When both releases are 4.22 multi nightlies, it should use the legacy HAProxy image configuration",
			controlPlaneVersion: "4.22.0-0.nightly-multi-2026-10-07-120000",
			userVersion:         "4.22.0-0.nightly-multi-2026-10-07-120000",
		},
		{
			name:                "When only the user release supports HAProxy version selection, it should use the legacy configuration",
			controlPlaneVersion: "4.22.0",
			userVersion:         "4.23.0",
		},
		{
			name:                "When only the control plane release supports HAProxy version selection, it should use the legacy configuration",
			controlPlaneVersion: "4.23.0",
			userVersion:         "4.22.0",
		},
		{
			name:                          "When both releases are 4.23 multi nightlies, it should configure versioned HAProxy images",
			controlPlaneVersion:           "4.23.0-0.nightly-multi-2026-10-07-120000",
			userVersion:                   "4.23.0-0.nightly-multi-2026-10-07-120000",
			expectHAProxyVersionSelection: true,
		},
		{
			name:                          "When both releases are final 4.23, it should configure versioned HAProxy images",
			controlPlaneVersion:           "4.23.0",
			userVersion:                   "4.23.1",
			expectHAProxyVersionSelection: true,
		},
		{
			name:                          "When releases use OCP 5 versions, it should configure versioned HAProxy images",
			controlPlaneVersion:           "5.0.0",
			userVersion:                   "5.1.0",
			expectHAProxyVersionSelection: true,
		},
		{
			name:                        "When a 4.23 release overrides the ingress operator with a 4.22 binary, it should use the legacy configuration",
			controlPlaneVersion:         "4.23.0",
			userVersion:                 "4.23.0",
			controlPlaneImageOverridden: true,
		},
		{
			name:                        "When an OCP 5 release overrides the ingress operator with a 4.22 binary, it should use the legacy configuration",
			controlPlaneVersion:         "5.1.0",
			userVersion:                 "5.1.0",
			controlPlaneImageOverridden: true,
		},
		{
			name:                "When the control plane release version is invalid, it should return a contextual error",
			controlPlaneVersion: "not-a-version",
			userVersion:         "4.23.0",
			expectedError:       "failed to parse control plane release version \"not-a-version\"",
		},
		{
			name:                "When the user release version is invalid, it should return a contextual error",
			controlPlaneVersion: "4.23.0",
			userVersion:         "not-a-version",
			expectedError:       "failed to parse user release version \"not-a-version\"",
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

			controlPlaneProvider := &trackingImageProvider{
				version:          tc.controlPlaneVersion,
				images:           map[string]string{},
				requestedImages:  sets.New[string](),
				missingImages:    sets.New[string](),
				overriddenImages: sets.New[string](),
			}
			if tc.controlPlaneImageOverridden {
				controlPlaneProvider.overriddenImages.Insert("cluster-ingress-operator")
			}

			userImages := map[string]string{
				"haproxy-router":           legacyHAProxyImage,
				"cluster-ingress-operator": canaryImage,
			}
			if tc.expectHAProxyVersionSelection {
				userImages["haproxy-router-haproxy28"] = haproxy28Image
				userImages["haproxy-router-haproxy32"] = haproxy32Image
			}
			userProvider := &trackingImageProvider{
				version:          tc.userVersion,
				images:           userImages,
				requestedImages:  sets.New[string](),
				missingImages:    sets.New[string](),
				overriddenImages: sets.New[string](),
			}

			cpContext := component.WorkloadContext{
				HCP:                      hcp,
				ReleaseImageProvider:     controlPlaneProvider,
				UserReleaseImageProvider: userProvider,
			}

			deployment, err := assets.LoadDeploymentManifest(ComponentName)
			g.Expect(err).ToNot(HaveOccurred())

			err = adaptDeployment(cpContext, deployment)
			if tc.expectedError != "" {
				g.Expect(err).To(MatchError(ContainSubstring(tc.expectedError)))
				return
			}
			g.Expect(err).ToNot(HaveOccurred())

			container := podspec.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
			g.Expect(container).ToNot(BeNil(), "ingress-operator container should exist")

			g.Expect(podspec.FindEnvVar("IMAGE", container.Env)).To(Equal(&corev1.EnvVar{
				Name:  "IMAGE",
				Value: legacyHAProxyImage,
			}))
			g.Expect(podspec.FindEnvVar("RELEASE_VERSION", container.Env)).To(Equal(&corev1.EnvVar{
				Name:  "RELEASE_VERSION",
				Value: tc.userVersion,
			}))
			g.Expect(podspec.FindEnvVar("CANARY_IMAGE", container.Env)).To(Equal(&corev1.EnvVar{
				Name:  "CANARY_IMAGE",
				Value: canaryImage,
			}))

			if tc.fips {
				g.Expect(podspec.FindEnvVar("FIPS_ENABLED", container.Env)).To(Equal(&corev1.EnvVar{
					Name:  "FIPS_ENABLED",
					Value: "true",
				}))
			} else {
				g.Expect(podspec.FindEnvVar("FIPS_ENABLED", container.Env)).To(BeNil())
			}

			expectedCommand := []string{
				"ingress-operator", "start",
				"--namespace", "openshift-ingress-operator",
				"--image", "$(IMAGE)",
				"--canary-image", "$(CANARY_IMAGE)",
				"--release-version", "$(RELEASE_VERSION)",
				"--metrics-listen-addr", "0.0.0.0:60000",
			}

			if tc.expectHAProxyVersionSelection {
				expectedCommand = append(expectedCommand,
					"--haproxy-image", "2.8=$(HAPROXY_28_IMAGE)",
					"--haproxy-image", "3.2=$(HAPROXY_32_IMAGE)",
					"--default-haproxy-version", "$(DEFAULT_HAPROXY_VERSION)",
				)
				g.Expect(container.Command).To(Equal(expectedCommand))
				g.Expect(podspec.FindEnvVar("HAPROXY_28_IMAGE", container.Env)).To(Equal(&corev1.EnvVar{
					Name:  "HAPROXY_28_IMAGE",
					Value: haproxy28Image,
				}))
				g.Expect(podspec.FindEnvVar("HAPROXY_32_IMAGE", container.Env)).To(Equal(&corev1.EnvVar{
					Name:  "HAPROXY_32_IMAGE",
					Value: haproxy32Image,
				}))
				g.Expect(podspec.FindEnvVar("DEFAULT_HAPROXY_VERSION", container.Env)).To(Equal(&corev1.EnvVar{
					Name:  "DEFAULT_HAPROXY_VERSION",
					Value: "3.2",
				}))
				g.Expect(userProvider.requestedImages).To(Equal(sets.New(
					"haproxy-router",
					"cluster-ingress-operator",
					"haproxy-router-haproxy28",
					"haproxy-router-haproxy32",
				)))
			} else {
				g.Expect(container.Command).To(Equal(expectedCommand))
				g.Expect(podspec.FindEnvVar("HAPROXY_28_IMAGE", container.Env)).To(BeNil())
				g.Expect(podspec.FindEnvVar("HAPROXY_32_IMAGE", container.Env)).To(BeNil())
				g.Expect(podspec.FindEnvVar("DEFAULT_HAPROXY_VERSION", container.Env)).To(BeNil())
				g.Expect(userProvider.requestedImages).To(Equal(sets.New(
					"haproxy-router",
					"cluster-ingress-operator",
				)))
			}
			g.Expect(userProvider.missingImages).To(BeEmpty())
		})
	}
}

func TestSupportsHAProxyVersionSelection(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		version  string
		expected bool
	}{
		{
			name:    "When the release is 4.22, it should report version selection as unsupported",
			version: "4.22.0",
		},
		{
			name:     "When the release is a 4.23 prerelease, it should report version selection as supported",
			version:  "4.23.0-0.nightly-multi-2026-10-07-120000",
			expected: true,
		},
		{
			name:     "When the release is 5.0, it should report version selection as supported",
			version:  "5.0.0",
			expected: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			g.Expect(supportsHAProxyVersionSelection(semver.MustParse(tc.version))).To(Equal(tc.expected))
		})
	}
}

type trackingImageProvider struct {
	version          string
	images           map[string]string
	requestedImages  sets.Set[string]
	missingImages    sets.Set[string]
	overriddenImages sets.Set[string]
}

func (p *trackingImageProvider) GetImage(key string) string {
	p.requestedImages.Insert(key)
	image, ok := p.images[key]
	if !ok || image == "" {
		p.missingImages.Insert(key)
	}
	return image
}

func (p *trackingImageProvider) ImageExist(key string) (string, bool) {
	image, ok := p.images[key]
	return image, ok
}

func (p *trackingImageProvider) Version() string {
	return p.version
}

func (p *trackingImageProvider) ComponentVersions() (map[string]string, error) {
	return nil, nil
}

func (p *trackingImageProvider) ComponentImages() map[string]string {
	return p.images
}

func (p *trackingImageProvider) ImageOverridden(key string) bool {
	return p.overriddenImages.Has(key)
}
