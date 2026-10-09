package hostedcluster

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestIsIgnitionServerDisabled(t *testing.T) {
	tests := []struct {
		name     string
		hcluster *hyperv1.HostedCluster
		expected bool
	}{
		{
			name: "When the DisableIgnitionServerAnnotation is set, it should report the ignition server as disabled",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						hyperv1.DisableIgnitionServerAnnotation: "true",
					},
				},
			},
			expected: true,
		},
		{
			name: "When the DisableIgnitionServerAnnotation is set to an empty value, it should still report the ignition server as disabled",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						hyperv1.DisableIgnitionServerAnnotation: "",
					},
				},
			},
			expected: true,
		},
		{
			name: "When the DisableIgnitionServerAnnotation is absent, it should report the ignition server as enabled",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"some.other/annotation": "value",
					},
				},
			},
			expected: false,
		},
		{
			name:     "When the HostedCluster has no annotations, it should report the ignition server as enabled without panicking",
			hcluster: &hyperv1.HostedCluster{},
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(isIgnitionServerDisabled(tc.hcluster)).To(Equal(tc.expected))
		})
	}
}
