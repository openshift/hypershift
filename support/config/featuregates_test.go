package config

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestFeatureGateDetailsFromConfigMap(t *testing.T) {
	testCases := []struct {
		name           string
		configMapData  map[string]string
		releaseVersion string
		expectError    bool
		expectedResult *configv1.FeatureGateDetails
	}{
		{
			name: "When the CM has valid feature gates it should return the feature gate details",
			configMapData: map[string]string{
				"featureGates": `
version: "4.19.0"
enabled:
- name: CSIDriverSharedResource
- name: ExternalCloudProvider
disabled:
- name: BuildCSIVolumes
`,
			},
			releaseVersion: "4.19.0",
			expectedResult: &configv1.FeatureGateDetails{
				Version: "4.19.0",
				Enabled: []configv1.FeatureGateAttributes{
					{Name: "CSIDriverSharedResource"},
					{Name: "ExternalCloudProvider"},
				},
				Disabled: []configv1.FeatureGateAttributes{
					{Name: "BuildCSIVolumes"},
				},
			},
		},
		{
			name: "When the CM has a version mismatch it should return an error",
			configMapData: map[string]string{
				"featureGates": `
version: "4.18.0"
enabled:
- name: CSIDriverSharedResource
- name: ExternalCloudProvider
disabled:
- name: BuildCSIVolumes
`,
			},
			releaseVersion: "4.19.0",
			expectError:    true,
		},
		{
			name: "When the CM has invalid yaml it should return an error",
			configMapData: map[string]string{
				"featureGates": `
version: "4.19.0"
enabled:
- InvalidFormat
- name: ExternalCloudProvider
disabled:
- name: BuildCSIVolumes
`,
			},
			releaseVersion: "4.19.0",
			expectError:    true,
		},
		{
			name: "When the CM has a missing featureGates key it should return an error",
			configMapData: map[string]string{
				"InvalidKey": "some value",
			},
			releaseVersion: "4.19.0",
			expectError:    true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			scheme := runtime.NewScheme()
			err := corev1.AddToScheme(scheme)
			Expect(err).NotTo(HaveOccurred())

			configMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hostedcluster-gates",
					Namespace: "test-namespace",
				},
				Data: tc.configMapData,
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(configMap).
				Build()

			result, err := FeatureGateDetailsFromConfigMap(fakeClient, context.Background(), "test-namespace", tc.releaseVersion)
			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(result).To(Equal(tc.expectedResult))
			}
		})
	}
}

func TestFeatureGatesFromDetails(t *testing.T) {
	testCases := []struct {
		name           string
		featureGates   *configv1.FeatureGateDetails
		expectedResult []string
	}{
		{
			name: "When there are enabled and disabled feature gates it should return the correct feature gates",
			featureGates: &configv1.FeatureGateDetails{
				Version: "4.19.0",
				Enabled: []configv1.FeatureGateAttributes{
					{Name: "ExternalCloudProvider"},
					{Name: "CSIDriverSharedResource"},
				},
				Disabled: []configv1.FeatureGateAttributes{
					{Name: "BuildCSIVolumes"},
					{Name: "NodeSwap"},
				},
			},
			expectedResult: []string{
				"ExternalCloudProvider=true",
				"CSIDriverSharedResource=true",
				"BuildCSIVolumes=false",
				"NodeSwap=false",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			result := FeatureGatesFromDetails(tc.featureGates)
			g.Expect(result).To(ConsistOf(tc.expectedResult))
		})
	}
}
