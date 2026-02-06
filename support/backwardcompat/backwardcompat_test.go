package backwardcompat

import (
	"context"
	"fmt"
	"testing"

	"github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/util"

	v1 "github.com/openshift/api/config/v1"
	imagev1 "github.com/openshift/api/image/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/blang/semver"
)

func TestGetBackwardCompatibleConfigHash(t *testing.T) {
	tests := []struct {
		name                   string
		input                  v1beta1.ClusterConfiguration
		expectedHashedJSONE    string
		requiresBackwardCompat bool
	}{
		{
			name: "test config without an image",
			input: v1beta1.ClusterConfiguration{
				Proxy: &v1.ProxySpec{
					HTTPProxy: "http://proxy.example.com",
				},
			},
			expectedHashedJSONE: `{"proxy":{"httpProxy":"http://proxy.example.com"}}`,
		},
		{
			name: "test config with an image and no imageStreamImportMode",
			input: v1beta1.ClusterConfiguration{
				Proxy: &v1.ProxySpec{
					HTTPProxy: "http://proxy.example.com",
				},
				Image: &v1.ImageSpec{
					RegistrySources: v1.RegistrySources{
						InsecureRegistries: []string{"registry.example.com"},
					},
				},
			},
			expectedHashedJSONE:    `{"image":{"additionalTrustedCA":{"name":""},"registrySources":{"insecureRegistries":["registry.example.com"]}},"proxy":{"httpProxy":"http://proxy.example.com","trustedCA":{"name":""}}}`,
			requiresBackwardCompat: true,
		},
		{
			name: "test config with an image and imageStreamImportMode",
			input: v1beta1.ClusterConfiguration{
				Proxy: &v1.ProxySpec{
					HTTPProxy: "http://proxy.example.com",
				},
				Image: &v1.ImageSpec{
					RegistrySources: v1.RegistrySources{
						InsecureRegistries: []string{"registry.example.com"},
					},
					ImageStreamImportMode: "",
				},
			},
			expectedHashedJSONE:    `{"image":{"additionalTrustedCA":{"name":""},"registrySources":{"insecureRegistries":["registry.example.com"]}},"proxy":{"httpProxy":"http://proxy.example.com","trustedCA":{"name":""}}}`,
			requiresBackwardCompat: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withoutBackwardCompat, err := util.HashStructWithJSONMapper(tt.input, nil)
			if err != nil {
				t.Errorf("error hashing without backward compatibility: %v", err)
				return
			}
			withBackwardCompat, err := GetBackwardCompatibleConfigHash(&tt.input)
			if err != nil {
				t.Errorf("GetBackwardCompatibleConfigHash() error = %v", err)
				return
			}
			if !tt.requiresBackwardCompat && withoutBackwardCompat != withBackwardCompat {
				t.Errorf("GetBackwardCompatibleConfigHash() = %v, want %v", withBackwardCompat, withoutBackwardCompat)
			} else if tt.requiresBackwardCompat {
				if withoutBackwardCompat == withBackwardCompat {
					t.Errorf("GetBackwardCompatibleConfigHash() = %v, want %v", withBackwardCompat, withoutBackwardCompat)
				} else {
					want, err := util.HashBytes([]byte(tt.expectedHashedJSONE))
					if err != nil {
						t.Errorf("error hashing wanted config: %v", err)
						return
					}
					if withBackwardCompat != want {
						t.Errorf("GetBackwardCompatibleConfigHash() = %v, want %v", withBackwardCompat, want)
					}
				}
			}

		})
	}
}

// mockReleaseProvider is a mock implementation of releaseinfo.Provider for testing.
type mockReleaseProvider struct {
	components map[string]string
	err        error
}

func (m *mockReleaseProvider) Lookup(_ context.Context, _ string, _ []byte) (*releaseinfo.ReleaseImage, error) {
	if m.err != nil {
		return nil, m.err
	}
	tags := make([]imagev1.TagReference, 0, len(m.components))
	for name, image := range m.components {
		tags = append(tags, imagev1.TagReference{
			Name: name,
			From: &corev1.ObjectReference{Name: image},
		})
	}
	return &releaseinfo.ReleaseImage{
		ImageStream: &imagev1.ImageStream{
			ObjectMeta: metav1.ObjectMeta{Name: "4.20.12"},
			Spec: imagev1.ImageStreamSpec{
				Tags: tags,
			},
		},
	}, nil
}

func TestGetBackwardCompatibleCAPIImage(t *testing.T) {
	tests := []struct {
		name           string
		releaseVersion semver.Version
		component      string
		provider       *mockReleaseProvider
		expectedImage  string
		expectError    bool
	}{
		{
			name:           "When version is below 4.21.0 it should return empty string",
			releaseVersion: semver.MustParse("4.20.0"),
			component:      "cluster-capi-controllers",
			provider: &mockReleaseProvider{
				components: map[string]string{
					"cluster-capi-controllers": "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:abc123",
				},
			},
			expectedImage: "",
			expectError:   false,
		},
		{
			name:           "When version is exactly 4.21.0-0 it should return the pinned image",
			releaseVersion: semver.MustParse("4.21.0-0"),
			component:      "cluster-capi-controllers",
			provider: &mockReleaseProvider{
				components: map[string]string{
					"cluster-capi-controllers": "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:abc123",
				},
			},
			expectedImage: "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:abc123",
			expectError:   false,
		},
		{
			name:           "When version is above 4.21.0 it should return the pinned image",
			releaseVersion: semver.MustParse("4.22.0"),
			component:      "aws-cluster-api-controllers",
			provider: &mockReleaseProvider{
				components: map[string]string{
					"aws-cluster-api-controllers": "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:def456",
				},
			},
			expectedImage: "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:def456",
			expectError:   false,
		},
		{
			name:           "When provider returns error it should propagate the error",
			releaseVersion: semver.MustParse("4.21.0"),
			component:      "cluster-capi-controllers",
			provider: &mockReleaseProvider{
				err: fmt.Errorf("lookup failed"),
			},
			expectedImage: "",
			expectError:   true,
		},
		{
			name:           "When component does not exist it should return error",
			releaseVersion: semver.MustParse("4.21.0"),
			component:      "nonexistent-component",
			provider: &mockReleaseProvider{
				components: map[string]string{
					"cluster-capi-controllers": "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:abc123",
				},
			},
			expectedImage: "",
			expectError:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			pullSecret := []byte(`{"auths":{}}`)

			image, err := GetBackwardCompatibleCAPIImage(ctx, pullSecret, tt.provider, tt.releaseVersion, tt.component)
			if tt.expectError && err == nil {
				t.Errorf("GetBackwardCompatibleCAPIImage() expected error but got none")
				return
			}
			if !tt.expectError && err != nil {
				t.Errorf("GetBackwardCompatibleCAPIImage() unexpected error: %v", err)
				return
			}
			if image != tt.expectedImage {
				t.Errorf("GetBackwardCompatibleCAPIImage() = %q, want %q", image, tt.expectedImage)
			}
		})
	}
}
