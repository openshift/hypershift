package kubevirt

import (
	"encoding/json"
	"strings"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/api/util/ipnet"

	mcfgv1 "github.com/openshift/api/machineconfiguration/v1"

	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/utils/ptr"

	ignitionapi "github.com/coreos/ignition/v2/config/v3_2/types"
	"github.com/vincent-petithory/dataurl"
)

// decodeIgnitionFileContents parses the YAML MachineConfig, extracts the ignition
// config from Spec.Config.Raw, and returns the concatenated decoded file contents.
func decodeIgnitionFileContents(configYAML string) string {
	mc := &mcfgv1.MachineConfig{}
	if err := yaml.NewYAMLOrJSONDecoder(strings.NewReader(configYAML), 4096).Decode(mc); err != nil {
		return ""
	}
	if mc.Spec.Config.Raw == nil {
		return ""
	}

	ignConfig := &ignitionapi.Config{}
	if err := json.Unmarshal(mc.Spec.Config.Raw, ignConfig); err != nil {
		return ""
	}

	var decoded strings.Builder
	for _, f := range ignConfig.Storage.Files {
		if f.Contents.Source == nil {
			continue
		}
		du, err := dataurl.DecodeString(*f.Contents.Source)
		if err != nil {
			continue
		}
		decoded.Write(du.Data)
	}
	return decoded.String()
}

func ipv4Networking() hyperv1.ClusterNetworking {
	return hyperv1.ClusterNetworking{
		ClusterNetwork: []hyperv1.ClusterNetworkEntry{{CIDR: *ipnet.MustParseCIDR("10.132.0.0/14")}},
		ServiceNetwork: []hyperv1.ServiceNetworkEntry{{CIDR: *ipnet.MustParseCIDR("172.31.0.0/16")}},
	}
}

func dualStackNetworking() hyperv1.ClusterNetworking {
	return hyperv1.ClusterNetworking{
		ClusterNetwork: []hyperv1.ClusterNetworkEntry{
			{CIDR: *ipnet.MustParseCIDR("10.132.0.0/14")},
			{CIDR: *ipnet.MustParseCIDR("fd01::/48")},
		},
		ServiceNetwork: []hyperv1.ServiceNetworkEntry{
			{CIDR: *ipnet.MustParseCIDR("172.31.0.0/16")},
			{CIDR: *ipnet.MustParseCIDR("fd02::/112")},
		},
	}
}

func multusPrimaryNodePool() *hyperv1.NodePool {
	return &hyperv1.NodePool{
		Spec: hyperv1.NodePoolSpec{
			Platform: hyperv1.NodePoolPlatform{
				Type: hyperv1.KubevirtPlatform,
				Kubevirt: &hyperv1.KubevirtNodePoolPlatform{
					AttachDefaultNetwork: ptr.To(false),
					AdditionalNetworks: []hyperv1.KubevirtNetwork{
						{Name: "ns1/localnet-net"},
					},
				},
			},
		},
	}
}

func TestGenerateNetworkOverrideMachineConfig(t *testing.T) {
	tests := []struct {
		name                 string
		nodePool             *hyperv1.NodePool
		networking           hyperv1.ClusterNetworking
		expectConfig         bool
		expectYAMLContent    []string
		expectDecodedContent []string
	}{
		{
			name:         "When the NodePool uses multus as primary network on a dual-stack cluster, it should generate the override MachineConfig",
			nodePool:     multusPrimaryNodePool(),
			networking:   dualStackNetworking(),
			expectConfig: true,
			expectYAMLContent: []string{
				"01-kubevirt-network",
				"001-nmstate-disable-ipv6-autoconf",
				"002-nmstate-arp-proxy-ipv6-gw",
			},
			expectDecodedContent: []string{
				"desiredState: {}",
			},
		},
		{
			name:         "When the NodePool uses multus as primary network on an IPv4-only cluster, it should not generate any config",
			nodePool:     multusPrimaryNodePool(),
			networking:   ipv4Networking(),
			expectConfig: false,
		},
		{
			name:     "When the NodePool uses multus as primary network and only the machine network has IPv6, it should generate the override MachineConfig",
			nodePool: multusPrimaryNodePool(),
			networking: hyperv1.ClusterNetworking{
				ClusterNetwork: []hyperv1.ClusterNetworkEntry{{CIDR: *ipnet.MustParseCIDR("10.132.0.0/14")}},
				ServiceNetwork: []hyperv1.ServiceNetworkEntry{{CIDR: *ipnet.MustParseCIDR("172.31.0.0/16")}},
				MachineNetwork: []hyperv1.MachineNetworkEntry{{CIDR: *ipnet.MustParseCIDR("fd03::/64")}},
			},
			expectConfig: true,
		},
		{
			name: "When the NodePool attaches the default pod network on a dual-stack cluster, it should not generate any config",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.KubevirtPlatform,
						Kubevirt: &hyperv1.KubevirtNodePoolPlatform{
							AttachDefaultNetwork: ptr.To(true),
						},
					},
				},
			},
			networking:   dualStackNetworking(),
			expectConfig: false,
		},
		{
			name: "When AttachDefaultNetwork is nil, it should default to the pod network and not generate any config",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type:     hyperv1.KubevirtPlatform,
						Kubevirt: &hyperv1.KubevirtNodePoolPlatform{},
					},
				},
			},
			networking:   dualStackNetworking(),
			expectConfig: false,
		},
		{
			name: "When the NodePool platform is not KubeVirt, it should not generate any config",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AWSPlatform,
					},
				},
			},
			networking:   dualStackNetworking(),
			expectConfig: false,
		},
		{
			name: "When the KubeVirt platform spec is nil, it should not generate any config",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.KubevirtPlatform,
					},
				},
			},
			networking:   dualStackNetworking(),
			expectConfig: false,
		},
		{
			name:         "When the NodePool is nil, it should not generate any config",
			nodePool:     nil,
			networking:   dualStackNetworking(),
			expectConfig: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := GenerateNetworkOverrideMachineConfig(tt.nodePool, tt.networking)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.expectConfig && result == "" {
				t.Fatal("expected config but got empty string")
			}
			if !tt.expectConfig && result != "" {
				t.Fatalf("expected empty string but got config: %s", result)
			}

			for _, content := range tt.expectYAMLContent {
				if !strings.Contains(result, content) {
					t.Errorf("expected YAML to contain %q, but it doesn't.\nConfig:\n%s", content, result)
				}
			}

			if len(tt.expectDecodedContent) > 0 {
				decoded := decodeIgnitionFileContents(result)
				for _, content := range tt.expectDecodedContent {
					if !strings.Contains(decoded, content) {
						t.Errorf("expected decoded content to contain %q, but it doesn't.\nDecoded:\n%s", content, decoded)
					}
				}
			}
		})
	}
}

func TestHasIPv6Network(t *testing.T) {
	tests := []struct {
		name       string
		networking hyperv1.ClusterNetworking
		expected   bool
	}{
		{
			name:       "When all networks are IPv4, it should return false",
			networking: ipv4Networking(),
			expected:   false,
		},
		{
			name:       "When the cluster and service networks are dual-stack, it should return true",
			networking: dualStackNetworking(),
			expected:   true,
		},
		{
			name: "When only the machine network has an IPv6 CIDR, it should return true",
			networking: hyperv1.ClusterNetworking{
				MachineNetwork: []hyperv1.MachineNetworkEntry{{CIDR: *ipnet.MustParseCIDR("fd03::/64")}},
			},
			expected: true,
		},
		{
			name: "When only the service network has an IPv6 CIDR, it should return true",
			networking: hyperv1.ClusterNetworking{
				ServiceNetwork: []hyperv1.ServiceNetworkEntry{{CIDR: *ipnet.MustParseCIDR("fd02::/112")}},
			},
			expected: true,
		},
		{
			name:       "When networking is empty, it should return false",
			networking: hyperv1.ClusterNetworking{},
			expected:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasIPv6Network(tt.networking); got != tt.expected {
				t.Errorf("hasIPv6Network() = %v, expected %v", got, tt.expected)
			}
		})
	}
}
