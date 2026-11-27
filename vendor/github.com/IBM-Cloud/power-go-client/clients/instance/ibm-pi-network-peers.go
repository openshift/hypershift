package instance

import (
	"context"
	"fmt"

	"github.com/IBM-Cloud/power-go-client/helpers"
	"github.com/IBM-Cloud/power-go-client/ibmpisession"
	"github.com/IBM-Cloud/power-go-client/power/client/network_peers"
	"github.com/IBM-Cloud/power-go-client/power/models"
)

// IBMPINetworkPeerClient
type IBMPINetworkPeerClient struct {
	IBMPIClient
}

// NewIBMPINetworkPeerClient
func NewIBMPINetworkPeerClient(ctx context.Context, sess *ibmpisession.IBMPISession, cloudInstanceID string) *IBMPINetworkPeerClient {
	return &IBMPINetworkPeerClient{
		*NewIBMPIClient(ctx, sess, cloudInstanceID),
	}
}

// Get network peers
func (f *IBMPINetworkPeerClient) GetNetworkPeers() (*models.NetworkPeers, error) {
	if !f.session.IsOnPrem() {
		return nil, fmt.Errorf(helpers.NotOffPremSupported)
	}
	params := network_peers.NewV1NetworkPeersGetallParams().
		WithTimeout(helpers.PIGetTimeOut)
	resp, err := f.session.Power.NetworkPeers.V1NetworkPeersGetallContext(f.ctx, params, f.session.AuthInfo(f.cloudInstanceID))
	if err != nil {
		return nil, ibmpisession.SDKFailWithAPIError(err, fmt.Errorf("failed to Get network peers for cloud instance %s with error %w", f.cloudInstanceID, err))
	}
	if resp == nil || resp.Payload == nil {
		return nil, fmt.Errorf("failed to Get network peers for cloud instance %s", f.cloudInstanceID)
	}
	return resp.Payload, nil
}

// Delete a network peer
func (f *IBMPINetworkPeerClient) DeleteNetworkPeer(id string) error {
	if !f.session.IsOnPrem() {
		return fmt.Errorf(helpers.NotOffPremSupported)
	}
	params := network_peers.NewV1NetworkPeersIDDeleteParams().WithTimeout(helpers.PIDeleteTimeOut).WithNetworkPeerID(id)
	_, err := f.session.Power.NetworkPeers.V1NetworkPeersIDDeleteContext(f.ctx, params, f.session.AuthInfo(f.cloudInstanceID))
	if err != nil {
		return ibmpisession.SDKFailWithAPIError(err, fmt.Errorf("failed to Delete network peer %s for cloud instance %s with error %w", id, f.cloudInstanceID, err))
	}
	return nil

}

// Get a network peer
func (f *IBMPINetworkPeerClient) GetNetworkPeer(id string) (*models.NetworkPeer, error) {
	if !f.session.IsOnPrem() {
		return nil, fmt.Errorf(helpers.NotOffPremSupported)
	}
	params := network_peers.NewV1NetworkPeersIDGetParams().WithTimeout(helpers.PIDeleteTimeOut).WithNetworkPeerID(id)
	resp, err := f.session.Power.NetworkPeers.V1NetworkPeersIDGetContext(f.ctx, params, f.session.AuthInfo(f.cloudInstanceID))
	if err != nil {
		return nil, ibmpisession.SDKFailWithAPIError(err, fmt.Errorf("failed to Get network peer %s for cloud instance %s with error %w", id, f.cloudInstanceID, err))
	}
	if resp == nil || resp.Payload == nil {
		return nil, fmt.Errorf("failed to Get network peer %s for cloud instance %s", id, f.cloudInstanceID)
	}
	return resp.Payload, nil

}

// Update a network peer
func (f *IBMPINetworkPeerClient) UpdateNetworkPeer(id string, body *models.NetworkPeerUpdate) (*models.NetworkPeer, error) {
	if !f.session.IsOnPrem() {
		return nil, fmt.Errorf(helpers.NotOffPremSupported)
	}
	params := network_peers.NewV1NetworkPeersIDPutParams().WithTimeout(helpers.PIUpdateTimeOut).WithNetworkPeerID(id).WithBody(body)
	resp, err := f.session.Power.NetworkPeers.V1NetworkPeersIDPutContext(f.ctx, params, f.session.AuthInfo(f.cloudInstanceID))
	if err != nil {
		return nil, ibmpisession.SDKFailWithAPIError(err, fmt.Errorf("failed to Update network peer %s for cloud instance %s with error %w", id, f.cloudInstanceID, err))
	}
	if resp == nil || resp.Payload == nil {
		return nil, fmt.Errorf("failed to Update network peer %s", id)
	}
	return resp.Payload, nil
}

// Get all network peers interfaces
func (f *IBMPINetworkPeerClient) GetAllNetworkPeersInterfaces() (models.PeerInterfaces, error) {
	if !f.session.IsOnPrem() {
		return nil, fmt.Errorf(helpers.NotOffPremSupported)
	}
	params := network_peers.NewV1NetworkPeersInterfacesGetallParams().WithTimeout(helpers.PIGetTimeOut)
	resp, err := f.session.Power.NetworkPeers.V1NetworkPeersInterfacesGetallContext(f.ctx, params, f.session.AuthInfo(f.cloudInstanceID))
	if err != nil {
		return nil, ibmpisession.SDKFailWithAPIError(err, fmt.Errorf("failed to Get network peers interfaces for cloud instance %s with error %w", f.cloudInstanceID, err))
	}
	if resp == nil || resp.Payload == nil {
		return nil, fmt.Errorf("failed to Get network peers interfaces for cloud instance %s", f.cloudInstanceID)
	}
	return resp.Payload, nil
}

// Create a network peer
func (f *IBMPINetworkPeerClient) CreateNetworkPeer(body *models.NetworkPeerCreate) (*models.NetworkPeer, error) {
	if !f.session.IsOnPrem() {
		return nil, fmt.Errorf(helpers.NotOffPremSupported)
	}
	params := network_peers.NewV1NetworkPeersPostParams().WithTimeout(helpers.PICreateTimeOut).WithBody(body)
	resp, err := f.session.Power.NetworkPeers.V1NetworkPeersPostContext(f.ctx, params, f.session.AuthInfo(f.cloudInstanceID))
	if err != nil {
		return nil, ibmpisession.SDKFailWithAPIError(err, fmt.Errorf("failed to Create network peer for cloud instance %s with error %w", f.cloudInstanceID, err))
	}
	if resp == nil || resp.Payload == nil {
		return nil, fmt.Errorf("failed to Create network peer for cloud instance %s", f.cloudInstanceID)
	}
	return resp.Payload, nil
}

// Delete network peer's route filter
func (f *IBMPINetworkPeerClient) DeleteNetworkPeersRouteFilter(id, routeFilterID string) error {
	if !f.session.IsOnPrem() {
		return fmt.Errorf(helpers.NotOffPremSupported)
	}
	params := network_peers.NewV1NetworkPeersRouteFilterIDDeleteParams().WithTimeout(helpers.PIDeleteTimeOut).WithNetworkPeerID(id).WithRouteFilterID(routeFilterID)
	_, err := f.session.Power.NetworkPeers.V1NetworkPeersRouteFilterIDDeleteContext(f.ctx, params, f.session.AuthInfo(f.cloudInstanceID))
	if err != nil {
		return ibmpisession.SDKFailWithAPIError(err, fmt.Errorf("failed to Delete network peer's %s route filter %s for cloud instance %s with error %w", id, routeFilterID, f.cloudInstanceID, err))
	}
	return nil
}

// Get network peer's route filter
func (f *IBMPINetworkPeerClient) GetNetworkPeersRouteFilter(id, routeFilterID string) (*models.RouteFilter, error) {
	if !f.session.IsOnPrem() {
		return nil, fmt.Errorf(helpers.NotOffPremSupported)
	}
	params := network_peers.NewV1NetworkPeersRouteFilterIDGetParams().WithTimeout(helpers.PIGetTimeOut).WithNetworkPeerID(id).WithRouteFilterID(routeFilterID)
	resp, err := f.session.Power.NetworkPeers.V1NetworkPeersRouteFilterIDGetContext(f.ctx, params, f.session.AuthInfo(f.cloudInstanceID))
	if err != nil {
		return nil, ibmpisession.SDKFailWithAPIError(err, fmt.Errorf("failed to Get network peer's %s route filter %s for cloud instance %s with error %w", id, routeFilterID, f.cloudInstanceID, err))
	}
	if resp == nil || resp.Payload == nil {
		return nil, fmt.Errorf("failed to Get network peer's %s route filter %s for cloud instance %s", id, routeFilterID, f.cloudInstanceID)
	}
	return resp.Payload, nil
}

// Create network peer's route filter
func (f *IBMPINetworkPeerClient) CreateNetworkPeersRouteFilters(id string, body *models.RouteFilterCreate) (*models.RouteFilter, error) {
	if !f.session.IsOnPrem() {
		return nil, fmt.Errorf(helpers.NotOffPremSupported)
	}
	params := network_peers.NewV1NetworkPeersRouteFiltersPostParams().WithTimeout(helpers.PICreateTimeOut).WithNetworkPeerID(id).WithBody(body)
	resp, err := f.session.Power.NetworkPeers.V1NetworkPeersRouteFiltersPostContext(f.ctx, params, f.session.AuthInfo(f.cloudInstanceID))
	if err != nil {
		return nil, ibmpisession.SDKFailWithAPIError(err, fmt.Errorf("failed to Create network peer's %s route filter for cloud instance %s with error %w", id, f.cloudInstanceID, err))
	}
	if resp == nil || resp.Payload == nil {
		return nil, fmt.Errorf("failed to Create network peer's %s route filter for cloud instance %s", id, f.cloudInstanceID)
	}
	return resp.Payload, nil
}
