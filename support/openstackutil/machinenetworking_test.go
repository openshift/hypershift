package openstackutil

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/api/util/ipnet"
)

func TestGetIngressIP(t *testing.T) {
	machineNetwork := hyperv1.MachineNetworkEntry{
		CIDR: *ipnet.MustParseCIDR("10.0.0.0/16"),
	}

	expectedIP := "10.0.0.7" // Seventh IP from the CIDR range

	ip, err := GetIngressIP(machineNetwork)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ip != expectedIP {
		t.Errorf("expected IP: %s, got: %s", expectedIP, ip)
	}
}
