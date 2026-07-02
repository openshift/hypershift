package kubevirt

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/ignition"
	api "github.com/openshift/hypershift/support/api"

	mcfgv1 "github.com/openshift/api/machineconfiguration/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"github.com/clarketm/json"
	ignitionapi "github.com/coreos/ignition/v2/config/v3_2/types"
	"github.com/vincent-petithory/dataurl"
)

const (
	kubevirtNetworkMachineConfigName = "01-kubevirt-network"
	ignitionVersion                  = "3.2.0"

	nmstateDisableIPv6AutoconfPath = "/etc/nmstate/001-nmstate-disable-ipv6-autoconf.yml"
	nmstateArpProxyIPv6GwPath      = "/etc/nmstate/002-nmstate-arp-proxy-ipv6-gw.yml"

	nmstateDisableIPv6AutoconfContent = `capture:
  ethernet-nics: interfaces.type=="ethernet"
desiredState:
  interfaces:
  - name: "{{ capture.ethernet-nics.interfaces.0.name }}"
    type: ethernet
    state: up
    ipv6:
      enabled: true
      dhcp: true
      autoconf: false
`

	nmstateArpProxyIPv6GwContent = `capture:
  ethernet-nics: interfaces.type=="ethernet"
desiredState:
  interfaces:
  - name: "{{ capture.ethernet-nics.interfaces.0.name }}"
    type: "{{ capture.ethernet-nics.interfaces.0.type }}"
    ipv4: "{{ capture.ethernet-nics.interfaces.0.ipv4 }}"
    ipv6: "{{ capture.ethernet-nics.interfaces.0.ipv6 }}"
  routes:
    config:
    - destination: ::/0
      next-hop-interface: "{{ capture.ethernet-nics.interfaces.0.name }}"
      next-hop-address: fe80::1
`
)

// GenerateNetworkMachineConfig generates a serialized MachineConfig YAML string
// with KubeVirt-specific network configuration for pods using the default cluster
// network. When the NodePool uses the default pod network, it returns nmstate
// configuration that disables IPv6 autoconf (to use DHCPv6 only) and sets up
// the IPv6 gateway route via KubeVirt's ARP proxy (fe80::1).
//
// When the NodePool uses multus as the primary network (AttachDefaultNetwork=false),
// this configuration is not needed since the network should behave as a standard
// L2 network where SLAAC and normal IPv6 auto-configuration should apply.
func GenerateNetworkMachineConfig(nodePool *hyperv1.NodePool) (string, error) {
	if nodePool.Spec.Platform.Type != hyperv1.KubevirtPlatform {
		return "", nil
	}

	kvPlatform := nodePool.Spec.Platform.Kubevirt
	if kvPlatform == nil {
		return "", nil
	}

	// Only generate nmstate network configuration when using the default
	// pod network. When using multus as primary network (AttachDefaultNetwork=false)
	// these configurations should not be applied.
	if !shouldAttachDefaultNetwork(kvPlatform) {
		return "", nil
	}

	return encodeIgnitionAsMachineConfig(
		[]ignitionapi.File{
			fileFromBytes(nmstateDisableIPv6AutoconfPath, []byte(nmstateDisableIPv6AutoconfContent)),
			fileFromBytes(nmstateArpProxyIPv6GwPath, []byte(nmstateArpProxyIPv6GwContent)),
		},
		"kubevirt network",
	)
}

// fileFromBytes creates an ignition-config file with the given contents.
func fileFromBytes(path string, contents []byte) ignitionapi.File {
	mode := 0644
	return ignitionapi.File{
		Node: ignitionapi.Node{
			Path:      path,
			Overwrite: ptr.To(true),
		},
		FileEmbedded1: ignitionapi.FileEmbedded1{
			Mode: &mode,
			Contents: ignitionapi.Resource{
				Source: ptr.To(dataurl.EncodeBytes(contents)),
			},
		},
	}
}

// encodeIgnitionAsMachineConfig builds a MachineConfig containing the given ignition
// files and returns it as a YAML-encoded string. The label parameter is used in
// error messages to identify the caller context.
func encodeIgnitionAsMachineConfig(files []ignitionapi.File, label string) (string, error) {
	ignConfig := ignitionapi.Config{
		Ignition: ignitionapi.Ignition{
			Version: ignitionVersion,
		},
		Storage: ignitionapi.Storage{
			Files: files,
		},
	}

	serializedConfig, err := json.Marshal(&ignConfig)
	if err != nil {
		return "", fmt.Errorf("failed to serialize %s ignition config: %w", label, err)
	}

	mc := &mcfgv1.MachineConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name: kubevirtNetworkMachineConfigName,
		},
	}
	ignition.SetMachineConfigLabels(mc)
	mc.Spec.Config.Raw = serializedConfig

	mc.APIVersion = mcfgv1.SchemeGroupVersion.String()
	mc.Kind = "MachineConfig"

	encoded, err := api.CompatibleYAMLEncode(mc, api.YamlSerializer)
	if err != nil {
		return "", fmt.Errorf("failed to serialize %s machine config: %w", label, err)
	}

	return string(encoded), nil
}

// GenerateNetworkOverrideMachineConfig generates a MachineConfig that overrides
// the MCO-rendered nmstate files with no-op content. This is used when a KubeVirt
// NodePool uses multus as primary network but the MCO templates still produce the
// pod-network-specific nmstate configuration unconditionally.
// Once the MCO templates are cleaned up, this function can be removed.
func GenerateNetworkOverrideMachineConfig(nodePool *hyperv1.NodePool) (string, error) {
	if nodePool.Spec.Platform.Type != hyperv1.KubevirtPlatform {
		return "", nil
	}

	kvPlatform := nodePool.Spec.Platform.Kubevirt
	if kvPlatform == nil {
		return "", nil
	}

	// Only generate override when using multus (not the default pod network)
	if shouldAttachDefaultNetwork(kvPlatform) {
		return "", nil
	}

	noopContent := []byte("# Network configuration not needed for multus primary network\ndesiredState: {}\n")

	return encodeIgnitionAsMachineConfig(
		[]ignitionapi.File{
			fileFromBytes(nmstateDisableIPv6AutoconfPath, noopContent),
			fileFromBytes(nmstateArpProxyIPv6GwPath, noopContent),
		},
		"kubevirt network override",
	)
}
