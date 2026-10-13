package azure

import (
	"testing"

	. "github.com/onsi/gomega"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v5"
)

func TestNewVirtualNetwork(t *testing.T) {
	tests := map[string]struct {
		location       string
		vnetAddrPrefix string
	}{
		"When location is eastus it should create virtual network with correct configuration": {
			location:       "eastus",
			vnetAddrPrefix: "10.0.0.0/16",
		},
		"When location is westus2 it should create virtual network with correct configuration": {
			location:       "westus2",
			vnetAddrPrefix: "192.168.0.0/16",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			vnet := NewVirtualNetwork(test.location, test.vnetAddrPrefix)

			g.Expect(vnet.Location).ToNot(BeNil())
			g.Expect(*vnet.Location).To(Equal(test.location))
			g.Expect(vnet.Properties).ToNot(BeNil())
			g.Expect(vnet.Properties.AddressSpace).ToNot(BeNil())
			g.Expect(vnet.Properties.AddressSpace.AddressPrefixes).To(HaveLen(1))
			g.Expect(*vnet.Properties.AddressSpace.AddressPrefixes[0]).To(Equal(test.vnetAddrPrefix))
			g.Expect(vnet.Properties.Subnets).ToNot(BeNil())
			g.Expect(vnet.Properties.Subnets).To(BeEmpty())
		})
	}
}

func TestNewVirtualNetworkLink(t *testing.T) {
	tests := map[string]struct {
		location            string
		vnetID              string
		registrationEnabled bool
	}{
		"When registration is enabled it should create link with registration enabled": {
			location:            "global",
			vnetID:              "/subscriptions/sub123/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet1",
			registrationEnabled: true,
		},
		"When registration is disabled it should create link with registration disabled": {
			location:            "global",
			vnetID:              "/subscriptions/sub456/resourceGroups/rg2/providers/Microsoft.Network/virtualNetworks/vnet2",
			registrationEnabled: false,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			link := NewVirtualNetworkLink(test.location, test.vnetID, test.registrationEnabled)

			g.Expect(link.Location).ToNot(BeNil())
			g.Expect(*link.Location).To(Equal(test.location))
			g.Expect(link.Properties).ToNot(BeNil())
			g.Expect(link.Properties.VirtualNetwork).ToNot(BeNil())
			g.Expect(*link.Properties.VirtualNetwork.ID).To(Equal(test.vnetID))
			g.Expect(link.Properties.RegistrationEnabled).ToNot(BeNil())
			g.Expect(*link.Properties.RegistrationEnabled).To(Equal(test.registrationEnabled))
		})
	}
}

func TestNewPublicIPAddress(t *testing.T) {
	tests := map[string]struct {
		name     string
		location string
	}{
		"When location is eastus it should create public IP with standard configuration": {
			name:     "test-infra-id",
			location: "eastus",
		},
		"When location is northeurope it should create public IP with correct region": {
			name:     "my-cluster-ip",
			location: "northeurope",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			ip := NewPublicIPAddress(test.name, test.location)

			g.Expect(ip.Name).ToNot(BeNil())
			g.Expect(*ip.Name).To(Equal(test.name))
			g.Expect(ip.Location).ToNot(BeNil())
			g.Expect(*ip.Location).To(Equal(test.location))
			g.Expect(ip.Properties).ToNot(BeNil())
			g.Expect(*ip.Properties.PublicIPAddressVersion).To(Equal(armnetwork.IPVersionIPv4))
			g.Expect(*ip.Properties.PublicIPAllocationMethod).To(Equal(armnetwork.IPAllocationMethodStatic))
			g.Expect(*ip.Properties.IdleTimeoutInMinutes).To(Equal(int32(4)))
			g.Expect(ip.SKU).ToNot(BeNil())
			g.Expect(*ip.SKU.Name).To(Equal(armnetwork.PublicIPAddressSKUNameStandard))
		})
	}
}

func TestNewLoadBalancer(t *testing.T) {
	tests := map[string]struct {
		location         string
		infraID          string
		idPrefix         string
		loadBalancerName string
	}{
		"When standard configuration is provided it should create load balancer with correct settings": {
			location:         "eastus",
			infraID:          "test-infra-id",
			idPrefix:         "subscriptions/sub123/resourceGroups/test-rg/providers/Microsoft.Network/loadBalancers",
			loadBalancerName: "test-infra-id",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			publicIP := &armnetwork.PublicIPAddress{
				ID: stringPtr("/subscriptions/sub123/resourceGroups/test-rg/providers/Microsoft.Network/publicIPAddresses/test-ip"),
			}

			lb := NewLoadBalancer(test.location, test.infraID, test.idPrefix, test.loadBalancerName, publicIP)

			g.Expect(lb.Location).ToNot(BeNil())
			g.Expect(*lb.Location).To(Equal(test.location))
			g.Expect(lb.SKU).ToNot(BeNil())
			g.Expect(*lb.SKU.Name).To(Equal(armnetwork.LoadBalancerSKUNameStandard))
			g.Expect(lb.Properties).ToNot(BeNil())

			// Check frontend IP configuration
			g.Expect(lb.Properties.FrontendIPConfigurations).To(HaveLen(1))
			g.Expect(*lb.Properties.FrontendIPConfigurations[0].Name).To(Equal(test.infraID))

			// Check backend address pool
			g.Expect(lb.Properties.BackendAddressPools).To(HaveLen(1))
			g.Expect(*lb.Properties.BackendAddressPools[0].Name).To(Equal(test.infraID))

			// Check health probe
			g.Expect(lb.Properties.Probes).To(HaveLen(1))
			g.Expect(*lb.Properties.Probes[0].Name).To(Equal(test.infraID))
			g.Expect(*lb.Properties.Probes[0].Properties.Port).To(Equal(int32(30595)))
			g.Expect(*lb.Properties.Probes[0].Properties.RequestPath).To(Equal("/healthz"))

			// Check outbound rule
			g.Expect(lb.Properties.OutboundRules).To(HaveLen(1))
			g.Expect(*lb.Properties.OutboundRules[0].Name).To(Equal(test.infraID))
		})
	}
}

func stringPtr(s string) *string {
	return &s
}
