/*
Copyright 2021 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1beta1

import (
	"fmt"

	"k8s.io/utils/pointer"
)

const (
	// DefaultVnetCIDR is the default Vnet CIDR.
	DefaultVnetCIDR = "10.0.0.0/8"
	// DefaultControlPlaneSubnetCIDR is the default Control Plane Subnet CIDR.
	DefaultControlPlaneSubnetCIDR = "10.0.0.0/16"
	// DefaultNodeSubnetCIDR is the default Node Subnet CIDR.
	DefaultNodeSubnetCIDR = "10.1.0.0/16"
	// DefaultNodeSubnetCIDRPattern is the pattern that will be used to generate the default subnets CIDRs.
	DefaultNodeSubnetCIDRPattern = "10.%d.0.0/16"
	// DefaultAzureBastionSubnetCIDR is the default Subnet CIDR for AzureBastion.
	DefaultAzureBastionSubnetCIDR = "10.255.255.224/27"
	// DefaultAzureBastionSubnetName is the default Subnet Name for AzureBastion.
	DefaultAzureBastionSubnetName = "AzureBastionSubnet"
	// DefaultInternalLBIPAddress is the default internal load balancer ip address.
	DefaultInternalLBIPAddress = "10.0.0.100"
	// DefaultOutboundRuleIdleTimeoutInMinutes is the default for IdleTimeoutInMinutes for the load balancer.
	DefaultOutboundRuleIdleTimeoutInMinutes = 4
	// DefaultAzureCloud is the public cloud that will be used by most users.
	DefaultAzureCloud = "AzurePublicCloud"
)

func (c *AzureCluster) setDefaults() {
	c.setResourceGroupDefault()
	c.setAzureEnvironmentDefault()
	c.setNetworkSpecDefaults()
}

func (c *AzureCluster) setNetworkSpecDefaults() {
	c.setVnetDefaults()
	c.setBastionDefaults()
	c.setSubnetDefaults()
	c.setVnetPeeringDefaults()
	c.setAPIServerLBDefaults()
	c.setNodeOutboundLBDefaults()
	c.setControlPlaneOutboundLBDefaults()
}

func (c *AzureCluster) setResourceGroupDefault() {
	if c.Spec.ResourceGroup == "" {
		c.Spec.ResourceGroup = c.Name
	}
}

func (c *AzureCluster) setAzureEnvironmentDefault() {
	if c.Spec.AzureEnvironment == "" {
		c.Spec.AzureEnvironment = DefaultAzureCloud
	}
}

func (c *AzureCluster) setVnetDefaults() {
	if c.Spec.NetworkSpec.Vnet.ResourceGroup == "" {
		c.Spec.NetworkSpec.Vnet.ResourceGroup = c.Spec.ResourceGroup
	}
	if c.Spec.NetworkSpec.Vnet.Name == "" {
		c.Spec.NetworkSpec.Vnet.Name = generateVnetName(c.ObjectMeta.Name)
	}
	if len(c.Spec.NetworkSpec.Vnet.CIDRBlocks) == 0 {
		c.Spec.NetworkSpec.Vnet.CIDRBlocks = []string{DefaultVnetCIDR}
	}
}

func (c *AzureCluster) setSubnetDefaults() {
	cpSubnet, err := c.Spec.NetworkSpec.GetControlPlaneSubnet()
	if err != nil {
		cpSubnet = SubnetSpec{Role: SubnetControlPlane}
		c.Spec.NetworkSpec.Subnets = append(c.Spec.NetworkSpec.Subnets, cpSubnet)
	}

	if cpSubnet.Name == "" {
		cpSubnet.Name = generateControlPlaneSubnetName(c.ObjectMeta.Name)
	}
	if len(cpSubnet.CIDRBlocks) == 0 {
		cpSubnet.CIDRBlocks = []string{DefaultControlPlaneSubnetCIDR}
	}
	if cpSubnet.SecurityGroup.Name == "" {
		cpSubnet.SecurityGroup.Name = generateControlPlaneSecurityGroupName(c.ObjectMeta.Name)
	}
	setSecurityRuleDefaults(&cpSubnet.SecurityGroup)

	c.Spec.NetworkSpec.UpdateControlPlaneSubnet(cpSubnet)

	var nodeSubnetFound bool
	var nodeSubnetCounter int
	for i, subnet := range c.Spec.NetworkSpec.Subnets {
		if subnet.Role == SubnetNode {
			nodeSubnetCounter++
			nodeSubnetFound = true
			if subnet.Name == "" {
				subnet.Name = withIndex(generateNodeSubnetName(c.ObjectMeta.Name), nodeSubnetCounter)
			}
			if len(subnet.CIDRBlocks) == 0 {
				subnet.CIDRBlocks = []string{fmt.Sprintf(DefaultNodeSubnetCIDRPattern, nodeSubnetCounter)}
			}
			if subnet.SecurityGroup.Name == "" {
				subnet.SecurityGroup.Name = generateNodeSecurityGroupName(c.ObjectMeta.Name)
			}
			setSecurityRuleDefaults(&subnet.SecurityGroup)
			if subnet.RouteTable.Name == "" {
				subnet.RouteTable.Name = generateNodeRouteTableName(c.ObjectMeta.Name)
			}
			if subnet.IsNatGatewayEnabled() {
				if subnet.NatGateway.NatGatewayIP.Name == "" {
					subnet.NatGateway.NatGatewayIP.Name = generateNatGatewayIPName(c.ObjectMeta.Name, subnet.Name)
				}
			}

			c.Spec.NetworkSpec.Subnets[i] = subnet
		}
	}

	if !nodeSubnetFound {
		nodeSubnet := SubnetSpec{
			Role:       SubnetNode,
			Name:       generateNodeSubnetName(c.ObjectMeta.Name),
			CIDRBlocks: []string{DefaultNodeSubnetCIDR},
			SecurityGroup: SecurityGroup{
				Name: generateNodeSecurityGroupName(c.ObjectMeta.Name),
			},
			RouteTable: RouteTable{
				Name: generateNodeRouteTableName(c.ObjectMeta.Name),
			},
		}
		c.Spec.NetworkSpec.Subnets = append(c.Spec.NetworkSpec.Subnets, nodeSubnet)
	}
}

func (c *AzureCluster) setVnetPeeringDefaults() {
	for i, peering := range c.Spec.NetworkSpec.Vnet.Peerings {
		if peering.ResourceGroup == "" {
			c.Spec.NetworkSpec.Vnet.Peerings[i].ResourceGroup = c.Spec.ResourceGroup
		}
	}
}

func setSecurityRuleDefaults(sg *SecurityGroup) {
	for i := range sg.SecurityRules {
		if sg.SecurityRules[i].Direction == "" {
			sg.SecurityRules[i].Direction = SecurityRuleDirectionInbound
		}
	}
}

func (c *AzureCluster) setAPIServerLBDefaults() {
	lb := &c.Spec.NetworkSpec.APIServerLB
	if lb.Type == "" {
		lb.Type = Public
	}
	if lb.SKU == "" {
		lb.SKU = SKUStandard
	}
	if lb.IdleTimeoutInMinutes == nil {
		lb.IdleTimeoutInMinutes = pointer.Int32Ptr(DefaultOutboundRuleIdleTimeoutInMinutes)
	}

	if lb.Type == Public {
		if lb.Name == "" {
			lb.Name = generatePublicLBName(c.ObjectMeta.Name)
		}
		if len(lb.FrontendIPs) == 0 {
			lb.FrontendIPs = []FrontendIP{
				{
					Name: generateFrontendIPConfigName(lb.Name),
					PublicIP: &PublicIPSpec{
						Name: generatePublicIPName(c.ObjectMeta.Name),
					},
				},
			}
		}
	} else if lb.Type == Internal {
		if lb.Name == "" {
			lb.Name = generateInternalLBName(c.ObjectMeta.Name)
		}
		if len(lb.FrontendIPs) == 0 {
			lb.FrontendIPs = []FrontendIP{
				{
					Name:             generateFrontendIPConfigName(lb.Name),
					PrivateIPAddress: DefaultInternalLBIPAddress,
				},
			}
		}
	}
}

func (c *AzureCluster) setNodeOutboundLBDefaults() {
	if c.Spec.NetworkSpec.NodeOutboundLB == nil {
		if c.Spec.NetworkSpec.APIServerLB.Type == Internal {
			return
		}

		var needsOutboundLB bool
		for _, subnet := range c.Spec.NetworkSpec.Subnets {
			if subnet.Role == SubnetNode && !subnet.IsNatGatewayEnabled() {
				needsOutboundLB = true
				break
			}
		}

		// If we don't default the outbound LB when there are some subnets with NAT gateway,
		// and some without, those without wouldn't have outbound traffic. So taking the
		// safer route, we configure the outbound LB in that scenario.
		if !needsOutboundLB {
			return
		}

		c.Spec.NetworkSpec.NodeOutboundLB = &LoadBalancerSpec{}
	}

	lb := c.Spec.NetworkSpec.NodeOutboundLB
	lb.Type = Public
	lb.SKU = SKUStandard
	lb.Name = c.ObjectMeta.Name

	if lb.IdleTimeoutInMinutes == nil {
		lb.IdleTimeoutInMinutes = pointer.Int32Ptr(DefaultOutboundRuleIdleTimeoutInMinutes)
	}

	if lb.FrontendIPsCount == nil {
		lb.FrontendIPsCount = pointer.Int32Ptr(1)
	}

	c.setOutboundLBFrontendIPs(lb, generateNodeOutboundIPName)
}

func (c *AzureCluster) setControlPlaneOutboundLBDefaults() {
	// public clusters don't need control plane outbound lb
	if c.Spec.NetworkSpec.APIServerLB.Type == Public {
		return
	}

	// private clusters can disable control plane outbound lb by setting it to nil.
	if c.Spec.NetworkSpec.ControlPlaneOutboundLB == nil {
		return
	}

	lb := c.Spec.NetworkSpec.ControlPlaneOutboundLB
	lb.Type = Public
	lb.SKU = SKUStandard

	if lb.Name == "" {
		lb.Name = generateControlPlaneOutboundLBName(c.ObjectMeta.Name)
	}

	if lb.IdleTimeoutInMinutes == nil {
		lb.IdleTimeoutInMinutes = pointer.Int32Ptr(DefaultOutboundRuleIdleTimeoutInMinutes)
	}

	if lb.FrontendIPsCount == nil {
		lb.FrontendIPsCount = pointer.Int32Ptr(1)
	}

	c.setOutboundLBFrontendIPs(lb, generateControlPlaneOutboundIPName)
}

// setOutboundLBFrontendIPs sets the frontend ips for the given load balancer.
// The name of the frontend ip is generated using generatePublicIPName function.
func (c *AzureCluster) setOutboundLBFrontendIPs(lb *LoadBalancerSpec, generatePublicIPName func(string) string) {
	switch *lb.FrontendIPsCount {
	case 0:
		lb.FrontendIPs = []FrontendIP{}
	case 1:
		lb.FrontendIPs = []FrontendIP{
			{
				Name: generateFrontendIPConfigName(lb.Name),
				PublicIP: &PublicIPSpec{
					Name: generatePublicIPName(c.ObjectMeta.Name),
				},
			},
		}
	default:
		lb.FrontendIPs = make([]FrontendIP, *lb.FrontendIPsCount)
		for i := 0; i < int(*lb.FrontendIPsCount); i++ {
			lb.FrontendIPs[i] = FrontendIP{
				Name: withIndex(generateFrontendIPConfigName(lb.Name), i+1),
				PublicIP: &PublicIPSpec{
					Name: withIndex(generatePublicIPName(c.ObjectMeta.Name), i+1),
				},
			}
		}
	}
}

func (c *AzureCluster) setBastionDefaults() {
	if c.Spec.BastionSpec.AzureBastion != nil {
		if c.Spec.BastionSpec.AzureBastion.Name == "" {
			c.Spec.BastionSpec.AzureBastion.Name = generateAzureBastionName(c.ObjectMeta.Name)
		}
		// Ensure defaults for the Subnet settings.
		{
			if c.Spec.BastionSpec.AzureBastion.Subnet.Name == "" {
				c.Spec.BastionSpec.AzureBastion.Subnet.Name = DefaultAzureBastionSubnetName
			}
			if len(c.Spec.BastionSpec.AzureBastion.Subnet.CIDRBlocks) == 0 {
				c.Spec.BastionSpec.AzureBastion.Subnet.CIDRBlocks = []string{DefaultAzureBastionSubnetCIDR}
			}
		}
		// Ensure defaults for the PublicIP settings.
		{
			if c.Spec.BastionSpec.AzureBastion.PublicIP.Name == "" {
				c.Spec.BastionSpec.AzureBastion.PublicIP.Name = generateAzureBastionPublicIPName(c.ObjectMeta.Name)
			}
		}
	}
}

// generateVnetName generates a virtual network name, based on the cluster name.
func generateVnetName(clusterName string) string {
	return fmt.Sprintf("%s-%s", clusterName, "vnet")
}

// generateControlPlaneSubnetName generates a node subnet name, based on the cluster name.
func generateControlPlaneSubnetName(clusterName string) string {
	return fmt.Sprintf("%s-%s", clusterName, "controlplane-subnet")
}

// generateNodeSubnetName generates a node subnet name, based on the cluster name.
func generateNodeSubnetName(clusterName string) string {
	return fmt.Sprintf("%s-%s", clusterName, "node-subnet")
}

// generateAzureBastionName generates an azure bastion name.
func generateAzureBastionName(clusterName string) string {
	return fmt.Sprintf("%s-azure-bastion", clusterName)
}

// generateAzureBastionPublicIPName generates an azure bastion public ip name.
func generateAzureBastionPublicIPName(clusterName string) string {
	return fmt.Sprintf("%s-azure-bastion-pip", clusterName)
}

// generateControlPlaneSecurityGroupName generates a control plane security group name, based on the cluster name.
func generateControlPlaneSecurityGroupName(clusterName string) string {
	return fmt.Sprintf("%s-%s", clusterName, "controlplane-nsg")
}

// generateNodeSecurityGroupName generates a node security group name, based on the cluster name.
func generateNodeSecurityGroupName(clusterName string) string {
	return fmt.Sprintf("%s-%s", clusterName, "node-nsg")
}

// generateNodeRouteTableName generates a node route table name, based on the cluster name.
func generateNodeRouteTableName(clusterName string) string {
	return fmt.Sprintf("%s-%s", clusterName, "node-routetable")
}

// generateInternalLBName generates a internal load balancer name, based on the cluster name.
func generateInternalLBName(clusterName string) string {
	return fmt.Sprintf("%s-%s", clusterName, "internal-lb")
}

// generatePublicLBName generates a public load balancer name, based on the cluster name.
func generatePublicLBName(clusterName string) string {
	return fmt.Sprintf("%s-%s", clusterName, "public-lb")
}

// generateControlPlaneOutboundLBName generates the name of the control plane outbound LB.
func generateControlPlaneOutboundLBName(clusterName string) string {
	return fmt.Sprintf("%s-outbound-lb", clusterName)
}

// generatePublicIPName generates a public IP name, based on the cluster name and a hash.
func generatePublicIPName(clusterName string) string {
	return fmt.Sprintf("pip-%s-apiserver", clusterName)
}

// generateFrontendIPConfigName generates a load balancer frontend IP config name.
func generateFrontendIPConfigName(lbName string) string {
	return fmt.Sprintf("%s-%s", lbName, "frontEnd")
}

// generateNodeOutboundIPName generates a public IP name, based on the cluster name.
func generateNodeOutboundIPName(clusterName string) string {
	return fmt.Sprintf("pip-%s-node-outbound", clusterName)
}

// generateControlPlaneOutboundIPName generates a public IP name, based on the cluster name.
func generateControlPlaneOutboundIPName(clusterName string) string {
	return fmt.Sprintf("pip-%s-controlplane-outbound", clusterName)
}

// generateNatGatewayIPName generates a NAT gateway IP name.
func generateNatGatewayIPName(clusterName, subnetName string) string {
	return fmt.Sprintf("pip-%s-%s-natgw", clusterName, subnetName)
}

// withIndex appends the index as suffix to a generated name.
func withIndex(name string, n int) string {
	return fmt.Sprintf("%s-%d", name, n)
}
