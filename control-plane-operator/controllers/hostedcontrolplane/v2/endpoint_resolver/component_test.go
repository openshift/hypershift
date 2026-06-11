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
		monitoring  hyperv1.MonitoringSpec
		expected    bool
	}{
		{
			name:        "When metrics forwarding mode is not set, it should return false",
			annotations: map[string]string{},
			expected:    false,
		},
		{
			name:        "When DisableMonitoringServices is set, it should return false even if forwarding is enabled",
			annotations: map[string]string{hyperv1.DisableMonitoringServices: "true"},
			monitoring: hyperv1.MonitoringSpec{
				MetricsForwarding: hyperv1.MetricsForwardingSpec{
					Mode: hyperv1.MetricsForwardingModeForward,
				},
			},
			expected: false,
		},
		{
			name: "When metrics forwarding mode is Enabled, it should return true",
			monitoring: hyperv1.MonitoringSpec{
				MetricsForwarding: hyperv1.MetricsForwardingSpec{
					Mode: hyperv1.MetricsForwardingModeForward,
				},
			},
			expected: true,
		},
		{
			name: "When metrics forwarding mode is Disabled, it should return false",
			monitoring: hyperv1.MonitoringSpec{
				MetricsForwarding: hyperv1.MetricsForwardingSpec{
					Mode: hyperv1.MetricsForwardingModeNone,
				},
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
				Spec: hyperv1.HostedControlPlaneSpec{
					Monitoring: tc.monitoring,
				},
			}

			cpContext := component.WorkloadContext{
				Context: context.Background(),
				HCP:     hcp,
			}

			result, err := Predicate(cpContext)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(result).To(Equal(tc.expected))
		})
	}
}
