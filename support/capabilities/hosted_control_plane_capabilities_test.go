package capabilities

import (
	"reflect"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"
)

func TestIsImageRegistryCapabilityEnabled(t *testing.T) {
	tests := []struct {
		name                       string
		disabledCapabilities       []configv1.ClusterVersionCapability
		enabledCapabilities        []configv1.ClusterVersionCapability
		expectImageRegistryEnabled bool
	}{
		{
			name:                       "returns true when image registry capability is neither disabled nor enabled",
			enabledCapabilities:        nil,
			disabledCapabilities:       nil,
			expectImageRegistryEnabled: true,
		},
		{
			name:                       "returns false when image registry capability is disabled",
			enabledCapabilities:        nil,
			disabledCapabilities:       []configv1.ClusterVersionCapability{configv1.ClusterVersionCapabilityImageRegistry},
			expectImageRegistryEnabled: false,
		},
		{
			name:                       "returns true when image registry capability is enabled",
			enabledCapabilities:        []configv1.ClusterVersionCapability{configv1.ClusterVersionCapabilityImageRegistry},
			disabledCapabilities:       nil,
			expectImageRegistryEnabled: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			caps := &hyperv1.Capabilities{
				Disabled: test.disabledCapabilities,
			}
			enabled := IsImageRegistryCapabilityEnabled(caps)
			if test.expectImageRegistryEnabled && !enabled {
				t.Fatal("expected the registry to be enabled, but it wasn't")
			}
			if !test.expectImageRegistryEnabled && enabled {
				t.Fatal("expected the registry to not be enabled, but it was")
			}
		})
	}
}

func TestCalculateEnabledCapabilities(t *testing.T) {
	tests := []struct {
		name                 string
		disabledCapabilities []configv1.ClusterVersionCapability
		expectedCapabilities []configv1.ClusterVersionCapability
	}{
		{
			name:                 "returns default capability set when disabledCapabilities is nil",
			disabledCapabilities: nil,
			expectedCapabilities: []configv1.ClusterVersionCapability{
				configv1.ClusterVersionCapabilityBuild,
				configv1.ClusterVersionCapabilityCSISnapshot,
				configv1.ClusterVersionCapabilityCloudControllerManager,
				configv1.ClusterVersionCapabilityCloudCredential,
				configv1.ClusterVersionCapabilityConsole,
				configv1.ClusterVersionCapabilityDeploymentConfig,
				configv1.ClusterVersionCapabilityImageRegistry,
				configv1.ClusterVersionCapabilityIngress,
				configv1.ClusterVersionCapabilityInsights,
				configv1.ClusterVersionCapabilityMachineAPI,
				configv1.ClusterVersionCapabilityNodeTuning,
				configv1.ClusterVersionCapabilityOperatorLifecycleManager,
				configv1.ClusterVersionCapabilityOperatorLifecycleManagerV1,
				configv1.ClusterVersionCapabilityStorage,
				configv1.ClusterVersionCapabilityBaremetal,
				configv1.ClusterVersionCapabilityMarketplace,
				configv1.ClusterVersionCapabilityOpenShiftSamples,
			},
		},
		{
			name:                 "returns default set minus image registry capability when ImageRegistry capability is Disabled",
			disabledCapabilities: []configv1.ClusterVersionCapability{configv1.ClusterVersionCapabilityImageRegistry},
			expectedCapabilities: []configv1.ClusterVersionCapability{
				configv1.ClusterVersionCapabilityBuild,
				configv1.ClusterVersionCapabilityCSISnapshot,
				configv1.ClusterVersionCapabilityCloudControllerManager,
				configv1.ClusterVersionCapabilityCloudCredential,
				configv1.ClusterVersionCapabilityConsole,
				configv1.ClusterVersionCapabilityDeploymentConfig,
				// configv1.ClusterVersionCapabilityImageRegistry,
				configv1.ClusterVersionCapabilityIngress,
				configv1.ClusterVersionCapabilityInsights,
				configv1.ClusterVersionCapabilityMachineAPI,
				configv1.ClusterVersionCapabilityNodeTuning,
				configv1.ClusterVersionCapabilityOperatorLifecycleManager,
				configv1.ClusterVersionCapabilityOperatorLifecycleManagerV1,
				configv1.ClusterVersionCapabilityStorage,
				configv1.ClusterVersionCapabilityBaremetal,
				configv1.ClusterVersionCapabilityMarketplace,
				configv1.ClusterVersionCapabilityOpenShiftSamples,
			},
		},
		{
			name:                 "returns default set minus baremetal capability when baremetal capability is Disabled",
			disabledCapabilities: []configv1.ClusterVersionCapability{configv1.ClusterVersionCapabilityBaremetal},
			expectedCapabilities: []configv1.ClusterVersionCapability{
				configv1.ClusterVersionCapabilityBuild,
				configv1.ClusterVersionCapabilityCSISnapshot,
				configv1.ClusterVersionCapabilityCloudControllerManager,
				configv1.ClusterVersionCapabilityCloudCredential,
				configv1.ClusterVersionCapabilityConsole,
				configv1.ClusterVersionCapabilityDeploymentConfig,
				configv1.ClusterVersionCapabilityImageRegistry,
				configv1.ClusterVersionCapabilityIngress,
				configv1.ClusterVersionCapabilityInsights,
				configv1.ClusterVersionCapabilityMachineAPI,
				configv1.ClusterVersionCapabilityNodeTuning,
				configv1.ClusterVersionCapabilityOperatorLifecycleManager,
				configv1.ClusterVersionCapabilityOperatorLifecycleManagerV1,
				configv1.ClusterVersionCapabilityStorage,
				// configv1.ClusterVersionCapabilityBaremetal,
				configv1.ClusterVersionCapabilityMarketplace,
				configv1.ClusterVersionCapabilityOpenShiftSamples,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			caps := &hyperv1.Capabilities{
				Disabled: test.disabledCapabilities,
			}
			enabledCapabilities := CalculateEnabledCapabilities(caps)
			if !reflect.DeepEqual(test.expectedCapabilities, enabledCapabilities) {
				t.Logf("expected enabled capabilities: %v", test.expectedCapabilities)
				t.Logf("calculated enabled capabilities: %v", enabledCapabilities)
				t.Fatalf("expected enabled capabilities differed from calculated enabled capabilities")
			}
		})
	}
}
