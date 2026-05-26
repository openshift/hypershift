package catalogs

import (
	"errors"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestCheckCatalogImageOverrides(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name             string
		images           map[string]string
		expectedOverride bool
		expectedError    error
	}{
		{
			name: "When all images are empty, it should return false with no error",
			images: map[string]string{
				"redhat-operators":    "",
				"redhat-marketplace":  "",
				"community-operators": "",
				"certified-operators": "",
			},
			expectedOverride: false,
			expectedError:    nil,
		},
		{
			name: "When all images are provided with sha256, it should return true with no error",
			images: map[string]string{
				"redhat-operators":    "registry.io/repo@sha256:abc123",
				"redhat-marketplace":  "registry.io/repo@sha256:def456",
				"community-operators": "registry.io/repo@sha256:ghi789",
				"certified-operators": "registry.io/repo@sha256:jkl012",
			},
			expectedOverride: true,
			expectedError:    nil,
		},
		{
			name: "When image is provided without sha256, it should return error",
			images: map[string]string{
				"redhat-operators":    "registry.io/repo:latest",
				"redhat-marketplace":  "",
				"community-operators": "",
				"certified-operators": "",
			},
			expectedOverride: false,
			expectedError:    errors.New("images for OLM catalogs should be referenced only by digest"),
		},
		{
			name: "When some images are missing, it should return error",
			images: map[string]string{
				"redhat-operators":    "registry.io/repo@sha256:abc123",
				"redhat-marketplace":  "registry.io/repo@sha256:def456",
				"community-operators": "",
				"certified-operators": "",
			},
			expectedOverride: false,
			expectedError:    errors.New("if OLM catalog images are overridden, all the values for the 4 default catalogs should be provided"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			override, err := checkCatalogImageOverides(tc.images)

			g.Expect(override).To(Equal(tc.expectedOverride))
			if tc.expectedError != nil {
				g.Expect(err).To(MatchError(tc.expectedError.Error()))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
}

func TestGetCatalogImagesOverrides(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name                  string
		annotations           map[string]string
		capabilityImageStream bool
		validate              func(g Gomega, result map[string]string, err error)
	}{
		{
			name: "When all catalog annotations are set with sha256, it should return overrides",
			annotations: map[string]string{
				hyperv1.RedHatOperatorsCatalogImageAnnotation:    "registry.io/redhat@sha256:abc",
				hyperv1.RedHatMarketplaceCatalogImageAnnotation:  "registry.io/marketplace@sha256:def",
				hyperv1.CommunityOperatorsCatalogImageAnnotation: "registry.io/community@sha256:ghi",
				hyperv1.CertifiedOperatorsCatalogImageAnnotation: "registry.io/certified@sha256:jkl",
			},
			capabilityImageStream: false,
			validate: func(g Gomega, result map[string]string, err error) {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(result).To(HaveKeyWithValue("redhat-operators", "registry.io/redhat@sha256:abc"))
				g.Expect(result).To(HaveKeyWithValue("redhat-marketplace", "registry.io/marketplace@sha256:def"))
				g.Expect(result).To(HaveKeyWithValue("community-operators", "registry.io/community@sha256:ghi"))
				g.Expect(result).To(HaveKeyWithValue("certified-operators", "registry.io/certified@sha256:jkl"))
			},
		},
		{
			name: "When annotations are incomplete, it should return error",
			annotations: map[string]string{
				hyperv1.RedHatOperatorsCatalogImageAnnotation: "registry.io/redhat@sha256:abc",
			},
			capabilityImageStream: false,
			validate: func(g Gomega, result map[string]string, err error) {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("if OLM catalog images are overridden"))
			},
		},
		{
			name: "When annotations use tags instead of digests, it should return error",
			annotations: map[string]string{
				hyperv1.RedHatOperatorsCatalogImageAnnotation: "registry.io/redhat:latest",
			},
			capabilityImageStream: false,
			validate: func(g Gomega, result map[string]string, err error) {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("should be referenced only by digest"))
			},
		},
		{
			name:                  "When no annotations are set and capabilityImageStream is true, it should return nil",
			annotations:           map[string]string{},
			capabilityImageStream: true,
			validate: func(g Gomega, result map[string]string, err error) {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(result).To(BeNil())
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-hcp",
					Namespace:   "test-namespace",
					Annotations: tc.annotations,
				},
			}

			cpContext := component.WorkloadContext{
				HCP: hcp,
			}

			result, err := getCatalogImagesOverrides(cpContext, tc.capabilityImageStream)
			tc.validate(g, result, err)
		})
	}
}
