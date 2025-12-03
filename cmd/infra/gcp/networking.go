package gcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/go-logr/logr"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

// isNotFoundError checks if an error is a GCP 404 Not Found error.
func isNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *googleapi.Error
	if errors.As(err, &apiErr) {
		return apiErr.Code == 404
	}
	return false
}

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
	if projectID == "" {
		return nil, fmt.Errorf("projectID is required")
	}
	if infraID == "" {
		return nil, fmt.Errorf("infraID is required")
	}
	if region == "" {
		return nil, fmt.Errorf("region is required")
	}

	computeService, err := compute.NewService(ctx, option.WithScopes(compute.CloudPlatformScope))
	if err != nil {
		return nil, fmt.Errorf("failed to create compute service client: %w", err)
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

// DeleteNetwork deletes the VPC network.
func (n *NetworkManager) DeleteNetwork(ctx context.Context) error {
	networkName := n.formatNetworkName()
	n.logger.Info("Deleting VPC network", "name", networkName)

	op, err := n.computeService.Networks.Delete(n.projectID, networkName).Context(ctx).Do()
	if err != nil {
		if isNotFoundError(err) {
			n.logger.Info("VPC network not found, skipping", "name", networkName)
			return nil
		}
		return fmt.Errorf("failed to delete VPC network: %w", err)
	}

	if err := n.waitForGlobalOperation(ctx, op.Name); err != nil {
		return fmt.Errorf("failed waiting for VPC network deletion: %w", err)
	}

	n.logger.Info("Deleted VPC network", "name", networkName)
	return nil
}

// DeleteSubnet deletes the subnet.
func (n *NetworkManager) DeleteSubnet(ctx context.Context) error {
	subnetName := n.formatSubnetName()
	n.logger.Info("Deleting subnet", "name", subnetName)

	op, err := n.computeService.Subnetworks.Delete(n.projectID, n.region, subnetName).Context(ctx).Do()
	if err != nil {
		if isNotFoundError(err) {
			n.logger.Info("Subnet not found, skipping", "name", subnetName)
			return nil
		}
		return fmt.Errorf("failed to delete subnet: %w", err)
	}

	if err := n.waitForRegionOperation(ctx, op.Name); err != nil {
		return fmt.Errorf("failed waiting for subnet deletion: %w", err)
	}

	n.logger.Info("Deleted subnet", "name", subnetName)
	return nil
}

// DeleteRouter deletes the Cloud Router.
func (n *NetworkManager) DeleteRouter(ctx context.Context) error {
	routerName := n.formatRouterName()
	n.logger.Info("Deleting Cloud Router", "name", routerName)

	op, err := n.computeService.Routers.Delete(n.projectID, n.region, routerName).Context(ctx).Do()
	if err != nil {
		if isNotFoundError(err) {
			n.logger.Info("Cloud Router not found, skipping", "name", routerName)
			return nil
		}
		return fmt.Errorf("failed to delete Cloud Router: %w", err)
	}

	if err := n.waitForRegionOperation(ctx, op.Name); err != nil {
		return fmt.Errorf("failed waiting for Cloud Router deletion: %w", err)
	}

	n.logger.Info("Deleted Cloud Router", "name", routerName)
	return nil
}

// DeleteNAT deletes the Cloud NAT configuration from the router.
func (n *NetworkManager) DeleteNAT(ctx context.Context) error {
	routerName := n.formatRouterName()
	natName := n.formatNATName()
	n.logger.Info("Deleting Cloud NAT", "name", natName, "router", routerName)

	// Get the current router configuration
	router, err := n.getRouter(ctx, routerName)
	if err != nil {
		if isNotFoundError(err) {
			n.logger.Info("Cloud Router not found, skipping NAT deletion", "router", routerName)
			return nil
		}
		return fmt.Errorf("failed to get router for NAT deletion: %w", err)
	}

	// Find and remove the NAT configuration
	var updatedNats []*compute.RouterNat
	found := false
	for _, nat := range router.Nats {
		if nat.Name == natName {
			found = true
			continue
		}
		updatedNats = append(updatedNats, nat)
	}

	if !found {
		n.logger.Info("Cloud NAT not found, skipping", "name", natName)
		return nil
	}

	router.Nats = updatedNats
	router.ForceSendFields = []string{"Nats"}

	op, err := n.computeService.Routers.Patch(n.projectID, n.region, routerName, router).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("failed to delete Cloud NAT: %w", err)
	}

	if err := n.waitForRegionOperation(ctx, op.Name); err != nil {
		return fmt.Errorf("failed waiting for Cloud NAT deletion: %w", err)
	}

	n.logger.Info("Deleted Cloud NAT", "name", natName)
	return nil
}

// waitForGlobalOperation waits for a global operation until completion or timeout.
// It uses the GCP Wait API which does server-side long-polling, reducing API calls.
func (n *NetworkManager) waitForGlobalOperation(ctx context.Context, opName string) error {
	return wait.PollUntilContextTimeout(ctx, defaultPollingInterval, defaultOperationTimeout, true, func(ctx context.Context) (bool, error) {
		op, err := n.computeService.GlobalOperations.Wait(n.projectID, opName).Context(ctx).Do()
		if err != nil {
			return false, fmt.Errorf("failed to wait for operation: %w", err)
		}

		if op.Status == "DONE" {
			if op.Error != nil {
				return false, fmt.Errorf("operation failed: %s", formatOperationErrors(op.Error.Errors))
			}
			return true, nil
		}

		return false, nil
	})
}

// waitForRegionOperation waits for a regional operation until completion or timeout.
// It uses the GCP Wait API which does server-side long-polling, reducing API calls.
func (n *NetworkManager) waitForRegionOperation(ctx context.Context, opName string) error {
	return wait.PollUntilContextTimeout(ctx, defaultPollingInterval, defaultOperationTimeout, true, func(ctx context.Context) (bool, error) {
		op, err := n.computeService.RegionOperations.Wait(n.projectID, n.region, opName).Context(ctx).Do()
		if err != nil {
			return false, fmt.Errorf("failed to wait for operation: %w", err)
		}

		if op.Status == "DONE" {
			if op.Error != nil {
				return false, fmt.Errorf("operation failed: %s", formatOperationErrors(op.Error.Errors))
			}
			return true, nil
		}

		return false, nil
	})
}

// formatOperationErrors formats GCP operation errors into a readable string.
func formatOperationErrors(errors []*compute.OperationErrorErrors) string {
	if len(errors) == 0 {
		return "unknown error"
	}
	var messages []string
	for _, e := range errors {
		messages = append(messages, fmt.Sprintf("%s: %s", e.Code, e.Message))
	}
	return fmt.Sprintf("[%s]", strings.Join(messages, ", "))
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
