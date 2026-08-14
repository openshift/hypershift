package ingressoperator

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"
)

func TestIsIngressCapabilityEnabled(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name         string
		capabilities *hyperv1.Capabilities
		expected     bool
	}{
		{
			name:         "When capabilities is nil, it should return true because all capabilities are enabled by default",
			capabilities: nil,
			expected:     true,
		},
		{
			name:         "When capabilities is empty with no disabled entries, it should return true",
			capabilities: &hyperv1.Capabilities{},
			expected:     true,
		},
		{
			name: "When Ingress is explicitly listed in the disabled capabilities, it should return false",
			capabilities: &hyperv1.Capabilities{
				Disabled: []hyperv1.OptionalCapability{hyperv1.IngressCapability},
			},
			expected: false,
		},
		{
			name: "When a different capability is disabled but Ingress is not, it should return true",
			capabilities: &hyperv1.Capabilities{
				Disabled: []hyperv1.OptionalCapability{hyperv1.NodeTuningCapability},
			},
			expected: true,
		},
		{
			name: "When multiple capabilities are disabled including Ingress, it should return false",
			capabilities: &hyperv1.Capabilities{
				Disabled: []hyperv1.OptionalCapability{
					hyperv1.NodeTuningCapability,
					hyperv1.IngressCapability,
					hyperv1.ConsoleCapability,
				},
			},
			expected: false,
		},
		{
			name: "When Ingress is listed in the enabled capabilities, it should return true",
			capabilities: &hyperv1.Capabilities{
				Enabled: []hyperv1.OptionalCapability{hyperv1.IngressCapability},
			},
			expected: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			cpContext := component.WorkloadContext{
				HCP: &hyperv1.HostedControlPlane{
					Spec: hyperv1.HostedControlPlaneSpec{
						Capabilities: tc.capabilities,
					},
				},
			}

			result, err := isIngressCapabilityEnabled(cpContext)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(result).To(Equal(tc.expected))
		})
	}
}
