package azure

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dns/armdns"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v5"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/privatedns/armprivatedns"

	"k8s.io/utils/ptr"

	ctrl "sigs.k8s.io/controller-runtime"
)

// NetworkManager handles Azure networking operations
type NetworkManager struct {
	subscriptionID string
	creds          azcore.TokenCredential
}

// NewNetworkManager creates a new NetworkManager
func NewNetworkManager(subscriptionID string, creds azcore.TokenCredential) *NetworkManager {
	return &NetworkManager{
		subscriptionID: subscriptionID,
		creds:          creds,
	}
}

// GetBaseDomainID gets the resource group ID for the resource group containing the base domain
func (n *NetworkManager) GetBaseDomainID(ctx context.Context, baseDomain string) (string, error) {
	zonesClient, err := armdns.NewZonesClient(n.subscriptionID, n.creds, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create dns zone %s: %w", baseDomain, err)
	}

	pager := zonesClient.NewListPager(nil)
	for pager.More() {
		pagerResults, err := pager.NextPage(ctx)
		if err != nil {
			return "", fmt.Errorf("failed to retrieve list of DNS zones: %w", err)
		}

		for _, result := range pagerResults.Value {
			if *result.Name == baseDomain {
				return *result.ID, nil
			}
		}
	}
	return "", fmt.Errorf("could not find any DNS zones in subscription")
}

// CreateSecurityGroup creates the security group the virtual network will use
func (n *NetworkManager) CreateSecurityGroup(ctx context.Context, resourceGroupName string, name string, infraID string, location string) (string, error) {
	securityGroupClient, err := armnetwork.NewSecurityGroupsClient(n.subscriptionID, n.creds, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create security group client: %w", err)
	}

	securityGroupName := name + "-" + infraID + "-nsg"
	securityGroupFuture, err := securityGroupClient.BeginCreateOrUpdate(ctx, resourceGroupName, securityGroupName, armnetwork.SecurityGroup{Location: &location}, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create network security group: %w", err)
	}
	securityGroup, err := securityGroupFuture.PollUntilDone(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("failed to get network security group creation result: %w", err)
	}

	return *securityGroup.ID, nil
}

// NewVirtualNetwork creates a VirtualNetwork struct with the given address prefix.
// It initializes an empty virtual network with the specified location and address space,
// ready to have subnets added to it.
//
// Parameters:
//   - location: Azure region where the virtual network will be created (e.g., "eastus")
//   - vnetAddrPrefix: CIDR notation for the virtual network address space (e.g., "10.0.0.0/16")
//
// Returns an armnetwork.VirtualNetwork with an empty Subnets slice that can be populated later.
func NewVirtualNetwork(location string, vnetAddrPrefix string) armnetwork.VirtualNetwork {
	return armnetwork.VirtualNetwork{
		Location: &location,
		Properties: &armnetwork.VirtualNetworkPropertiesFormat{
			AddressSpace: &armnetwork.AddressSpace{
				AddressPrefixes: []*string{
					ptr.To(vnetAddrPrefix),
				},
			},
			Subnets: []*armnetwork.Subnet{},
		},
	}
}

// CreateVirtualNetwork creates the virtual network
func (n *NetworkManager) CreateVirtualNetwork(ctx context.Context, resourceGroupName string, name string, infraID string, location string, subnetID string, securityGroupID string) (armnetwork.VirtualNetworksClientCreateOrUpdateResponse, error) {
	l := ctrl.LoggerFrom(ctx)

	networksClient, err := armnetwork.NewVirtualNetworksClient(n.subscriptionID, n.creds, nil)
	if err != nil {
		return armnetwork.VirtualNetworksClientCreateOrUpdateResponse{}, fmt.Errorf("failed to create new virtual networks client: %w", err)
	}

	vnetToCreate := NewVirtualNetwork(location, VirtualNetworkAddressPrefix)

	if len(subnetID) > 0 {
		vnetToCreate.Properties.Subnets = append(vnetToCreate.Properties.Subnets, &armnetwork.Subnet{ID: ptr.To(subnetID)})
		l.Info("Using existing subnet in vnet creation", "ID", subnetID)
	} else {
		vnetToCreate.Properties.Subnets = append(vnetToCreate.Properties.Subnets, &armnetwork.Subnet{
			Name: ptr.To("default"),
			Properties: &armnetwork.SubnetPropertiesFormat{
				AddressPrefix: ptr.To(VirtualNetworkSubnetAddressPrefix),
				NetworkSecurityGroup: &armnetwork.SecurityGroup{
					ID: ptr.To(securityGroupID),
				},
			},
		})
		l.Info("Creating new subnet for vnet creation")
	}

	vnetName := name + "-" + infraID
	vnetFuture, err := networksClient.BeginCreateOrUpdate(ctx, resourceGroupName, vnetName, vnetToCreate, nil)
	if err != nil {
		return armnetwork.VirtualNetworksClientCreateOrUpdateResponse{}, fmt.Errorf("failed to create vnet: %w", err)
	}
	vnet, err := vnetFuture.PollUntilDone(ctx, nil)
	if err != nil {
		return armnetwork.VirtualNetworksClientCreateOrUpdateResponse{}, fmt.Errorf("failed to wait for vnet creation: %w", err)
	}

	if vnet.ID == nil || vnet.Name == nil {
		return armnetwork.VirtualNetworksClientCreateOrUpdateResponse{}, fmt.Errorf("created vnet has no ID or name")
	}

	if len(vnet.Properties.Subnets) < 1 {
		return armnetwork.VirtualNetworksClientCreateOrUpdateResponse{}, fmt.Errorf("created vnet has no subnets: %+v", vnet)
	}

	if vnet.Properties.Subnets[0].ID == nil || vnet.Properties.Subnets[0].Name == nil {
		return armnetwork.VirtualNetworksClientCreateOrUpdateResponse{}, fmt.Errorf("created vnet has no subnet ID or name")
	}

	return vnet, nil
}

// CreatePrivateDNSZone creates the private DNS zone
func (n *NetworkManager) CreatePrivateDNSZone(ctx context.Context, resourceGroupName string, name string, baseDomain string) (string, string, error) {
	privateZoneClient, err := armprivatedns.NewPrivateZonesClient(n.subscriptionID, n.creds, nil)
	if err != nil {
		return "", "", fmt.Errorf("failed to create new private zones client: %w", err)
	}
	privateZoneParams := armprivatedns.PrivateZone{
		Location: ptr.To("global"),
	}
	privateDNSZonePromise, err := privateZoneClient.BeginCreateOrUpdate(ctx, resourceGroupName, name+"-azurecluster."+baseDomain, privateZoneParams, nil)
	if err != nil {
		return "", "", fmt.Errorf("failed to create private DNS zone: %w", err)
	}
	privateDNSZone, err := privateDNSZonePromise.PollUntilDone(ctx, nil)
	if err != nil {
		return "", "", fmt.Errorf("failed waiting for private DNS zone completion: %w", err)
	}

	return *privateDNSZone.ID, *privateDNSZone.Name, nil
}

// NewVirtualNetworkLink creates a VirtualNetworkLink struct for linking a VNet to a Private DNS Zone.
// This allows resources in the virtual network to resolve DNS records from the private DNS zone.
//
// Parameters:
//   - location: Azure region, typically "global" for private DNS zone links
//   - vnetID: Full resource ID of the virtual network to link (e.g., "/subscriptions/.../virtualNetworks/...")
//   - registrationEnabled: If true, enables automatic DNS record registration for VMs in the VNet
//
// Returns an armprivatedns.VirtualNetworkLink ready to be created via the Azure API.
func NewVirtualNetworkLink(location string, vnetID string, registrationEnabled bool) armprivatedns.VirtualNetworkLink {
	return armprivatedns.VirtualNetworkLink{
		Location: ptr.To(location),
		Properties: &armprivatedns.VirtualNetworkLinkProperties{
			VirtualNetwork:      &armprivatedns.SubResource{ID: ptr.To(vnetID)},
			RegistrationEnabled: ptr.To(registrationEnabled),
		},
	}
}

// CreatePrivateDNSZoneLink creates the private DNS Zone network link
func (n *NetworkManager) CreatePrivateDNSZoneLink(ctx context.Context, resourceGroupName string, name string, infraID string, vnetID string, privateDNSZoneName string) error {
	privateZoneLinkClient, err := armprivatedns.NewVirtualNetworkLinksClient(n.subscriptionID, n.creds, nil)
	if err != nil {
		return fmt.Errorf("failed to create new virtual network links client: %w", err)
	}

	virtualNetworkLinkParams := NewVirtualNetworkLink(VirtualNetworkLinkLocation, vnetID, false)
	networkLinkPromise, err := privateZoneLinkClient.BeginCreateOrUpdate(ctx, resourceGroupName, privateDNSZoneName, name+"-"+infraID, virtualNetworkLinkParams, nil)
	if err != nil {
		return fmt.Errorf("failed to set up network link for private DNS zone: %w", err)
	}
	_, err = networkLinkPromise.PollUntilDone(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed waiting for network link for private DNS zone: %w", err)
	}

	return nil
}

// NewPublicIPAddress creates a PublicIPAddress struct configured for use with a load balancer.
// The IP address is configured as a static IPv4 address with the Standard SKU, suitable for
// production load balancers that require consistent, non-changing IP addresses.
//
// Parameters:
//   - name: Name for the public IP address resource
//   - location: Azure region where the IP address will be allocated (e.g., "eastus")
//
// Returns an armnetwork.PublicIPAddress with:
//   - Static allocation method (IP doesn't change)
//   - IPv4 address version
//   - Standard SKU (required for Standard Load Balancers)
//   - 4-minute idle timeout
func NewPublicIPAddress(name string, location string) armnetwork.PublicIPAddress {
	return armnetwork.PublicIPAddress{
		Name:     ptr.To(name),
		Location: ptr.To(location),
		Properties: &armnetwork.PublicIPAddressPropertiesFormat{
			PublicIPAddressVersion:   ptr.To(armnetwork.IPVersionIPv4),
			PublicIPAllocationMethod: ptr.To(armnetwork.IPAllocationMethodStatic),
			IdleTimeoutInMinutes:     ptr.To[int32](4),
		},
		SKU: &armnetwork.PublicIPAddressSKU{
			Name: ptr.To(armnetwork.PublicIPAddressSKUNameStandard),
		},
	}
}

// CreatePublicIPAddressForLB creates a public IP address to use for the outbound rule in the load balancer
func (n *NetworkManager) CreatePublicIPAddressForLB(ctx context.Context, resourceGroupName string, infraID string, location string) (*armnetwork.PublicIPAddress, error) {
	publicIPAddressClient, err := armnetwork.NewPublicIPAddressesClient(n.subscriptionID, n.creds, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create public IP address client, %w", err)
	}

	publicIPAddress := NewPublicIPAddress(infraID, location)
	pollerResp, err := publicIPAddressClient.BeginCreateOrUpdate(
		ctx,
		resourceGroupName,
		infraID,
		publicIPAddress,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create public IP address, %w", err)
	}

	resp, err := pollerResp.PollUntilDone(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed while waiting create public IP address, %w", err)
	}
	return &resp.PublicIPAddress, nil
}

// newFrontendIPConfiguration creates a frontend IP configuration for a load balancer.
// The frontend configuration defines the public-facing IP address that clients connect to.
// It uses dynamic private IP allocation and associates with the provided public IP address.
//
// Parameters:
//   - name: Name for the frontend IP configuration
//   - publicIPAddress: The public IP address to associate with this frontend
//
// Returns a frontend IP configuration suitable for a Standard Load Balancer.
func newFrontendIPConfiguration(name string, publicIPAddress *armnetwork.PublicIPAddress) *armnetwork.FrontendIPConfiguration {
	return &armnetwork.FrontendIPConfiguration{
		Name: ptr.To(name),
		Properties: &armnetwork.FrontendIPConfigurationPropertiesFormat{
			PrivateIPAllocationMethod: ptr.To(armnetwork.IPAllocationMethodDynamic),
			PublicIPAddress:           publicIPAddress,
		},
	}
}

// newBackendAddressPool creates a backend address pool for a load balancer.
// The backend pool contains the network interfaces of VMs that will receive traffic
// distributed by the load balancer. VMs must be added to this pool to receive load-balanced traffic.
//
// Parameters:
//   - name: Name for the backend address pool
//
// Returns a backend address pool configuration.
func newBackendAddressPool(name string) *armnetwork.BackendAddressPool {
	return &armnetwork.BackendAddressPool{
		Name: ptr.To(name),
	}
}

// newHealthProbe creates a health probe for a load balancer.
// Health probes monitor the health of backend pool members by periodically sending HTTP requests.
// The load balancer only sends traffic to instances that pass the health check.
//
// Parameters:
//   - name: Name for the health probe
//   - port: TCP port to probe (e.g., 30595 for Kubernetes node health)
//   - requestPath: HTTP path to request (e.g., "/healthz")
//
// Returns a health probe configured with:
//   - HTTP protocol
//   - 5-second probe interval
//   - 2 consecutive failures required before marking unhealthy
func newHealthProbe(name string, port int32, requestPath string) *armnetwork.Probe {
	return &armnetwork.Probe{
		Name: ptr.To(name),
		Properties: &armnetwork.ProbePropertiesFormat{
			Protocol:          ptr.To(armnetwork.ProbeProtocolHTTP),
			Port:              ptr.To(port),
			IntervalInSeconds: ptr.To[int32](5),
			ProbeThreshold:    ptr.To[int32](2),
			RequestPath:       ptr.To(requestPath),
		},
	}
}

// newOutboundRule creates an outbound rule for a load balancer to enable egress connectivity.
// Outbound rules provide explicit control over SNAT (Source Network Address Translation) for backend
// pool members to reach the internet. This configuration follows Azure's recommended approach for
// managing outbound connectivity: https://learn.microsoft.com/en-us/azure/load-balancer/load-balancer-outbound-connections#outboundrules
//
// Parameters:
//   - name: Name for the outbound rule
//   - idPrefix: Azure resource ID prefix for constructing full resource references
//   - loadBalancerName: Name of the parent load balancer
//   - infraID: Infrastructure identifier used for resource naming
//
// Returns an outbound rule configured with:
//   - All protocols (TCP and UDP)
//   - 1024 allocated outbound ports per backend instance
//   - TCP reset enabled for idle connections
//   - 4-minute idle timeout
func newOutboundRule(name string, idPrefix string, loadBalancerName string, infraID string) *armnetwork.OutboundRule {
	return &armnetwork.OutboundRule{
		Name: ptr.To(name),
		Properties: &armnetwork.OutboundRulePropertiesFormat{
			BackendAddressPool: &armnetwork.SubResource{
				ID: ptr.To(fmt.Sprintf("/%s/%s/backendAddressPools/%s", idPrefix, loadBalancerName, infraID)),
			},
			FrontendIPConfigurations: []*armnetwork.SubResource{
				{
					ID: ptr.To(fmt.Sprintf("/%s/%s/frontendIPConfigurations/%s", idPrefix, loadBalancerName, infraID)),
				},
			},
			Protocol:               ptr.To(armnetwork.LoadBalancerOutboundRuleProtocolAll),
			AllocatedOutboundPorts: ptr.To[int32](1024),
			EnableTCPReset:         ptr.To(true),
			IdleTimeoutInMinutes:   ptr.To[int32](4),
		},
	}
}

// NewLoadBalancer creates a LoadBalancer struct configured for guest cluster egress traffic.
// This load balancer is used to provide outbound internet connectivity for nodes in the hosted cluster.
// The Azure cloud provider can later reuse this load balancer to add additional public IP addresses
// and load balancing rules for services of type LoadBalancer.
//
// The load balancer includes:
//   - Frontend IP configuration with a public IP address
//   - Backend address pool for guest cluster nodes
//   - Health probe for monitoring node health
//   - Outbound rule for explicit egress SNAT configuration
//
// Parameters:
//   - location: Azure region where the load balancer will be created
//   - infraID: Infrastructure identifier used for naming components
//   - idPrefix: Azure resource ID prefix for constructing component references
//   - loadBalancerName: Name for the load balancer resource
//   - publicIPAddress: Public IP address to use for the frontend configuration
//
// Returns a fully configured armnetwork.LoadBalancer with Standard SKU.
func NewLoadBalancer(location string, infraID string, idPrefix string, loadBalancerName string, publicIPAddress *armnetwork.PublicIPAddress) armnetwork.LoadBalancer {
	return armnetwork.LoadBalancer{
		Location: ptr.To(location),
		SKU: &armnetwork.LoadBalancerSKU{
			Name: ptr.To(armnetwork.LoadBalancerSKUNameStandard),
		},
		Properties: &armnetwork.LoadBalancerPropertiesFormat{
			FrontendIPConfigurations: []*armnetwork.FrontendIPConfiguration{
				newFrontendIPConfiguration(infraID, publicIPAddress),
			},
			BackendAddressPools: []*armnetwork.BackendAddressPool{
				newBackendAddressPool(infraID),
			},
			Probes: []*armnetwork.Probe{
				newHealthProbe(infraID, 30595, "/healthz"),
			},
			OutboundRules: []*armnetwork.OutboundRule{
				newOutboundRule(infraID, idPrefix, loadBalancerName, infraID),
			},
		},
	}
}

// CreateLoadBalancer creates a load balancer (LB) with an outbound rule for guest cluster egress; azure cloud provider will reuse this LB to add a public ip address and the load balancer rules
func (n *NetworkManager) CreateLoadBalancer(ctx context.Context, resourceGroupName string, infraID string, location string, publicIPAddress *armnetwork.PublicIPAddress) error {
	idPrefix := fmt.Sprintf("subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/loadBalancers", n.subscriptionID, resourceGroupName)
	loadBalancerName := infraID

	loadBalancerClient, err := armnetwork.NewLoadBalancersClient(n.subscriptionID, n.creds, nil)
	if err != nil {
		return fmt.Errorf("failed to create load balancer client, %w", err)
	}

	loadBalancer := NewLoadBalancer(location, infraID, idPrefix, loadBalancerName, publicIPAddress)
	pollerResp, err := loadBalancerClient.BeginCreateOrUpdate(ctx, resourceGroupName, loadBalancerName, loadBalancer, nil)

	if err != nil {
		return fmt.Errorf("failed to create guest cluster egress load balancer: %w", err)
	}

	_, err = pollerResp.PollUntilDone(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed waiting to create guest cluster egress load balancer: %w", err)
	}
	return nil
}
