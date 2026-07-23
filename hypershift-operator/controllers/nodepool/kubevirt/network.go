package kubevirt

import (
	"fmt"
	"net"

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
)

// GenerateNetworkOverrideMachineConfig generates a MachineConfig that overrides
// the MCO-rendered KubeVirt nmstate files with no-op content.
//
// The MCO templates unconditionally render nmstate configuration that disables
// IPv6 autoconf and routes IPv6 through KubeVirt's ARP proxy gateway (fe80::1).
// That configuration is only correct for the default pod network, where
// OVN-Kubernetes assigns IPv6 addresses via DHCPv6. When a NodePool uses multus
// as its primary network (AttachDefaultNetwork=false), the network behaves as a
// standard L2 segment and that configuration breaks SLAAC, preventing nodes from
// getting IPv6 addresses.
//
// The override is only generated when both conditions hold:
//   - The NodePool uses multus as primary network (AttachDefaultNetwork=false).
//   - The HostedCluster networking includes IPv6 (dual-stack or IPv6 primary).
//
// IPv4-only clusters are excluded on purpose: the stale nmstate files are
// asymptomatic there, and since cluster networking CIDRs are immutable those
// clusters can never become affected. Skipping them keeps this MachineConfig
// out of their NodePool config hash, avoiding a NodePool rollout when the
// HyperShift operator is upgraded.
func GenerateNetworkOverrideMachineConfig(nodePool *hyperv1.NodePool, networking hyperv1.ClusterNetworking) (string, error) {
	if nodePool == nil {
		return "", nil
	}

	if nodePool.Spec.Platform.Type != hyperv1.KubevirtPlatform {
		return "", nil
	}

	kvPlatform := nodePool.Spec.Platform.Kubevirt
	if kvPlatform == nil {
		return "", nil
	}

	// Only generate the override when using multus (not the default pod network).
	if shouldAttachDefaultNetwork(kvPlatform) {
		return "", nil
	}

	// Only generate the override when the cluster networking includes IPv6.
	if !hasIPv6Network(networking) {
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

// hasIPv6Network returns true when any of the cluster, service or machine
// networks contains an IPv6 CIDR.
func hasIPv6Network(networking hyperv1.ClusterNetworking) bool {
	for _, entry := range networking.ClusterNetwork {
		if net.IP(entry.CIDR.IP).To4() == nil {
			return true
		}
	}
	for _, entry := range networking.ServiceNetwork {
		if net.IP(entry.CIDR.IP).To4() == nil {
			return true
		}
	}
	for _, entry := range networking.MachineNetwork {
		if net.IP(entry.CIDR.IP).To4() == nil {
			return true
		}
	}
	return false
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
