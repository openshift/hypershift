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
		disabledCapabilities       []hyperv1.OptionalCapability
		expectImageRegistryEnabled bool
	}{
		{
			name:                       "returns false when image registry capability is disabled",
			disabledCapabilities:       []hyperv1.OptionalCapability{hyperv1.ImageRegistryCapability},
			expectImageRegistryEnabled: false,
		},
		{
			name:                       "returns true when image registry capability is enabled",
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
		disabledCapabilities []hyperv1.OptionalCapability
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
			disabledCapabilities: []hyperv1.OptionalCapability{hyperv1.ImageRegistryCapability},
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

func TestHasDisabledCapabilities(t *testing.T) {
	tests := []struct {
		name                 string
		disabledCapabilities []hyperv1.OptionalCapability
		expectResult         bool
	}{
		{
			name:                 "returns false when none capabilities are disabled",
			disabledCapabilities: nil,
			expectResult:         false,
		},
		{
			name:                 "returns true if any capabilities are disabled",
			disabledCapabilities: []hyperv1.OptionalCapability{hyperv1.OpenShiftSamplesCapability},
			expectResult:         true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			caps := &hyperv1.Capabilities{
				Disabled: test.disabledCapabilities,
			}
			hasDisabledCapabilities := HasDisabledCapabilities(caps)
			if test.expectResult && !hasDisabledCapabilities {
				t.Fatal("expected HasDisabledCapabilities, to be true, but it wasn't")
			}
			if !test.expectResult && hasDisabledCapabilities {
				t.Fatal("expected HasDisabledCapabilities, to be false, but it wasn't")
			}
		})
	}
}
