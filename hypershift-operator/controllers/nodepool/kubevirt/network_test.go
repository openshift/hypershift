package kubevirt

import (
	"encoding/json"
	"strings"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

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

func TestGenerateNetworkMachineConfig(t *testing.T) {
	tests := []struct {
		name                 string
		nodePool             *hyperv1.NodePool
		expectConfig         bool
		expectYAMLContent    []string // checked in the YAML output (paths, names, labels)
		expectDecodedContent []string // checked in decoded base64 file content
		expectError          bool
	}{
		{
			name: "default pod network generates nmstate config",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type:     hyperv1.KubevirtPlatform,
						Kubevirt: &hyperv1.KubevirtNodePoolPlatform{
							// AttachDefaultNetwork defaults to true when nil
						},
					},
				},
			},
			expectConfig: true,
			expectYAMLContent: []string{
				"001-nmstate-disable-ipv6-autoconf",
				"002-nmstate-arp-proxy-ipv6-gw",
				"01-kubevirt-network",
				"machineconfiguration.openshift.io/role: worker",
			},
			expectDecodedContent: []string{
				"autoconf: false",
				"dhcp: true",
				"fe80::1",
			},
		},
		{
			name: "explicit AttachDefaultNetwork=true generates nmstate config",
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
			expectConfig: true,
			expectDecodedContent: []string{
				"autoconf: false",
				"fe80::1",
			},
		},
		{
			name: "multus primary network does not generate nmstate config",
			nodePool: &hyperv1.NodePool{
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
			},
			expectConfig: false,
		},
		{
			name: "non-kubevirt platform returns empty",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AWSPlatform,
					},
				},
			},
			expectConfig: false,
		},
		{
			name: "kubevirt platform with nil Kubevirt spec returns empty",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.KubevirtPlatform,
					},
				},
			},
			expectConfig: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := GenerateNetworkMachineConfig(tt.nodePool)
			if tt.expectError && err == nil {
				t.Fatal("expected error but got nil")
			}
			if !tt.expectError && err != nil {
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

func TestGenerateNetworkOverrideMachineConfig(t *testing.T) {
	tests := []struct {
		name                 string
		nodePool             *hyperv1.NodePool
		expectConfig         bool
		expectYAMLContent    []string
		expectDecodedContent []string
	}{
		{
			name: "multus primary network generates override",
			nodePool: &hyperv1.NodePool{
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
			},
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
			name: "default pod network does not generate override",
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
			expectConfig: false,
		},
		{
			name: "non-kubevirt platform returns empty",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AWSPlatform,
					},
				},
			},
			expectConfig: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := GenerateNetworkOverrideMachineConfig(tt.nodePool)
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
