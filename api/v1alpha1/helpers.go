package v1alpha1

import "github.com/openshift/hypershift/api/util/ipnet"

func (m MachineNetworkEntries) IPNets() ipnet.IPNets {
	out := ipnet.IPNets{}
	for _, mm := range m {
		out = append(out, mm.CIDR)
	}

	return out
}

func (c ClusterNetworkEntries) IPNets() ipnet.IPNets {
	out := ipnet.IPNets{}
	for _, cc := range c {
		out = append(out, cc.CIDR)
	}
	return out
}

func (s ServiceNetworkEntries) IPNets() ipnet.IPNets {
	out := ipnet.IPNets{}
	for _, ss := range s {
		out = append(out, ss.CIDR)
	}
	return out
}
