package catalogs

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"

	configv1 "github.com/openshift/api/config/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestCatalogsPredicate(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name                     string
		olmCatalogPlacement      hyperv1.OLMCatalogPlacement
		disableAllDefaultSources bool
		expectedResult           bool
	}{
		{
			name:                     "When OLMCatalogPlacement is Management and default sources are not disabled, it should return true",
			olmCatalogPlacement:      hyperv1.ManagementOLMCatalogPlacement,
			disableAllDefaultSources: false,
			expectedResult:           true,
		},
		{
			name:                     "When OLMCatalogPlacement is Guest, it should return false",
			olmCatalogPlacement:      hyperv1.GuestOLMCatalogPlacement,
			disableAllDefaultSources: false,
			expectedResult:           false,
		},
		{
			name:                     "When default sources are disabled, it should return false regardless of placement",
			olmCatalogPlacement:      hyperv1.ManagementOLMCatalogPlacement,
			disableAllDefaultSources: true,
			expectedResult:           false,
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
					OLMCatalogPlacement: tc.olmCatalogPlacement,
				},
			}

			if tc.disableAllDefaultSources {
				hcp.Spec.Configuration = &hyperv1.ClusterConfiguration{
					OperatorHub: &configv1.OperatorHubSpec{
						DisableAllDefaultSources: true,
					},
				}
			}

			cpContext := component.WorkloadContext{
				HCP: hcp,
			}

			result, err := catalogsPredicate(cpContext)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(result).To(Equal(tc.expectedResult))
		})
	}
}

func TestImageStreamPredicate(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name                  string
		capabilityImageStream bool
		annotationPresent     bool
		expectedResult        bool
	}{
		{
			name:                  "When capabilityImageStream is false, it should return false",
			capabilityImageStream: false,
			annotationPresent:     false,
			expectedResult:        false,
		},
		{
			name:                  "When annotation is present, it should return false",
			capabilityImageStream: true,
			annotationPresent:     true,
			expectedResult:        false,
		},
		{
			name:                  "When capabilityImageStream is true and no annotation, it should return true",
			capabilityImageStream: true,
			annotationPresent:     false,
			expectedResult:        true,
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
			}

			if tc.annotationPresent {
				hcp.Annotations = map[string]string{
					hyperv1.RedHatOperatorsCatalogImageAnnotation: "some-image",
				}
			}

			catalogOpts := &catalogOptions{
				capabilityImageStream: tc.capabilityImageStream,
			}

			cpContext := component.WorkloadContext{
				HCP: hcp,
			}

			result := catalogOpts.imageStreamPredicate(cpContext)
			g.Expect(result).To(Equal(tc.expectedResult))
		})
	}
}

func TestNewCatalogComponents(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name                  string
		capabilityImageStream bool
		expectedCount         int
	}{
		{
			name:                  "When creating catalog components, it should return 4 components",
			capabilityImageStream: false,
			expectedCount:         4,
		},
		{
			name:                  "When creating catalog components with image stream capability, it should return 4 components",
			capabilityImageStream: true,
			expectedCount:         4,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			components := NewCatalogComponents(tc.capabilityImageStream)

			g.Expect(components).To(HaveLen(tc.expectedCount))
			for _, comp := range components {
				g.Expect(comp).ToNot(BeNil())
			}
		})
	}
}
