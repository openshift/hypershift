package releaseinfo

import (
	"context"
	"fmt"
	"sync"
	"testing"

	. "github.com/onsi/gomega"

	imageapi "github.com/openshift/api/image/v1"

	corev1 "k8s.io/api/core/v1"

	"github.com/coreos/stream-metadata-go/stream"
)

func TestRegistryMirrorProviderDecoratorLookup(t *testing.T) {
	releaseImageWithTags := &ReleaseImage{
		ImageStream: &imageapi.ImageStream{
			Spec: imageapi.ImageStreamSpec{
				Tags: []imageapi.TagReference{
					{
						Name: "hypershift",
						From: &corev1.ObjectReference{
							Name: "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:def456",
						},
					},
				},
			},
		},
		StreamMetadata: &stream.Stream{},
	}

	releaseImageNoTags := &ReleaseImage{
		ImageStream: &imageapi.ImageStream{
			Spec: imageapi.ImageStreamSpec{},
		},
		StreamMetadata: &stream.Stream{},
	}

	tests := []struct {
		name              string
		image             string
		registryOverrides map[string]string
		delegateImage     *ReleaseImage
		delegateErr       error
		wantDelegateImage string
		wantTagName       string
		wantErr           bool
		wantErrContains   string
	}{
		{
			name:  "When registry overrides match the release image, it should pass the overridden image to the delegate",
			image: "quay.io/openshift-release-dev/ocp-release-nightly@sha256:abc123",
			registryOverrides: map[string]string{
				"quay.io/openshift-release-dev": "myregistry.example.com/openshift-release-dev",
			},
			delegateImage:     releaseImageWithTags,
			wantDelegateImage: "myregistry.example.com/openshift-release-dev/ocp-release-nightly@sha256:abc123",
			wantTagName:       "myregistry.example.com/openshift-release-dev/ocp-v4.0-art-dev@sha256:def456",
		},
		{
			name:  "When no registry override matches the image, it should pass the original image to the delegate",
			image: "quay.io/openshift-release-dev/ocp-release@sha256:abc123",
			registryOverrides: map[string]string{
				"registry.example.com/no-match": "mirror.example.com/no-match",
			},
			delegateImage:     releaseImageWithTags,
			wantDelegateImage: "quay.io/openshift-release-dev/ocp-release@sha256:abc123",
		},
		{
			name:              "When registry overrides map is empty, it should pass the original image to the delegate",
			image:             "quay.io/openshift-release-dev/ocp-release@sha256:abc123",
			registryOverrides: map[string]string{},
			delegateImage:     releaseImageNoTags,
			wantDelegateImage: "quay.io/openshift-release-dev/ocp-release@sha256:abc123",
		},
		{
			name:  "When the delegate returns an error, it should propagate the error",
			image: "quay.io/org/repo@sha256:abc",
			registryOverrides: map[string]string{
				"quay.io": "mirror.example.com",
			},
			delegateErr:     fmt.Errorf("connection refused"),
			wantErr:         true,
			wantErrContains: "connection refused",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			var lookedUpImage string
			provider := &RegistryMirrorProviderDecorator{
				Delegate: &fakeProvider{
					lookupFn: func(_ context.Context, image string, _ []byte) (*ReleaseImage, error) {
						lookedUpImage = image
						if tt.delegateErr != nil {
							return nil, tt.delegateErr
						}
						return tt.delegateImage, nil
					},
				},
				RegistryOverrides: tt.registryOverrides,
				lock:              sync.Mutex{},
			}

			result, err := provider.Lookup(t.Context(), tt.image, []byte(`{"auths":{}}`))

			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
				if tt.wantErrContains != "" {
					g.Expect(err.Error()).To(ContainSubstring(tt.wantErrContains))
				}
				return
			}

			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(lookedUpImage).To(Equal(tt.wantDelegateImage))

			if tt.wantTagName != "" {
				g.Expect(result.ImageStream.Spec.Tags[0].From.Name).To(Equal(tt.wantTagName))
			}
		})
	}
}

// fakeProvider is a test helper that delegates Lookup to a caller-supplied function.
type fakeProvider struct {
	lookupFn func(ctx context.Context, image string, pullSecret []byte) (*ReleaseImage, error)
}

func (f *fakeProvider) Lookup(ctx context.Context, image string, pullSecret []byte) (*ReleaseImage, error) {
	return f.lookupFn(ctx, image, pullSecret)
}
