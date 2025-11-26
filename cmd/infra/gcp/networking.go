package gcp

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/option"
)

// NetworkManager encapsulates all GCP Compute API interactions for network infrastructure.
type NetworkManager struct {
	projectID      string
	infraID        string
	region         string
	computeService *compute.Service
	logger         logr.Logger
}

// NewNetworkManager creates a new NetworkManager for GCP network operations.
func NewNetworkManager(ctx context.Context, projectID, infraID, region string, logger logr.Logger) (*NetworkManager, error) {
	computeService, err := compute.NewService(ctx, option.WithScopes(compute.CloudPlatformScope))
	if err != nil {
		return nil, fmt.Errorf("failed to create compute service client: %w", err)
	}

	if projectID == "" {
		return nil, fmt.Errorf("projectID is required")
	}
	if infraID == "" {
		return nil, fmt.Errorf("infraID is required")
	}
	if region == "" {
		return nil, fmt.Errorf("region is required")
	}

	return &NetworkManager{
		projectID:      projectID,
		infraID:        infraID,
		region:         region,
		computeService: computeService,
		logger:         logger,
	}, nil
}

// CreateNetwork creates a VPC network with custom subnet mode.
func (n *NetworkManager) CreateNetwork(ctx context.Context) (*compute.Network, error) {
	networkName := n.formatNetworkName()
	n.logger.Info("Creating VPC network", "name", networkName)

	network := &compute.Network{
		Name:                  networkName,
		AutoCreateSubnetworks: false,
		Description:           fmt.Sprintf("HyperShift VPC for cluster %s", n.infraID),
		ForceSendFields:       []string{"AutoCreateSubnetworks"},
	}

	op, err := n.computeService.Networks.Insert(n.projectID, network).Context(ctx).Do()
	if err != nil {
		if isAlreadyExistsError(err) {
			n.logger.Info("Using existing VPC network", "name", networkName)
			return n.getNetwork(ctx, networkName)
		}
		return nil, fmt.Errorf("failed to create VPC network: %w", err)
	}

	if err := n.waitForGlobalOperation(ctx, op.Name); err != nil {
		return nil, fmt.Errorf("failed waiting for VPC network creation: %w", err)
	}

	n.logger.Info("Created VPC network", "name", networkName)
	return n.getNetwork(ctx, networkName)
}

// CreateSubnet creates a subnet in the specified VPC network.
func (n *NetworkManager) CreateSubnet(ctx context.Context, networkSelfLink, cidr string) (*compute.Subnetwork, error) {
	subnetName := n.formatSubnetName()
	n.logger.Info("Creating subnet", "name", subnetName, "cidr", cidr)

	subnet := &compute.Subnetwork{
		Name:                  subnetName,
		IpCidrRange:           cidr,
		Network:               networkSelfLink,
		Region:                n.region,
		PrivateIpGoogleAccess: true,
		Description:           fmt.Sprintf("HyperShift subnet for cluster %s", n.infraID),
	}

	op, err := n.computeService.Subnetworks.Insert(n.projectID, n.region, subnet).Context(ctx).Do()
	if err != nil {
		if isAlreadyExistsError(err) {
			n.logger.Info("Using existing subnet", "name", subnetName)
			return n.getSubnet(ctx, subnetName)
		}
		return nil, fmt.Errorf("failed to create subnet: %w", err)
	}

	if err := n.waitForRegionOperation(ctx, op.Name); err != nil {
		return nil, fmt.Errorf("failed waiting for subnet creation: %w", err)
	}

	n.logger.Info("Created subnet", "name", subnetName, "cidr", cidr)
	return n.getSubnet(ctx, subnetName)
}

// CreateRouter creates a Cloud Router for NAT gateway.
func (n *NetworkManager) CreateRouter(ctx context.Context, networkSelfLink string) (*compute.Router, error) {
	routerName := n.formatRouterName()
	n.logger.Info("Creating Cloud Router", "name", routerName)

	router := &compute.Router{
		Name:        routerName,
		Network:     networkSelfLink,
		Description: fmt.Sprintf("HyperShift Cloud Router for cluster %s", n.infraID),
	}

	op, err := n.computeService.Routers.Insert(n.projectID, n.region, router).Context(ctx).Do()
	if err != nil {
		if isAlreadyExistsError(err) {
			n.logger.Info("Using existing Cloud Router", "name", routerName)
			return n.getRouter(ctx, routerName)
		}
		return nil, fmt.Errorf("failed to create Cloud Router: %w", err)
	}

	if err := n.waitForRegionOperation(ctx, op.Name); err != nil {
		return nil, fmt.Errorf("failed waiting for Cloud Router creation: %w", err)
	}

	n.logger.Info("Created Cloud Router", "name", routerName)
	return n.getRouter(ctx, routerName)
}

// CreateNAT creates a Cloud NAT configuration on the specified router.
func (n *NetworkManager) CreateNAT(ctx context.Context, routerName, subnetSelfLink string) (string, error) {
	natName := n.formatNATName()
	n.logger.Info("Creating Cloud NAT", "name", natName, "router", routerName)

	// Get the current router configuration
	router, err := n.getRouter(ctx, routerName)
	if err != nil {
		return "", fmt.Errorf("failed to get router for NAT configuration: %w", err)
	}

	// Check if NAT already exists
	for _, nat := range router.Nats {
		if nat.Name == natName {
			n.logger.Info("Using existing Cloud NAT", "name", natName)
			return natName, nil
		}
	}

	// Add NAT configuration to the router
	nat := &compute.RouterNat{
		Name:                          natName,
		NatIpAllocateOption:           "AUTO_ONLY",
		SourceSubnetworkIpRangesToNat: "LIST_OF_SUBNETWORKS",
		Subnetworks: []*compute.RouterNatSubnetworkToNat{
			{
				Name:                subnetSelfLink,
				SourceIpRangesToNat: []string{"ALL_IP_RANGES"},
			},
		},
	}
	router.Nats = append(router.Nats, nat)

	op, err := n.computeService.Routers.Patch(n.projectID, n.region, routerName, router).Context(ctx).Do()
	if err != nil {
		return "", fmt.Errorf("failed to create Cloud NAT: %w", err)
	}

	if err := n.waitForRegionOperation(ctx, op.Name); err != nil {
		return "", fmt.Errorf("failed waiting for Cloud NAT creation: %w", err)
	}

	n.logger.Info("Created Cloud NAT", "name", natName)
	return natName, nil
}

// CreateEgressFirewall creates a firewall rule allowing egress traffic.
func (n *NetworkManager) CreateEgressFirewall(ctx context.Context, networkSelfLink string) (*compute.Firewall, error) {
	firewallName := n.formatFirewallName()
	n.logger.Info("Creating egress firewall rule", "name", firewallName)

	firewall := &compute.Firewall{
		Name:        firewallName,
		Network:     networkSelfLink,
		Direction:   "EGRESS",
		Priority:    1000,
		Description: fmt.Sprintf("HyperShift egress firewall for cluster %s", n.infraID),
		Allowed: []*compute.FirewallAllowed{
			{
				IPProtocol: "tcp",
				Ports:      []string{"0-65535"},
			},
			{
				IPProtocol: "udp",
				Ports:      []string{"0-65535"},
			},
		},
		DestinationRanges: []string{"0.0.0.0/0"},
	}

	op, err := n.computeService.Firewalls.Insert(n.projectID, firewall).Context(ctx).Do()
	if err != nil {
		if isAlreadyExistsError(err) {
			n.logger.Info("Using existing egress firewall rule", "name", firewallName)
			return n.getFirewall(ctx, firewallName)
		}
		return nil, fmt.Errorf("failed to create egress firewall rule: %w", err)
	}

	if err := n.waitForGlobalOperation(ctx, op.Name); err != nil {
		return nil, fmt.Errorf("failed waiting for egress firewall rule creation: %w", err)
	}

	n.logger.Info("Created egress firewall rule", "name", firewallName)
	return n.getFirewall(ctx, firewallName)
}

// getNetwork retrieves a VPC network by name.
func (n *NetworkManager) getNetwork(ctx context.Context, name string) (*compute.Network, error) {
	return n.computeService.Networks.Get(n.projectID, name).Context(ctx).Do()
}

// getSubnet retrieves a subnet by name.
func (n *NetworkManager) getSubnet(ctx context.Context, name string) (*compute.Subnetwork, error) {
	return n.computeService.Subnetworks.Get(n.projectID, n.region, name).Context(ctx).Do()
}

// getRouter retrieves a Cloud Router by name.
func (n *NetworkManager) getRouter(ctx context.Context, name string) (*compute.Router, error) {
	return n.computeService.Routers.Get(n.projectID, n.region, name).Context(ctx).Do()
}

// getFirewall retrieves a firewall rule by name.
func (n *NetworkManager) getFirewall(ctx context.Context, name string) (*compute.Firewall, error) {
	return n.computeService.Firewalls.Get(n.projectID, name).Context(ctx).Do()
}

// waitForGlobalOperation polls a global operation until completion or timeout.
func (n *NetworkManager) waitForGlobalOperation(ctx context.Context, opName string) error {
	deadline := time.Now().Add(defaultOperationTimeout)
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("operation canceled: %w", err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("operation timed out: %s", opName)
		}

		op, err := n.computeService.GlobalOperations.Get(n.projectID, opName).Context(ctx).Do()
		if err != nil {
			return fmt.Errorf("failed to get operation status: %w", err)
		}

		if op.Status == "DONE" {
			if op.Error != nil {
				return fmt.Errorf("operation failed: %v", op.Error.Errors)
			}
			return nil
		}

		time.Sleep(defaultPollingInterval)
	}
}

// waitForRegionOperation polls a regional operation until completion or timeout.
func (n *NetworkManager) waitForRegionOperation(ctx context.Context, opName string) error {
	deadline := time.Now().Add(defaultOperationTimeout)
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("operation canceled: %w", err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("operation timed out: %s", opName)
		}

		op, err := n.computeService.RegionOperations.Get(n.projectID, n.region, opName).Context(ctx).Do()
		if err != nil {
			return fmt.Errorf("failed to get operation status: %w", err)
		}

		if op.Status == "DONE" {
			if op.Error != nil {
				return fmt.Errorf("operation failed: %v", op.Error.Errors)
			}
			return nil
		}

		time.Sleep(defaultPollingInterval)
	}
}

// formatNetworkName returns the VPC network name for this infrastructure.
func (n *NetworkManager) formatNetworkName() string {
	return fmt.Sprintf("%s-network", n.infraID)
}

// formatSubnetName returns the subnet name for this infrastructure.
func (n *NetworkManager) formatSubnetName() string {
	return fmt.Sprintf("%s-subnet", n.infraID)
}

// formatRouterName returns the Cloud Router name for this infrastructure.
func (n *NetworkManager) formatRouterName() string {
	return fmt.Sprintf("%s-router", n.infraID)
}

// formatNATName returns the Cloud NAT name for this infrastructure.
func (n *NetworkManager) formatNATName() string {
	return fmt.Sprintf("%s-nat", n.infraID)
}

// formatFirewallName returns the egress firewall rule name for this infrastructure.
func (n *NetworkManager) formatFirewallName() string {
	return fmt.Sprintf("%s-egress-allow", n.infraID)
}
