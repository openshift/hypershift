package capabilities

import (
	"reflect"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/config"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/blang/semver"
)

func TestIsImageRegistryCapabilityEnabled(t *testing.T) {
	tests := []struct {
		name                       string
		disabledCapabilities       []hyperv1.OptionalCapability
		enabledCapabilities        []hyperv1.OptionalCapability
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
			disabledCapabilities:       []hyperv1.OptionalCapability{hyperv1.ImageRegistryCapability},
			expectImageRegistryEnabled: false,
		},
		{
			name:                       "returns true when image registry capability is enabled",
			enabledCapabilities:        []hyperv1.OptionalCapability{hyperv1.ImageRegistryCapability},
			disabledCapabilities:       nil,
			expectImageRegistryEnabled: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			caps := &hyperv1.Capabilities{
				Enabled:  test.enabledCapabilities,
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

func TestSupportsCompatibilityRequirements(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		releaseVersion string
		expected       bool
	}{
		{name: "When the release version is zero, it should not support compatibility requirements", releaseVersion: "0.0.0", expected: false},
		{name: "When the release is before 5.1, it should not support compatibility requirements", releaseVersion: "5.0.99", expected: false},
		{name: "When the release is 5.1, it should support compatibility requirements", releaseVersion: "5.1.0", expected: true},
		{name: "When a 5.1 prerelease is used, it should support compatibility requirements", releaseVersion: "5.1.0-0.ci-20260909", expected: true},
		{name: "When a later release is used, it should support compatibility requirements", releaseVersion: "6.0.0", expected: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			version, err := semver.Parse(test.releaseVersion)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(SupportsCompatibilityRequirements(version)).To(Equal(test.expected))
		})
	}
}

func TestCalculateEnabledCapabilities(t *testing.T) {
	t.Run("When capabilities are nil on 5.1, it should include compatibility requirements", func(t *testing.T) {
		g := NewWithT(t)
		g.Expect(CalculateEnabledCapabilities(nil, config.Version510)).To(ContainElement(configv1.ClusterVersionCapabilityCompatibilityRequirements))
	})

	tests := []struct {
		name                 string
		releaseVersion       semver.Version
		enabledCapabilities  []hyperv1.OptionalCapability
		disabledCapabilities []hyperv1.OptionalCapability
		expectedCapabilities []configv1.ClusterVersionCapability
	}{
		{
			name:                 "When capability overrides are nil on 5.1, it should include compatibility requirements",
			releaseVersion:       config.Version510,
			enabledCapabilities:  nil,
			disabledCapabilities: nil,
			expectedCapabilities: []configv1.ClusterVersionCapability{
				configv1.ClusterVersionCapabilityBuild,
				configv1.ClusterVersionCapabilityCSISnapshot,
				configv1.ClusterVersionCapabilityCloudControllerManager,
				configv1.ClusterVersionCapabilityCloudCredential,
				configv1.ClusterVersionCapabilityCompatibilityRequirements,
				configv1.ClusterVersionCapabilityConsole,
				configv1.ClusterVersionCapabilityDeploymentConfig,
				configv1.ClusterVersionCapabilityImageRegistry,
				configv1.ClusterVersionCapabilityIngress,
				configv1.ClusterVersionCapabilityInsights,
				// configv1.ClusterVersionCapabilityMachineAPI,
				configv1.ClusterVersionCapabilityNodeTuning,
				configv1.ClusterVersionCapabilityOperatorLifecycleManager,
				configv1.ClusterVersionCapabilityOperatorLifecycleManagerV1,
				configv1.ClusterVersionCapabilityStorage,
				configv1.ClusterVersionCapabilityMarketplace,
				configv1.ClusterVersionCapabilityOpenShiftSamples,
			},
		},
		{
			name:                 "When ImageRegistry is disabled, it should retain compatibility requirements",
			releaseVersion:       config.Version510,
			enabledCapabilities:  nil,
			disabledCapabilities: []hyperv1.OptionalCapability{hyperv1.ImageRegistryCapability},
			expectedCapabilities: []configv1.ClusterVersionCapability{
				configv1.ClusterVersionCapabilityBuild,
				configv1.ClusterVersionCapabilityCSISnapshot,
				configv1.ClusterVersionCapabilityCloudControllerManager,
				configv1.ClusterVersionCapabilityCloudCredential,
				configv1.ClusterVersionCapabilityCompatibilityRequirements,
				configv1.ClusterVersionCapabilityConsole,
				configv1.ClusterVersionCapabilityDeploymentConfig,
				// configv1.ClusterVersionCapabilityImageRegistry,
				configv1.ClusterVersionCapabilityIngress,
				configv1.ClusterVersionCapabilityInsights,
				// configv1.ClusterVersionCapabilityMachineAPI,
				configv1.ClusterVersionCapabilityNodeTuning,
				configv1.ClusterVersionCapabilityOperatorLifecycleManager,
				configv1.ClusterVersionCapabilityOperatorLifecycleManagerV1,
				configv1.ClusterVersionCapabilityStorage,
				configv1.ClusterVersionCapabilityMarketplace,
				configv1.ClusterVersionCapabilityOpenShiftSamples,
			},
		},
		{
			name:                 "When baremetal is enabled, it should retain compatibility requirements",
			releaseVersion:       config.Version510,
			enabledCapabilities:  []hyperv1.OptionalCapability{hyperv1.BaremetalCapability},
			disabledCapabilities: nil,
			expectedCapabilities: []configv1.ClusterVersionCapability{
				configv1.ClusterVersionCapabilityBuild,
				configv1.ClusterVersionCapabilityCSISnapshot,
				configv1.ClusterVersionCapabilityCloudControllerManager,
				configv1.ClusterVersionCapabilityCloudCredential,
				configv1.ClusterVersionCapabilityCompatibilityRequirements,
				configv1.ClusterVersionCapabilityConsole,
				configv1.ClusterVersionCapabilityDeploymentConfig,
				configv1.ClusterVersionCapabilityImageRegistry,
				configv1.ClusterVersionCapabilityIngress,
				configv1.ClusterVersionCapabilityInsights,
				// configv1.ClusterVersionCapabilityMachineAPI,
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
				Enabled:  test.enabledCapabilities,
				Disabled: test.disabledCapabilities,
			}
			enabledCapabilities := CalculateEnabledCapabilities(caps, test.releaseVersion)
			if !reflect.DeepEqual(test.expectedCapabilities, enabledCapabilities) {
				t.Logf("expected enabled capabilities: %v", test.expectedCapabilities)
				t.Logf("calculated enabled capabilities: %v", enabledCapabilities)
				t.Fatalf("expected enabled capabilities differed from calculated enabled capabilities")
			}
		})
	}
}

func TestFilterByKnownCapabilities(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name              string
		desired           []configv1.ClusterVersionCapability
		knownCapabilities []configv1.ClusterVersionCapability
		expectedFiltered  []configv1.ClusterVersionCapability
	}{
		{
			name: "When knownCapabilities is empty, it should return all desired capabilities unfiltered",
			desired: []configv1.ClusterVersionCapability{
				configv1.ClusterVersionCapabilityBuild,
				configv1.ClusterVersionCapabilityClusterAPI,
			},
			knownCapabilities: nil,
			expectedFiltered: []configv1.ClusterVersionCapability{
				configv1.ClusterVersionCapabilityBuild,
				configv1.ClusterVersionCapabilityClusterAPI,
			},
		},
		{
			name: "When guest CVO does not know some capabilities, it should filter them out",
			desired: []configv1.ClusterVersionCapability{
				configv1.ClusterVersionCapabilityBuild,
				configv1.ClusterVersionCapabilityClusterAPI,
				configv1.ClusterVersionCapabilityCompatibilityRequirements,
				configv1.ClusterVersionCapabilityConsole,
			},
			knownCapabilities: []configv1.ClusterVersionCapability{
				configv1.ClusterVersionCapabilityBuild,
				configv1.ClusterVersionCapabilityConsole,
			},
			expectedFiltered: []configv1.ClusterVersionCapability{
				configv1.ClusterVersionCapabilityBuild,
				configv1.ClusterVersionCapabilityConsole,
			},
		},
		{
			name: "When all desired capabilities are known, it should return all desired capabilities",
			desired: []configv1.ClusterVersionCapability{
				configv1.ClusterVersionCapabilityBuild,
				configv1.ClusterVersionCapabilityConsole,
			},
			knownCapabilities: []configv1.ClusterVersionCapability{
				configv1.ClusterVersionCapabilityBuild,
				configv1.ClusterVersionCapabilityConsole,
				configv1.ClusterVersionCapabilityStorage,
			},
			expectedFiltered: []configv1.ClusterVersionCapability{
				configv1.ClusterVersionCapabilityBuild,
				configv1.ClusterVersionCapabilityConsole,
			},
		},
		{
			name: "When no desired capabilities are known, it should return nil",
			desired: []configv1.ClusterVersionCapability{
				configv1.ClusterVersionCapabilityClusterAPI,
				configv1.ClusterVersionCapabilityCompatibilityRequirements,
			},
			knownCapabilities: []configv1.ClusterVersionCapability{
				configv1.ClusterVersionCapabilityBuild,
			},
			expectedFiltered: nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			filtered := FilterByKnownCapabilities(test.desired, test.knownCapabilities)
			g.Expect(filtered).To(Equal(test.expectedFiltered))
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
