package hostedcontrolplane

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/util"

	"k8s.io/utils/ptr"

	"github.com/stretchr/testify/assert"
)

func TestDisableMultiNetworkDefaultBehavior(t *testing.T) {
	tests := []struct {
		name                        string
		setupHCP                    func() *hyperv1.HostedControlPlane
		expectedDisableMultiNetwork *bool
		description                 string
	}{
		{
			name: "Default ClusterNetworking should have multus enabled",
			setupHCP: func() *hyperv1.HostedControlPlane {
				return &hyperv1.HostedControlPlane{
					Spec: hyperv1.HostedControlPlaneSpec{
						OperatorConfiguration: &hyperv1.OperatorConfiguration{ClusterNetworkOperator: &hyperv1.ClusterNetworkOperatorSpec{}},
						Networking: hyperv1.ClusterNetworking{
							// DisableMultiNetwork not explicitly set - should default to false
							NetworkType: hyperv1.OVNKubernetes,
						},
					},
				}
			},
			expectedDisableMultiNetwork: nil,
			description:                 "When DisableMultiNetwork is not explicitly set, it should default to nil (multus enabled)",
		},
		{
			name: "Explicit false should enable multus",
			setupHCP: func() *hyperv1.HostedControlPlane {
				return &hyperv1.HostedControlPlane{
					Spec: hyperv1.HostedControlPlaneSpec{
						OperatorConfiguration: &hyperv1.OperatorConfiguration{ClusterNetworkOperator: &hyperv1.ClusterNetworkOperatorSpec{DisableMultiNetwork: ptr.To(false)}},
						Networking: hyperv1.ClusterNetworking{
							NetworkType: hyperv1.OVNKubernetes,
						},
					},
				}
			},
			expectedDisableMultiNetwork: ptr.To(false),
			description:                 "When DisableMultiNetwork is explicitly set to false, multus should be enabled",
		},
		{
			name: "Explicit true should disable multus",
			setupHCP: func() *hyperv1.HostedControlPlane {
				return &hyperv1.HostedControlPlane{
					Spec: hyperv1.HostedControlPlaneSpec{
						OperatorConfiguration: &hyperv1.OperatorConfiguration{ClusterNetworkOperator: &hyperv1.ClusterNetworkOperatorSpec{DisableMultiNetwork: ptr.To(true)}},
						Networking: hyperv1.ClusterNetworking{
							NetworkType: hyperv1.OVNKubernetes,
						},
					},
				}
			},
			expectedDisableMultiNetwork: ptr.To(true),
			description:                 "When DisableMultiNetwork is explicitly set to true, multus should be disabled",
		},
		{
			name: "DisableMultiNetwork works with empty networking config",
			setupHCP: func() *hyperv1.HostedControlPlane {
				return &hyperv1.HostedControlPlane{
					Spec: hyperv1.HostedControlPlaneSpec{
						OperatorConfiguration: &hyperv1.OperatorConfiguration{ClusterNetworkOperator: &hyperv1.ClusterNetworkOperatorSpec{DisableMultiNetwork: ptr.To(true)}},
						Networking:            hyperv1.ClusterNetworking{
							// Minimal networking config
						},
					},
				}
			},
			expectedDisableMultiNetwork: ptr.To(true),
			description:                 "DisableMultiNetwork should work even with minimal networking configuration",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			hcp := test.setupHCP()

			// Test that the field value matches expectations
			if test.expectedDisableMultiNetwork == nil {
				assert.Nil(t, hcp.Spec.OperatorConfiguration.ClusterNetworkOperator.DisableMultiNetwork, test.description)
			} else {
				assert.NotNil(t, hcp.Spec.OperatorConfiguration.ClusterNetworkOperator.DisableMultiNetwork, test.description)
				assert.Equal(t, *test.expectedDisableMultiNetwork, *hcp.Spec.OperatorConfiguration.ClusterNetworkOperator.DisableMultiNetwork, test.description)
			}

			// Test that the reconciler logic would make the correct decision
			shouldProcessMultus := !util.IsDisableMultiNetwork(hcp)

			expectedDisableMultiNetwork := false
			if test.expectedDisableMultiNetwork != nil {
				expectedDisableMultiNetwork = *test.expectedDisableMultiNetwork
			}
			expectedShouldProcess := !expectedDisableMultiNetwork
			assert.Equal(t, expectedShouldProcess, shouldProcessMultus,
				"Reconciler logic should correctly interpret DisableMultiNetwork field")
		})
	}
}

func TestDisableMultiNetworkRestartLogic(t *testing.T) {
	tests := []struct {
		name                 string
		disableMultiNetwork  *bool
		hasRestartAnnotation bool
		expectedRestart      bool
	}{
		{
			name:                 "Should restart: has annotation and multus enabled",
			disableMultiNetwork:  ptr.To(false),
			hasRestartAnnotation: true,
			expectedRestart:      true,
		},
		{
			name:                 "Should not restart: has annotation but multus disabled",
			disableMultiNetwork:  ptr.To(true),
			hasRestartAnnotation: true,
			expectedRestart:      false,
		},
		{
			name:                 "Should not restart: no annotation, multus enabled",
			disableMultiNetwork:  ptr.To(false),
			hasRestartAnnotation: false,
			expectedRestart:      false,
		},
		{
			name:                 "Should not restart: no annotation, multus disabled",
			disableMultiNetwork:  ptr.To(true),
			hasRestartAnnotation: false,
			expectedRestart:      false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			hcp := &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					OperatorConfiguration: &hyperv1.OperatorConfiguration{ClusterNetworkOperator: &hyperv1.ClusterNetworkOperatorSpec{DisableMultiNetwork: test.disableMultiNetwork}},
				},
			}

			// Test the restart logic: restart when hasRestartAnnotation && !DisableMultiNetwork
			shouldRestart := test.hasRestartAnnotation && !util.IsDisableMultiNetwork(hcp)
			assert.Equal(t, test.expectedRestart, shouldRestart, "Restart logic test failed")
		})
	}
}
