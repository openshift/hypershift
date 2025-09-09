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
	if pager.More() {
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
	securityGroupFuture, err := securityGroupClient.BeginCreateOrUpdate(ctx, resourceGroupName, name+"-"+infraID+"-nsg", armnetwork.SecurityGroup{Location: &location}, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create network security group: %w", err)
	}
	securityGroup, err := securityGroupFuture.PollUntilDone(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("failed to get network security group creation result: %w", err)
	}

	return *securityGroup.ID, nil
}

// CreateVirtualNetwork creates the virtual network
func (n *NetworkManager) CreateVirtualNetwork(ctx context.Context, resourceGroupName string, name string, infraID string, location string, subnetID string, securityGroupID string) (armnetwork.VirtualNetworksClientCreateOrUpdateResponse, error) {
	l := ctrl.LoggerFrom(ctx)

	networksClient, err := armnetwork.NewVirtualNetworksClient(n.subscriptionID, n.creds, nil)
	if err != nil {
		return armnetwork.VirtualNetworksClientCreateOrUpdateResponse{}, fmt.Errorf("failed to create new virtual networks client: %w", err)
	}

	vnetToCreate := armnetwork.VirtualNetwork{
		Location: &location,
		Properties: &armnetwork.VirtualNetworkPropertiesFormat{
			AddressSpace: &armnetwork.AddressSpace{
				AddressPrefixes: []*string{
					ptr.To(VirtualNetworkAddressPrefix),
				},
			},
			Subnets: []*armnetwork.Subnet{},
		},
	}

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

	vnetFuture, err := networksClient.BeginCreateOrUpdate(ctx, resourceGroupName, name+"-"+infraID, vnetToCreate, nil)
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

// CreatePrivateDNSZoneLink creates the private DNS Zone network link
func (n *NetworkManager) CreatePrivateDNSZoneLink(ctx context.Context, resourceGroupName string, name string, infraID string, vnetID string, privateDNSZoneName string) error {
	privateZoneLinkClient, err := armprivatedns.NewVirtualNetworkLinksClient(n.subscriptionID, n.creds, nil)
	if err != nil {
		return fmt.Errorf("failed to create new virtual network links client: %w", err)
	}

	virtualNetworkLinkParams := armprivatedns.VirtualNetworkLink{
		Location: ptr.To(VirtualNetworkLinkLocation),
		Properties: &armprivatedns.VirtualNetworkLinkProperties{
			VirtualNetwork:      &armprivatedns.SubResource{ID: &vnetID},
			RegistrationEnabled: ptr.To(false),
		},
	}
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

// CreatePublicIPAddressForLB creates a public IP address to use for the outbound rule in the load balancer
func (n *NetworkManager) CreatePublicIPAddressForLB(ctx context.Context, resourceGroupName string, infraID string, location string) (*armnetwork.PublicIPAddress, error) {
	publicIPAddressClient, err := armnetwork.NewPublicIPAddressesClient(n.subscriptionID, n.creds, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create public IP address client, %w", err)
	}

	pollerResp, err := publicIPAddressClient.BeginCreateOrUpdate(
		ctx,
		resourceGroupName,
		infraID,
		armnetwork.PublicIPAddress{
			Name:     ptr.To(infraID),
			Location: ptr.To(location),
			Properties: &armnetwork.PublicIPAddressPropertiesFormat{
				PublicIPAddressVersion:   ptr.To(armnetwork.IPVersionIPv4),
				PublicIPAllocationMethod: ptr.To(armnetwork.IPAllocationMethodStatic),
				IdleTimeoutInMinutes:     ptr.To[int32](4),
			},
			SKU: &armnetwork.PublicIPAddressSKU{
				Name: ptr.To(armnetwork.PublicIPAddressSKUNameStandard),
			},
		},
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

// CreateLoadBalancer creates a load balancer (LB) with an outbound rule for guest cluster egress; azure cloud provider will reuse this LB to add a public ip address and the load balancer rules
func (n *NetworkManager) CreateLoadBalancer(ctx context.Context, resourceGroupName string, infraID string, location string, publicIPAddress *armnetwork.PublicIPAddress) error {
	idPrefix := fmt.Sprintf("subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/loadBalancers", n.subscriptionID, resourceGroupName)
	loadBalancerName := infraID

	loadBalancerClient, err := armnetwork.NewLoadBalancersClient(n.subscriptionID, n.creds, nil)
	if err != nil {
		return fmt.Errorf("failed to create load balancer client, %w", err)
	}

	pollerResp, err := loadBalancerClient.BeginCreateOrUpdate(ctx,
		resourceGroupName,
		loadBalancerName,
		armnetwork.LoadBalancer{
			Location: ptr.To(location),
			SKU: &armnetwork.LoadBalancerSKU{
				Name: ptr.To(armnetwork.LoadBalancerSKUNameStandard),
			},
			Properties: &armnetwork.LoadBalancerPropertiesFormat{
				FrontendIPConfigurations: []*armnetwork.FrontendIPConfiguration{
					{
						Name: &infraID,
						Properties: &armnetwork.FrontendIPConfigurationPropertiesFormat{
							PrivateIPAllocationMethod: ptr.To(armnetwork.IPAllocationMethodDynamic),
							PublicIPAddress:           publicIPAddress,
						},
					},
				},
				BackendAddressPools: []*armnetwork.BackendAddressPool{
					{
						Name: &infraID,
					},
				},
				Probes: []*armnetwork.Probe{
					{
						Name: &infraID,
						Properties: &armnetwork.ProbePropertiesFormat{
							Protocol:          ptr.To(armnetwork.ProbeProtocolHTTP),
							Port:              ptr.To[int32](30595),
							IntervalInSeconds: ptr.To[int32](5),
							ProbeThreshold:    ptr.To[int32](2),
							RequestPath:       ptr.To("/healthz"),
						},
					},
				},
				// This outbound rule follows the guidance found here
				// https://learn.microsoft.com/en-us/azure/load-balancer/load-balancer-outbound-connections#outboundrules
				OutboundRules: []*armnetwork.OutboundRule{
					{
						Name: ptr.To(infraID),
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
					},
				},
			},
		}, nil)

	if err != nil {
		return fmt.Errorf("failed to create guest cluster egress load balancer: %w", err)
	}

	_, err = pollerResp.PollUntilDone(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed waiting to create guest cluster egress load balancer: %w", err)
	}
	return nil
}