package openstackutil

import (
	"net"

	"github.com/apparentlymart/go-cidr/cidr"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

const (
	DefaultCIDRBlock = "10.0.0.0/16"
)

// GetIngressIP returns the IP address to be used for the Ingress VIP.
// We take the seventh IP from the CIDR range like we do in the installer.
// https://github.com/openshift/installer/blob/8e548c31b0431419350edd1fabd4dcb06263440f/pkg/types/openstack/defaults/platform.go#L48
func GetIngressIP(machineNetwork hyperv1.MachineNetworkEntry) (string, error) {
	// go-cidr expects a net.IPNet, so we need to convert the hypershift type of CIDR to net.IPNet
	machineNetworkIPNet := &net.IPNet{
		IP:   machineNetwork.CIDR.IP,
		Mask: machineNetwork.CIDR.Mask,
	}
	ip, err := cidr.Host(machineNetworkIPNet, 7)
	if err != nil {
		return "", err
	}
	return ip.String(), nil
}
