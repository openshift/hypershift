package endpointresolver

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPredicate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		annotations map[string]string
		expected    bool
	}{
		{
			name:        "When both annotations are absent, it should return false",
			annotations: map[string]string{},
			expected:    false,
		},
		{
			name: "When only DisableMonitoringServices is present, it should return false",
			annotations: map[string]string{
				hyperv1.DisableMonitoringServices: "true",
			},
			expected: false,
		},
		{
			name: "When only EnableMetricsForwarding is present, it should return true",
			annotations: map[string]string{
				hyperv1.EnableMetricsForwarding: "true",
			},
			expected: true,
		},
		{
			name: "When both annotations are present, it should return false",
			annotations: map[string]string{
				hyperv1.DisableMonitoringServices: "true",
				hyperv1.EnableMetricsForwarding:   "true",
			},
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: tc.annotations,
				},
			}

			cpContext := component.WorkloadContext{
				Context: context.Background(),
				HCP:     hcp,
			}

			result, err := predicate(cpContext)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(result).To(Equal(tc.expected))
		})
	}
}
