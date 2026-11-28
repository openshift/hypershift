package instance

import (
	"context"
	"fmt"

	"github.com/IBM-Cloud/power-go-client/helpers"
	"github.com/IBM-Cloud/power-go-client/ibmpisession"
	"github.com/IBM-Cloud/power-go-client/power/client/host_groups"
	"github.com/IBM-Cloud/power-go-client/power/models"
)

// IBMPIHostGroupsClient
type IBMPIHostGroupsClient struct {
	IBMPIClient
}

// NewIBMPIHostGroupsClient
func NewIBMPIHostGroupsClient(ctx context.Context, sess *ibmpisession.IBMPISession, cloudInstanceID string) *IBMPIHostGroupsClient {
	return &IBMPIHostGroupsClient{
		*NewIBMPIClient(ctx, sess, cloudInstanceID),
	}
}

// Get All available hosts
func (f *IBMPIHostGroupsClient) GetAvailableHosts() (models.AvailableHostList, error) {
	if f.session.IsOnPrem() {
		return nil, fmt.Errorf(helpers.NotOnPremSupported)
	}
	params := host_groups.NewV1AvailableHostsParams().WithTimeout(helpers.PIGetTimeOut)
	resp, err := f.session.Power.HostGroups.V1AvailableHostsContext(f.ctx, params, f.session.AuthInfo(f.cloudInstanceID))
	if err != nil {
		return nil, ibmpisession.SDKFailWithAPIError(err, fmt.Errorf("failed to Get available hosts for %s: %w", f.cloudInstanceID, err))
	}

	if resp == nil || resp.Payload == nil {
		return nil, fmt.Errorf("failed to get available hosts")
	}
	return resp.Payload, nil
}

// Get all host groups
func (f *IBMPIHostGroupsClient) GetHostGroups() (models.HostGroupList, error) {
	if f.session.IsOnPrem() {
		return nil, fmt.Errorf(helpers.NotOnPremSupported)
	}
	params := host_groups.NewV1HostGroupsGetParams().WithTimeout(helpers.PIGetTimeOut)
	resp, err := f.session.Power.HostGroups.V1HostGroupsGetContext(f.ctx, params, f.session.AuthInfo(f.cloudInstanceID))
	if err != nil {
		return nil, ibmpisession.SDKFailWithAPIError(err, fmt.Errorf("failed to get Host groups for %s: %w", f.cloudInstanceID, err))
	}

	if resp == nil || resp.Payload == nil {
		return nil, fmt.Errorf("failed to get host groups")
	}
	return resp.Payload, nil
}

// Create a host group
func (f *IBMPIHostGroupsClient) CreateHostGroup(body *models.HostGroupCreate) (*models.HostGroup, error) {
	if f.session.IsOnPrem() {
		return nil, fmt.Errorf(helpers.NotOnPremSupported)
	}
	params := host_groups.NewV1HostGroupsPostParams().WithTimeout(helpers.PICreateTimeOut).WithBody(body)
	resp, err := f.session.Power.HostGroups.V1HostGroupsPostContext(f.ctx, params, f.session.AuthInfo(f.cloudInstanceID))
	if err != nil {
		return nil, ibmpisession.SDKFailWithAPIError(err, fmt.Errorf("failed to create host group for %s: %w", f.cloudInstanceID, err))
	}

	if resp == nil || resp.Payload == nil {
		return nil, fmt.Errorf("failed to create host groups")
	}
	return resp.Payload, nil
}

// Update a host group
func (f *IBMPIHostGroupsClient) UpdateHostGroup(body *models.HostGroupShareOp, id string) (*models.HostGroup, error) {
	if f.session.IsOnPrem() {
		return nil, fmt.Errorf(helpers.NotOnPremSupported)
	}
	params := host_groups.NewV1HostGroupsIDPutParams().WithTimeout(helpers.PICreateTimeOut).WithBody(body).WithHostGroupID(id)
	resp, err := f.session.Power.HostGroups.V1HostGroupsIDPutContext(f.ctx, params, f.session.AuthInfo(f.cloudInstanceID))
	if err != nil {
		return nil, ibmpisession.SDKFailWithAPIError(err, fmt.Errorf("failed to update host group for %s: %w", f.cloudInstanceID, err))
	}

	if resp == nil || resp.Payload == nil {
		return nil, fmt.Errorf("failed to update host groups")
	}
	return resp.Payload, nil
}

// Get a host group
func (f *IBMPIHostGroupsClient) GetHostGroup(id string) (*models.HostGroup, error) {
	if f.session.IsOnPrem() {
		return nil, fmt.Errorf(helpers.NotOnPremSupported)
	}
	params := host_groups.NewV1HostGroupsIDGetParams().WithTimeout(helpers.PIGetTimeOut).WithHostGroupID(id)
	resp, err := f.session.Power.HostGroups.V1HostGroupsIDGetContext(f.ctx, params, f.session.AuthInfo(f.cloudInstanceID))
	if err != nil {
		return nil, ibmpisession.SDKFailWithAPIError(err, fmt.Errorf("failed to get host group %s for %s: %w", id, f.cloudInstanceID, err))
	}

	if resp == nil || resp.Payload == nil {
		return nil, fmt.Errorf("failed to get host group %s", id)
	}
	return resp.Payload, nil
}

// Get  all hosts
func (f *IBMPIHostGroupsClient) GetHosts() (models.HostList, error) {
	if f.session.IsOnPrem() {
		return nil, fmt.Errorf(helpers.NotOnPremSupported)
	}
	hostReference := true
	params := host_groups.NewV1HostsGetParams().WithTimeout(helpers.PIGetTimeOut)
	params.HostReference = &hostReference
	resp, err := f.session.Power.HostGroups.V1HostsGetContext(f.ctx, params, f.session.AuthInfo(f.cloudInstanceID))
	if err != nil {
		return nil, ibmpisession.SDKFailWithAPIError(err, fmt.Errorf("failed to get hosts for %s: %w", f.cloudInstanceID, err))
	}

	if resp == nil || resp.Payload == nil {
		return nil, fmt.Errorf("failed to get hosts")
	}
	return resp.Payload, nil
}

// Create a host
func (f *IBMPIHostGroupsClient) CreateHost(body *models.HostCreate) (models.HostList, error) {
	if f.session.IsOnPrem() {
		return nil, fmt.Errorf(helpers.NotOnPremSupported)
	}
	params := host_groups.NewV1HostsPostParams().WithTimeout(helpers.PIGetTimeOut).WithBody(body)
	resp, err := f.session.Power.HostGroups.V1HostsPostContext(f.ctx, params, f.session.AuthInfo(f.cloudInstanceID))
	if err != nil {
		return nil, ibmpisession.SDKFailWithAPIError(err, fmt.Errorf("failed to create a host for %s: %w", f.cloudInstanceID, err))
	}

	if resp == nil || resp.Payload == nil {
		return nil, fmt.Errorf("failed to create a host")
	}
	return resp.Payload, nil
}

// Get a host
func (f *IBMPIHostGroupsClient) GetHost(id string) (*models.Host, error) {
	if f.session.IsOnPrem() {
		return nil, fmt.Errorf(helpers.NotOnPremSupported)
	}
	hostReference := true
	params := host_groups.NewV1HostsIDGetParams().WithTimeout(helpers.PIGetTimeOut).WithHostID(id)
	params.HostReference = &hostReference
	resp, err := f.session.Power.HostGroups.V1HostsIDGetContext(f.ctx, params, f.session.AuthInfo(f.cloudInstanceID))
	if err != nil {
		return nil, ibmpisession.SDKFailWithAPIError(err, fmt.Errorf("failed to get host %s for %s: %w", id, f.cloudInstanceID, err))
	}

	if resp == nil || resp.Payload == nil {
		return nil, fmt.Errorf("failed to get host %s", id)
	}
	return resp.Payload, nil
}

// Update a host
func (f *IBMPIHostGroupsClient) UpdateHost(body *models.HostPut, id string) (*models.Host, error) {
	if f.session.IsOnPrem() {
		return nil, fmt.Errorf(helpers.NotOnPremSupported)
	}
	params := host_groups.NewV1HostsIDPutParams().WithTimeout(helpers.PIGetTimeOut).WithHostID(id).WithBody(body)
	resp, err := f.session.Power.HostGroups.V1HostsIDPutContext(f.ctx, params, f.session.AuthInfo(f.cloudInstanceID))
	if err != nil {
		return nil, ibmpisession.SDKFailWithAPIError(err, fmt.Errorf("failed to update host %s for %s: %w", id, f.cloudInstanceID, err))
	}

	if resp == nil || resp.Payload == nil {
		return nil, fmt.Errorf("failed to update host %s", id)
	}
	return resp.Payload, nil
}

// Delete a host
func (f *IBMPIHostGroupsClient) DeleteHost(id string) error {
	if f.session.IsOnPrem() {
		return fmt.Errorf(helpers.NotOnPremSupported)
	}
	params := host_groups.NewV1HostsIDDeleteParams().WithTimeout(helpers.PIGetTimeOut).WithHostID(id)
	resp, err := f.session.Power.HostGroups.V1HostsIDDeleteContext(f.ctx, params, f.session.AuthInfo(f.cloudInstanceID))
	if err != nil {
		return ibmpisession.SDKFailWithAPIError(err, fmt.Errorf("failed to delete host %s for %s: %w", id, f.cloudInstanceID, err))
	}

	if resp == nil || resp.Payload == nil {
		return fmt.Errorf("failed to delete host %s", id)
	}
	return nil
}
