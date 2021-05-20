package vpn

import (
	"bytes"
	"fmt"
	"net"

	corev1 "k8s.io/api/core/v1"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
)

const (
	vpnServerConfigKey = "server.conf"
	workerConfigKey    = "worker"
)

func ReconcileVPNServerConfig(config *corev1.ConfigMap, ownerRef config.OwnerRef, clusterCIDR, serviceCIDR, machineCIDR string) error {
	ownerRef.ApplyTo(config)
	if config.Data == nil {
		config.Data = map[string]string{}
	}
	cfg, err := generateConfig(clusterCIDR, serviceCIDR, machineCIDR)
	if err != nil {
		return fmt.Errorf("failed to generate vpn config: %w", err)
	}
	config.Data[vpnServerConfigKey] = cfg
	return nil
}

func ReconcileVPNServerClientConfig(config *corev1.ConfigMap, ownerRef config.OwnerRef, clusterCIDR, serviceCIDR, machineCIDR string) error {
	ownerRef.ApplyTo(config)
	if config.Data == nil {
		config.Data = map[string]string{}
	}
	workerCfg, err := generateWorkerClientConfig(clusterCIDR, serviceCIDR, machineCIDR)
	if err != nil {
		return fmt.Errorf("failed to generate vpn worker config: %w", err)
	}
	config.Data[workerConfigKey] = workerCfg
	return nil
}

// TODO: switch to a struct with key [values] format instead of
// opaque string block
const baseConfig = `#VPN server config
server 192.168.255.0 255.255.255.0
verb 3
ca ca.crt
cert tls.crt
key tls.key
dh none
keepalive 10 60
persist-key
persist-tun
proto tcp
port 1194
dev tun0
status /tmp/openvpn-status.log

comp-lzo no

### Push Configurations Below
push "comp-lzo no"

### Extra Configurations Below
duplicate-cn
client-to-client

### Route Configurations Below
# These will be appended dynamically
# 
# route {{ address .PodCIDR }} {{ mask .PodCIDR }}
# route {{ address .ServiceCIDR }} {{ mask .ServiceCIDR }}
# route {{ address .MachineCIDR }} {{ mask .MachineCIDR }}
# push "route {{ address .PodCIDR }} {{ mask .PodCIDR }}"
# push "route {{ address .ServiceCIDR }} {{ mask .ServiceCIDR }}"
# push "route {{ address .MachineCIDR }} {{ mask .MachineCIDR }}"
`

type routeEntry struct {
	address string
	mask    string
}

func vpnRoutes(cidrs ...string) ([]routeEntry, error) {
	result := []routeEntry{}
	for _, cidr := range cidrs {
		address, mask, err := parseCIDR(cidr)
		if err != nil {
			return nil, fmt.Errorf("error parsing CIDR %s: %w", cidr, err)
		}
		result = append(result, routeEntry{address: address, mask: mask})
	}
	return result, nil
}

func generateConfig(cidrs ...string) (string, error) {
	config := &bytes.Buffer{}
	fmt.Fprintf(config, "%s", baseConfig)
	routes, err := vpnRoutes(cidrs...)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(config, "client-config-dir %s\n", volumeMounts.Path(vpnContainerServer().Name, vpnVolumeClientConfig().Name))
	for _, entry := range routes {
		fmt.Fprintf(config, "route %s %s\n", entry.address, entry.mask)
	}
	fmt.Fprintf(config, "\n")
	for _, entry := range routes {
		fmt.Fprintf(config, "push \"route %s %s\"\n", entry.address, entry.mask)
	}
	return config.String(), nil
}

func generateWorkerClientConfig(cidrs ...string) (string, error) {
	config := &bytes.Buffer{}
	routes, err := vpnRoutes(cidrs...)
	if err != nil {
		return "", err
	}
	for _, entry := range routes {
		fmt.Fprintf(config, "iroute %s %s\n", entry.address, entry.mask)
	}
	return config.String(), nil

}

func parseCIDR(cidr string) (string, string, error) {
	ip, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", "", err
	}
	mask := network.Mask
	if len(mask) != 4 {
		return "", "", fmt.Errorf("cidr %s is not ipv4", cidr)
	}
	return ip.String(), fmt.Sprintf("%d.%d.%d.%d", mask[0], mask[1], mask[2], mask[3]), nil

}
