package olm

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/util"
	"github.com/openshift/hypershift/support/util/fakeimagemetadataprovider"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestNewOperatorLifecycleManagerParams(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name                  string
		annotation            string
		imageMetadataProvider util.ImageMetadataProvider
		expectErr             string
	}{
		{
			name:                  "When OLMCatalogsISRegistryOverridesAnnotation contains malformed data, it should return a parse error",
			annotation:            "bad_value_no_equals",
			imageMetadataProvider: nil,
			expectErr:             "failed to parse OLM catalog registry overrides annotation",
		},
		{
			name:                  "When OLMCatalogsISRegistryOverridesAnnotation is empty, it should succeed with nil overrides",
			annotation:            "",
			imageMetadataProvider: &fakeimagemetadataprovider.FakeRegistryClientImageMetadataProviderHCCO{},
		},
		{
			name:                  "When OLMCatalogsISRegistryOverridesAnnotation has valid overrides, it should parse successfully",
			annotation:            "source=mirror",
			imageMetadataProvider: &fakeimagemetadataprovider.FakeRegistryClientImageMetadataProviderHCCO{},
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
					Annotations: map[string]string{
						hyperv1.OLMCatalogsISRegistryOverridesAnnotation: tc.annotation,
					},
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					ReleaseImage: "quay.io/openshift-release-dev/ocp-release:4.18.0-x86_64",
				},
			}

			pullSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pull-secret",
					Namespace: "test-namespace",
				},
				Data: map[string][]byte{
					corev1.DockerConfigJsonKey: []byte("{}"),
				},
			}

			_, err := NewOperatorLifecycleManagerParams(t.Context(), hcp, pullSecret, tc.imageMetadataProvider)
			if tc.expectErr != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tc.expectErr))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
}
