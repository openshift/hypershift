package config

import (
	"testing"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"

	"github.com/blang/semver"
)

func TestMutatingOwnerRefFromHCP(t *testing.T) {
	tests := []struct {
		name           string
		releaseVersion semver.Version
		expected       string
	}{
		{
			name:           "4.9.0 should use v1alpha1",
			releaseVersion: semver.MustParse("4.9.0"),
			expected:       "hypershift.openshift.io/v1alpha1",
		},
		{
			name:           "4.10.0 should use v1alpha1",
			releaseVersion: semver.MustParse("4.10.0"),
			expected:       "hypershift.openshift.io/v1alpha1",
		},
		{
			name:           "4.11.0 should use v1alpha1",
			releaseVersion: semver.MustParse("4.11.0"),
			expected:       "hypershift.openshift.io/v1alpha1",
		},
		{
			name:           "4.12.0 should use v1beta1",
			releaseVersion: semver.MustParse("4.12.0"),
			expected:       "hypershift.openshift.io/v1beta1",
		},
		{
			name:           "4.13.0 should use v1beta1",
			releaseVersion: semver.MustParse("4.13.0"),
			expected:       "hypershift.openshift.io/v1beta1",
		},
		{
			name:           "default should use v1beta1",
			releaseVersion: semver.MustParse("0.0.0"),
			expected:       "hypershift.openshift.io/v1beta1",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			result := MutatingOwnerRefFromHCP(&hyperv1.HostedControlPlane{}, test.releaseVersion)
			g.Expect(result.Reference.APIVersion).To(Equal(test.expected))
		})
	}
}
