package kubevirt

import (
	"net"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	mcfgutil "github.com/openshift/hypershift/hypershift-operator/controllers/nodepool/machineconfig"

	ignitionapi "github.com/coreos/ignition/v2/config/v3_2/types"
)

const (
	kubevirtNetworkMachineConfigName = "01-kubevirt-network"
	ignitionVersion                  = "3.2.0"

	// Paths must match the nmstate filenames the MCO renders for KubeVirt nodes.
	// If MCO renames or adds files, update these so the override keeps covering them.
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

	return mcfgutil.MachineConfigYAML(
		kubevirtNetworkMachineConfigName,
		ignitionapi.Config{
			Ignition: ignitionapi.Ignition{Version: ignitionVersion},
			Storage: ignitionapi.Storage{
				Files: []ignitionapi.File{
					mcfgutil.FileFromBytes(nmstateDisableIPv6AutoconfPath, 0644, noopContent),
					mcfgutil.FileFromBytes(nmstateArpProxyIPv6GwPath, 0644, noopContent),
				},
			},
		},
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
