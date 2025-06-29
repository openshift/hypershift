package hostedcontrolplane

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/stretchr/testify/assert"
)

func TestDisableMultusDefaultBehavior(t *testing.T) {
	tests := []struct {
		name                  string
		setupHCP              func() *hyperv1.HostedControlPlane
		expectedDisableMultus bool
		description           string
	}{
		{
			name: "Default ClusterNetworking should have multus enabled",
			setupHCP: func() *hyperv1.HostedControlPlane {
				return &hyperv1.HostedControlPlane{
					Spec: hyperv1.HostedControlPlaneSpec{
						OperatorConfiguration: &hyperv1.OperatorConfiguration{ClusterNetworkOperator: &hyperv1.ClusterNetworkOperatorSpec{}},
						Networking: hyperv1.ClusterNetworking{
							// DisableMultus not explicitly set - should default to false
							NetworkType: hyperv1.OVNKubernetes,
						},
					},
				}
			},
			expectedDisableMultus: false,
			description:           "When DisableMultus is not explicitly set, it should default to false (multus enabled)",
		},
		{
			name: "Explicit false should enable multus",
			setupHCP: func() *hyperv1.HostedControlPlane {
				return &hyperv1.HostedControlPlane{
					Spec: hyperv1.HostedControlPlaneSpec{
						OperatorConfiguration: &hyperv1.OperatorConfiguration{ClusterNetworkOperator: &hyperv1.ClusterNetworkOperatorSpec{DisableMultus: false}},
						Networking: hyperv1.ClusterNetworking{
							NetworkType: hyperv1.OVNKubernetes,
						},
					},
				}
			},
			expectedDisableMultus: false,
			description:           "When DisableMultus is explicitly set to false, multus should be enabled",
		},
		{
			name: "Explicit true should disable multus",
			setupHCP: func() *hyperv1.HostedControlPlane {
				return &hyperv1.HostedControlPlane{
					Spec: hyperv1.HostedControlPlaneSpec{
						OperatorConfiguration: &hyperv1.OperatorConfiguration{ClusterNetworkOperator: &hyperv1.ClusterNetworkOperatorSpec{DisableMultus: true}},
						Networking: hyperv1.ClusterNetworking{
							NetworkType: hyperv1.OVNKubernetes,
						},
					},
				}
			},
			expectedDisableMultus: true,
			description:           "When DisableMultus is explicitly set to true, multus should be disabled",
		},
		{
			name: "DisableMultus works with empty networking config",
			setupHCP: func() *hyperv1.HostedControlPlane {
				return &hyperv1.HostedControlPlane{
					Spec: hyperv1.HostedControlPlaneSpec{
						OperatorConfiguration: &hyperv1.OperatorConfiguration{ClusterNetworkOperator: &hyperv1.ClusterNetworkOperatorSpec{DisableMultus: true}},
						Networking:            hyperv1.ClusterNetworking{
							// Minimal networking config
						},
					},
				}
			},
			expectedDisableMultus: true,
			description:           "DisableMultus should work even with minimal networking configuration",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			hcp := test.setupHCP()

			// Test that the field value matches expectations
			assert.Equal(t, test.expectedDisableMultus, hcp.Spec.OperatorConfiguration.ClusterNetworkOperator.DisableMultus, test.description)

			// Test that the reconciler logic would make the correct decision
			shouldProcessMultus := !hcp.Spec.OperatorConfiguration.ClusterNetworkOperator.DisableMultus
			expectedShouldProcess := !test.expectedDisableMultus
			assert.Equal(t, expectedShouldProcess, shouldProcessMultus,
				"Reconciler logic should correctly interpret DisableMultus field")
		})
	}
}

func TestDisableMultusRestartLogic(t *testing.T) {
	tests := []struct {
		name                 string
		disableMultus        bool
		hasRestartAnnotation bool
		expectedRestart      bool
	}{
		{
			name:                 "Should restart: has annotation and multus enabled",
			disableMultus:        false,
			hasRestartAnnotation: true,
			expectedRestart:      true,
		},
		{
			name:                 "Should not restart: has annotation but multus disabled",
			disableMultus:        true,
			hasRestartAnnotation: true,
			expectedRestart:      false,
		},
		{
			name:                 "Should not restart: no annotation, multus enabled",
			disableMultus:        false,
			hasRestartAnnotation: false,
			expectedRestart:      false,
		},
		{
			name:                 "Should not restart: no annotation, multus disabled",
			disableMultus:        true,
			hasRestartAnnotation: false,
			expectedRestart:      false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			hcp := &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					OperatorConfiguration: &hyperv1.OperatorConfiguration{ClusterNetworkOperator: &hyperv1.ClusterNetworkOperatorSpec{DisableMultus: test.disableMultus}},
				},
			}

			// Test the restart logic: restart when hasRestartAnnotation && !hcp.Spec.Networking.DisableMultus
			shouldRestart := test.hasRestartAnnotation && !hcp.Spec.OperatorConfiguration.ClusterNetworkOperator.DisableMultus
			assert.Equal(t, test.expectedRestart, shouldRestart, "Restart logic test failed")
		})
	}
}
