package nto

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestIsNodeTuningCapabilityEnabled(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name         string
		capabilities *hyperv1.Capabilities
		expected     bool
	}{
		{
			name:         "When capabilities is nil, it should return true",
			capabilities: nil,
			expected:     true,
		},
		{
			name: "When NodeTuning capability is not disabled, it should return true",
			capabilities: &hyperv1.Capabilities{
				Disabled: []hyperv1.OptionalCapability{
					hyperv1.IngressCapability,
				},
			},
			expected: true,
		},
		{
			name: "When NodeTuning capability is disabled, it should return false",
			capabilities: &hyperv1.Capabilities{
				Disabled: []hyperv1.OptionalCapability{
					hyperv1.NodeTuningCapability,
				},
			},
			expected: false,
		},
		{
			name: "When NodeTuning capability is disabled with other capabilities, it should return false",
			capabilities: &hyperv1.Capabilities{
				Disabled: []hyperv1.OptionalCapability{
					hyperv1.IngressCapability,
					hyperv1.NodeTuningCapability,
					hyperv1.ImageRegistryCapability,
				},
			},
			expected: false,
		},
		{
			name: "When capabilities has empty disabled list, it should return true",
			capabilities: &hyperv1.Capabilities{
				Disabled: []hyperv1.OptionalCapability{},
			},
			expected: true,
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
					Capabilities: tc.capabilities,
				},
			}

			cpContext := component.WorkloadContext{
				Context: t.Context(),
				HCP:     hcp,
			}

			result, err := isNodeTuningCapabilityEnabled(cpContext)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(result).To(Equal(tc.expected))
		})
	}
}

func TestClusterNodeTuningOperatorOptions(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name                       string
		methodName                 string
		expectedIsRequestServing   bool
		expectedMultiZoneSpread    bool
		expectedNeedsManagementKAS bool
	}{
		{
			name:                       "When IsRequestServing is called, it should return false",
			methodName:                 "IsRequestServing",
			expectedIsRequestServing:   false,
			expectedMultiZoneSpread:    false,
			expectedNeedsManagementKAS: true,
		},
		{
			name:                       "When MultiZoneSpread is called, it should return false",
			methodName:                 "MultiZoneSpread",
			expectedIsRequestServing:   false,
			expectedMultiZoneSpread:    false,
			expectedNeedsManagementKAS: true,
		},
		{
			name:                       "When NeedsManagementKASAccess is called, it should return true",
			methodName:                 "NeedsManagementKASAccess",
			expectedIsRequestServing:   false,
			expectedMultiZoneSpread:    false,
			expectedNeedsManagementKAS: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			g := NewWithT(t)

			operator := &clusterNodeTuningOperator{}

			g.Expect(operator.IsRequestServing()).To(Equal(tc.expectedIsRequestServing))
			g.Expect(operator.MultiZoneSpread()).To(Equal(tc.expectedMultiZoneSpread))
			g.Expect(operator.NeedsManagementKASAccess()).To(Equal(tc.expectedNeedsManagementKAS))
		})
	}
}
