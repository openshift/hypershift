package imageregistry

import (
	"context"
	"errors"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	controlplaneoperatoroverrides "github.com/openshift/hypershift/hypershift-operator/controlplaneoperator-overrides"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/dockerv1client"
	"github.com/openshift/hypershift/support/util/fakeimagemetadataprovider"

	"github.com/openshift/api/image/docker10"
	imagev1 "github.com/openshift/api/image/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestHCControlPlaneReleaseImage(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		override *hyperv1.Release
		expected string
	}{
		{name: "When no control plane release is set, it should use the cluster release", expected: "quay.io/openshift-release-dev/ocp-release:4.21.10-x86_64"},
		{name: "When a control plane release is set, it should use that release", override: &hyperv1.Release{Image: "quay.io/openshift-release-dev/ocp-release:4.21.11-x86_64"}, expected: "quay.io/openshift-release-dev/ocp-release:4.21.11-x86_64"},
		{name: "When the control plane release is empty, it should preserve the override", override: &hyperv1.Release{}, expected: ""},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			cluster := &hyperv1.HostedCluster{Spec: hyperv1.HostedClusterSpec{
				Release: hyperv1.Release{Image: "quay.io/openshift-release-dev/ocp-release:4.21.10-x86_64"}, ControlPlaneRelease: testCase.override,
			}}
			NewWithT(t).Expect(HCControlPlaneReleaseImage(cluster)).To(Equal(testCase.expected))
		})
	}
}

func TestHCPControlPlaneReleaseImage(t *testing.T) {
	override := "quay.io/openshift-release-dev/ocp-release:4.21.11-x86_64"
	empty := ""
	for _, testCase := range []struct {
		name     string
		override *string
		expected string
	}{
		{name: "When no control plane release is set, it should use the release image", expected: "quay.io/openshift-release-dev/ocp-release:4.21.10-x86_64"},
		{name: "When a control plane release is set, it should use that release", override: &override, expected: override},
		{name: "When the control plane release is empty, it should preserve the override", override: &empty, expected: ""},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			controlPlane := &hyperv1.HostedControlPlane{Spec: hyperv1.HostedControlPlaneSpec{
				ReleaseImage: "quay.io/openshift-release-dev/ocp-release:4.21.10-x86_64", ControlPlaneReleaseImage: testCase.override,
			}}
			NewWithT(t).Expect(HCPControlPlaneReleaseImage(controlPlane)).To(Equal(testCase.expected))
		})
	}
}

type releaseProviderStub struct {
	releaseinfo.Provider
	version        string
	image          string
	err            error
	requestedImage string
}

func (provider *releaseProviderStub) Lookup(_ context.Context, image string, _ []byte) (*releaseinfo.ReleaseImage, error) {
	provider.requestedImage = image
	if provider.err != nil {
		return nil, provider.err
	}
	stream := &imagev1.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: provider.version}}
	if provider.image != "" {
		stream.Spec.Tags = []imagev1.TagReference{{Name: "hypershift", From: &corev1.ObjectReference{Name: provider.image}}}
	}
	return &releaseinfo.ReleaseImage{ImageStream: stream}, nil
}

func TestGetControlPlaneOperatorImage(t *testing.T) {
	t.Setenv(controlplaneoperatoroverrides.CPOOverridesEnvVar, "0")
	for _, testCase := range []struct {
		name            string
		annotation      string
		provider        releaseProviderStub
		expected        string
		expectedError   string
		enableOverrides bool
	}{
		{name: "When an annotation is set, it should bypass release lookup", annotation: "quay.io/hypershift/hypershift:latest", expected: "quay.io/hypershift/hypershift:latest"},
		{name: "When the payload includes hypershift, it should use the payload image", provider: releaseProviderStub{version: "4.21.0", image: "quay.io/openshift-release-dev/ocp-v4.0-art-dev:4.21.11-hypershift"}, expected: "quay.io/openshift-release-dev/ocp-v4.0-art-dev:4.21.11-hypershift"},
		{name: "When the payload lacks hypershift, it should use the operator image", provider: releaseProviderStub{version: "4.21.0"}, expected: "quay.io/hypershift/hypershift-operator:latest"},
		{name: "When enabled overrides do not match the platform, it should use the payload image", enableOverrides: true, provider: releaseProviderStub{version: "4.21.0", image: "quay.io/openshift-release-dev/ocp-v4.0-art-dev:4.21.11-hypershift"}, expected: "quay.io/openshift-release-dev/ocp-v4.0-art-dev:4.21.11-hypershift"},
		{name: "When the release lookup fails, it should return an error", provider: releaseProviderStub{err: fmt.Errorf("lookup failed")}, expectedError: "lookup failed"},
		{name: "When the release version is invalid, it should return an error", provider: releaseProviderStub{version: "invalid"}, expectedError: "No Major.Minor.Patch elements found"},
		{name: "When the release is unsupported, it should return an error", provider: releaseProviderStub{version: "4.8.0"}, expectedError: "unsupported release image with version 4.8.0"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			g := NewWithT(t)
			cluster := &hyperv1.HostedCluster{Spec: hyperv1.HostedClusterSpec{ControlPlaneRelease: &hyperv1.Release{Image: "quay.io/openshift-release-dev/ocp-release:4.21.11-x86_64"}}}
			if testCase.enableOverrides {
				t.Setenv(controlplaneoperatoroverrides.CPOOverridesEnvVar, "1")
				cluster.Spec.Platform.Type = hyperv1.NonePlatform
			}
			if testCase.annotation != "" {
				cluster.Annotations = map[string]string{hyperv1.ControlPlaneOperatorImageAnnotation: testCase.annotation}
			}
			image, err := GetControlPlaneOperatorImage(t.Context(), cluster, &testCase.provider, "quay.io/hypershift/hypershift-operator:latest", nil)
			g.Expect(image).To(Equal(testCase.expected))
			if testCase.expectedError != "" {
				g.Expect(err).To(MatchError(testCase.expectedError))
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}
			if testCase.provider.err != nil {
				g.Expect(errors.Is(err, testCase.provider.err)).To(BeTrue())
			}
			if testCase.annotation == "" {
				g.Expect(testCase.provider.requestedImage).To(Equal("quay.io/openshift-release-dev/ocp-release:4.21.11-x86_64"))
			} else {
				g.Expect(testCase.provider.requestedImage).To(BeEmpty())
			}
		})
	}
}

func TestGetControlPlaneOperatorImageLabels(t *testing.T) {
	for _, testCase := range []struct {
		name          string
		annotations   map[string]string
		provider      fakeimagemetadataprovider.FakeRegistryClientImageMetadataProvider
		expected      map[string]string
		expectedError string
	}{
		{name: "When labels are annotated, it should bypass metadata lookup", annotations: map[string]string{hyperv1.ControlPlaneOperatorImageLabelsAnnotation: "a=b,c=d"}, provider: fakeimagemetadataprovider.FakeRegistryClientImageMetadataProvider{Err: fmt.Errorf("not called")}, expected: map[string]string{"a": "b", "c": "d"}},
		{name: "When an annotated label is malformed, it should return an error", annotations: map[string]string{hyperv1.ControlPlaneOperatorImageLabelsAnnotation: "invalid"}, expectedError: "hosted cluster clusters/example annotation 0 malformed: label invalid not in key=value form"},
		{name: "When labels are not annotated, it should use image metadata", provider: fakeimagemetadataprovider.FakeRegistryClientImageMetadataProvider{Result: &dockerv1client.DockerImageConfig{Config: &docker10.DockerConfig{Labels: map[string]string{"a": "b"}}}}, expected: map[string]string{"a": "b"}},
		{name: "When metadata lookup fails, it should return an error", provider: fakeimagemetadataprovider.FakeRegistryClientImageMetadataProvider{Err: fmt.Errorf("lookup failed")}, expectedError: "failed to look up image metadata for quay.io/hypershift/hypershift-operator:latest: lookup failed"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			g := NewWithT(t)
			cluster := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: "example", Annotations: testCase.annotations}}
			labels, err := GetControlPlaneOperatorImageLabels(t.Context(), cluster, "quay.io/hypershift/hypershift-operator:latest", nil, &testCase.provider)
			g.Expect(labels).To(Equal(testCase.expected))
			if testCase.expectedError != "" {
				g.Expect(err).To(MatchError(testCase.expectedError))
				if testCase.provider.Err != nil {
					g.Expect(errors.Is(err, testCase.provider.Err)).To(BeTrue())
				}
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}
		})
	}
}
