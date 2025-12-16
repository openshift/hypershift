package util

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/reference"
)

func TestGetRegistryOverrides(t *testing.T) {
	ctx := context.TODO()
	testsCases := []struct {
		name           string
		ref            reference.DockerImageReference
		source         string
		mirror         string
		expectedImgRef *reference.DockerImageReference
		expectAnErr    bool
		overrideFound  bool
	}{
		{
			name: "if failed to parse source image",
			ref: reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "ocp-release",
				Namespace: "openshift-release-dev",
				Tag:       "4.15.0-rc.0-multi",
			},
			source:         "",
			mirror:         "",
			expectedImgRef: nil,
			expectAnErr:    true,
			overrideFound:  false,
		},
		{
			name: "if registry override coincidence not found",
			ref: reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "ocp",
				Namespace: "openshift-release-dev",
				Tag:       "4.15.0-rc.0-multi",
			},
			source: "quay.io/openshift-release-dev/ocp-release:4.15.0-rc.0-multi",
			mirror: "myregistry.io/openshift-release-dev/ocp-release:4.15.0-rc.0-multi",
			expectedImgRef: &reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "ocp",
				Namespace: "openshift-release-dev",
				Tag:       "4.15.0-rc.0-multi",
			},
			expectAnErr:   false,
			overrideFound: false,
		},
		{
			name: "if registry override coincidence is found",
			ref: reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "ocp-release",
				Namespace: "openshift-release-dev",
				Tag:       "4.15.0-rc.0-multi",
			},
			source: "quay.io/openshift-release-dev/ocp-release:4.15.0-rc.0-multi",
			mirror: "myregistry.io/openshift-release-dev/ocp-release:4.15.0-rc.0-multi",
			expectedImgRef: &reference.DockerImageReference{
				Registry:  "myregistry.io",
				Name:      "ocp-release",
				Namespace: "openshift-release-dev",
				Tag:       "4.15.0-rc.0-multi",
			},
			expectAnErr:   false,
			overrideFound: true,
		},
		{
			name: "if registry override partial coincidence is found",
			ref: reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "busybox",
				Namespace: "mce",
				Tag:       "multiarch",
			},
			source: "quay.io/mce",
			mirror: "quay.io/openshifttest",
			expectedImgRef: &reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "busybox",
				Namespace: "openshifttest",
				Tag:       "multiarch",
			},
			expectAnErr:   false,
			overrideFound: true,
		},
	}
	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			imgRef, overrideFound, err := GetRegistryOverrides(ctx, tc.ref, tc.source, tc.mirror)
			g.Expect(imgRef).To(Equal(tc.expectedImgRef))
			g.Expect(err != nil).To(Equal(tc.expectAnErr))
			g.Expect(overrideFound).To(Equal(tc.overrideFound))
		})
	}
}

func TestTryOnlyNamespaceOverride(t *testing.T) {
	testsCases := []struct {
		name           string
		ref            reference.DockerImageReference
		sourceRef      reference.DockerImageReference
		mirrorRef      reference.DockerImageReference
		expectedImgRef *reference.DockerImageReference
		overrideFound  bool
		expectAnErr    bool
	}{
		{
			name: "if namespace override is found",
			ref: reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "ocp-release",
				Namespace: "openshift-release-dev",
				Tag:       "4.15.0-rc.0-multi",
			},
			sourceRef: reference.DockerImageReference{
				Registry: "quay.io",
				Name:     "openshift-release-dev",
			},
			mirrorRef: reference.DockerImageReference{
				Registry: "myregistry.io",
				Name:     "openshift-release-dev",
			},
			expectedImgRef: &reference.DockerImageReference{
				Registry:  "myregistry.io",
				Name:      "ocp-release",
				Namespace: "openshift-release-dev",
				Tag:       "4.15.0-rc.0-multi",
			},
			overrideFound: true,
			expectAnErr:   false,
		},
		{
			name: "if namespace override is not found - namespace not empty",
			ref: reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "ocp-release",
				Namespace: "openshift-release-dev",
				Tag:       "4.15.0-rc.0-multi",
			},
			sourceRef: reference.DockerImageReference{
				Namespace: "test",
				Name:      "openshift-release-dev",
			},
			mirrorRef: reference.DockerImageReference{
				Registry: "myregistry.io",
			},
			expectedImgRef: nil,
			overrideFound:  false,
			expectAnErr:    false,
		},
		{
			name: "if namespace override is not found - name mismatch",
			ref: reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "ocp-release",
				Namespace: "openshift-release-dev",
				Tag:       "4.15.0-rc.0-multi",
			},
			sourceRef: reference.DockerImageReference{
				Name: "different-namespace",
			},
			mirrorRef: reference.DockerImageReference{
				Registry: "myregistry.io",
			},
			expectedImgRef: nil,
			overrideFound:  false,
			expectAnErr:    false,
		},
	}

	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			imgRef, overrideFound, err := tryOnlyNamespaceOverride(tc.ref, tc.sourceRef, tc.mirrorRef)
			g.Expect(imgRef).To(Equal(tc.expectedImgRef))
			g.Expect(overrideFound).To(Equal(tc.overrideFound))
			g.Expect(err != nil).To(Equal(tc.expectAnErr))
		})
	}
}

func TestTryExactCoincidenceOverride(t *testing.T) {
	testsCases := []struct {
		name           string
		ref            reference.DockerImageReference
		sourceRef      reference.DockerImageReference
		mirrorRef      reference.DockerImageReference
		expectedImgRef *reference.DockerImageReference
		overrideFound  bool
		expectAnErr    bool
	}{
		{
			name: "if exact coincidence override is found",
			ref: reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "ocp-release",
				Namespace: "openshift-release-dev",
				Tag:       "4.15.0-rc.0-multi",
			},
			sourceRef: reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "ocp-release",
				Namespace: "openshift-release-dev",
			},
			mirrorRef: reference.DockerImageReference{
				Registry:  "myregistry.io",
				Namespace: "openshift-release-dev",
				Name:      "ocp-release",
			},
			expectedImgRef: &reference.DockerImageReference{
				Registry:  "myregistry.io",
				Name:      "ocp-release",
				Namespace: "openshift-release-dev",
				Tag:       "4.15.0-rc.0-multi",
			},
			overrideFound: true,
			expectAnErr:   false,
		},
		{
			name: "if exact coincidence override is not found",
			ref: reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "ocp-release",
				Namespace: "openshift-release-dev",
				Tag:       "4.15.0-rc.0-multi",
			},
			sourceRef: reference.DockerImageReference{
				Registry:  "quay.io",
				Name:      "different-name",
				Namespace: "openshift-release-dev",
			},
			mirrorRef: reference.DockerImageReference{
				Registry: "myregistry.io",
			},
			expectedImgRef: nil,
			overrideFound:  false,
			expectAnErr:    false,
		},
	}

	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			imgRef, overrideFound, err := tryExactCoincidenceOverride(tc.ref, tc.sourceRef, tc.mirrorRef)
			if tc.overrideFound {
				g.Expect(imgRef).To(Equal(tc.expectedImgRef))
			} else {
				g.Expect(imgRef).To(BeNil())
			}
			g.Expect(overrideFound).To(Equal(tc.overrideFound))
			g.Expect(err != nil).To(Equal(tc.expectAnErr))
		})
	}
}

func TestTryOnlyRootRegistryOverride(t *testing.T) {
	testsCases := []struct {
		name           string
		ref            reference.DockerImageReference
		sourceRef      reference.DockerImageReference
		mirrorRef      reference.DockerImageReference
		expectedImgRef *reference.DockerImageReference
		overrideFound  bool
		expectAnErr    bool
	}{
		{
			name: "if root registry override is found",
			ref: reference.DockerImageReference{
				Registry:  "registry.build02.ci.openshift.org",
				Name:      "release",
				Namespace: "ocp",
				Tag:       "4.15.0-rc.0-multi",
			},
			sourceRef: reference.DockerImageReference{
				Name: "registry.build02.ci.openshift.org",
			},
			mirrorRef: reference.DockerImageReference{
				Name: "virthost.ostest.test.metalkube.org:5000",
			},
			expectedImgRef: &reference.DockerImageReference{
				Registry:  "virthost.ostest.test.metalkube.org:5000",
				Name:      "release",
				Namespace: "ocp",
				Tag:       "4.15.0-rc.0-multi",
			},
			overrideFound: true,
			expectAnErr:   false,
		},
		{
			name: "if root registry override is not found - namespace not empty",
			ref: reference.DockerImageReference{
				Registry:  "registry.build02.ci.openshift.org",
				Name:      "release",
				Namespace: "ocp",
				Tag:       "4.15.0-rc.0-multi",
			},
			sourceRef: reference.DockerImageReference{
				Namespace: "test",
				Name:      "registry.build02.ci.openshift.org",
			},
			mirrorRef: reference.DockerImageReference{
				Registry: "virthost.ostest.test.metalkube.org:5000",
			},
			expectedImgRef: nil,
			overrideFound:  false,
			expectAnErr:    false,
		},
		{
			name: "if root registry override is not found - registry not empty",
			ref: reference.DockerImageReference{
				Registry:  "registry.build02.ci.openshift.org",
				Name:      "release",
				Namespace: "ocp",
				Tag:       "4.15.0-rc.0-multi",
			},
			sourceRef: reference.DockerImageReference{
				Registry: "test",
				Name:     "registry.build02.ci.openshift.org",
			},
			mirrorRef: reference.DockerImageReference{
				Registry: "virthost.ostest.test.metalkube.org:5000",
			},
			expectedImgRef: nil,
			overrideFound:  false,
			expectAnErr:    false,
		},
		{
			name: "if root registry override is not found - name mismatch",
			ref: reference.DockerImageReference{
				Registry:  "registry.build02.ci.openshift.org",
				Name:      "release",
				Namespace: "ocp",
				Tag:       "4.15.0-rc.0-multi",
			},
			sourceRef: reference.DockerImageReference{
				Name: "different-registry",
			},
			mirrorRef: reference.DockerImageReference{
				Registry: "virthost.ostest.test.metalkube.org:5000",
			},
			expectedImgRef: nil,
			overrideFound:  false,
			expectAnErr:    false,
		},
	}

	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			imgRef, overrideFound, err := tryOnlyRootRegistryOverride(tc.ref, tc.sourceRef, tc.mirrorRef)
			if tc.overrideFound {
				g.Expect(imgRef).To(Equal(tc.expectedImgRef))
			} else {
				g.Expect(imgRef).To(BeNil())
			}
			g.Expect(overrideFound).To(Equal(tc.overrideFound))
			g.Expect(err != nil).To(Equal(tc.expectAnErr))
		})
	}
}
