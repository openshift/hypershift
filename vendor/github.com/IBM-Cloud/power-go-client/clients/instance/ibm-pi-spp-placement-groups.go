package instance

import (
	"context"
	"fmt"

	"github.com/IBM-Cloud/power-go-client/errors"
	"github.com/IBM-Cloud/power-go-client/helpers"
	"github.com/IBM-Cloud/power-go-client/ibmpisession"
	"github.com/IBM-Cloud/power-go-client/power/client/p_cloud_s_p_p_placement_groups"
	"github.com/IBM-Cloud/power-go-client/power/models"
)

// IBMPISPPPlacementGroupClient
type IBMPISPPPlacementGroupClient struct {
	IBMPIClient
}

// NewIBMPISPPPlacementGroupClient
func NewIBMPISPPPlacementGroupClient(ctx context.Context, sess *ibmpisession.IBMPISession, cloudInstanceID string) *IBMPISPPPlacementGroupClient {
	return &IBMPISPPPlacementGroupClient{
		*NewIBMPIClient(ctx, sess, cloudInstanceID),
	}
}

// Get a PI SPP Placement Group
func (f *IBMPISPPPlacementGroupClient) Get(id string) (*models.SPPPlacementGroup, error) {
	params := p_cloud_s_p_p_placement_groups.NewPcloudSppplacementgroupsGetParams().
		WithTimeout(helpers.PIGetTimeOut).
		WithCloudInstanceID(f.cloudInstanceID).WithSppPlacementGroupID(id)
	resp, err := f.session.Power.PCloudsppPlacementGroups.PcloudSppplacementgroupsGetContext(f.ctx, params, f.session.AuthInfo(f.cloudInstanceID))
	if err != nil {
		return nil, ibmpisession.SDKFailWithAPIError(err, fmt.Errorf(errors.GetSPPPlacementGroupOperationFailed, id, err))
	}
	if resp == nil || resp.Payload == nil {
		return nil, fmt.Errorf("failed to Get Shared Processor Pool Placement Group %s", id)
	}
	return resp.Payload, nil
}

// Get All SPP Placement Groups
func (f *IBMPISPPPlacementGroupClient) GetAll() (*models.SPPPlacementGroups, error) {
	params := p_cloud_s_p_p_placement_groups.NewPcloudSppplacementgroupsGetallParams().
		WithTimeout(helpers.PIGetTimeOut).
		WithCloudInstanceID(f.cloudInstanceID)
	resp, err := f.session.Power.PCloudsppPlacementGroups.PcloudSppplacementgroupsGetallContext(f.ctx, params, f.session.AuthInfo(f.cloudInstanceID))
	if err != nil {
		return nil, ibmpisession.SDKFailWithAPIError(err, fmt.Errorf("failed to Get All Shared Processor Pool Placement Groups: %w", err))
	}
	if resp == nil || resp.Payload == nil {
		return nil, fmt.Errorf("failed to Get all Shared Processor Pool Placement Groups")
	}
	return resp.Payload, nil
}

// Create a SPP Placement Group
func (f *IBMPISPPPlacementGroupClient) Create(body *models.SPPPlacementGroupCreate) (*models.SPPPlacementGroup, error) {
	params := p_cloud_s_p_p_placement_groups.NewPcloudSppplacementgroupsPostParams().
		WithTimeout(helpers.PICreateTimeOut).
		WithCloudInstanceID(f.cloudInstanceID).WithBody(body)
	postok, err := f.session.Power.PCloudsppPlacementGroups.PcloudSppplacementgroupsPostContext(f.ctx, params, f.session.AuthInfo(f.cloudInstanceID))
	if err != nil {
		return nil, ibmpisession.SDKFailWithAPIError(err, fmt.Errorf(errors.CreateSPPPlacementGroupOperationFailed, f.cloudInstanceID, err))
	}
	if postok == nil || postok.Payload == nil {
		return nil, fmt.Errorf("failed to Create Shared Processor Pool Placement Group")
	}
	return postok.Payload, nil
}

// Delete a SPP Placement Group
func (f *IBMPISPPPlacementGroupClient) Delete(id string) error {
	params := p_cloud_s_p_p_placement_groups.NewPcloudSppplacementgroupsDeleteParams().
		WithTimeout(helpers.PIDeleteTimeOut).
		WithCloudInstanceID(f.cloudInstanceID).WithSppPlacementGroupID(id)
	_, err := f.session.Power.PCloudsppPlacementGroups.PcloudSppplacementgroupsDeleteContext(f.ctx, params, f.session.AuthInfo(f.cloudInstanceID))
	if err != nil {
		return fmt.Errorf(errors.DeleteSPPPlacementGroupOperationFailed, id, err)
	}
	return nil
}

// Add an Instance to a SPP Placement Group
func (f *IBMPISPPPlacementGroupClient) AddMember(id string, sppID string) (*models.SPPPlacementGroup, error) {
	params := p_cloud_s_p_p_placement_groups.NewPcloudSppplacementgroupsMembersPostParams().
		WithTimeout(helpers.PICreateTimeOut).
		WithCloudInstanceID(f.cloudInstanceID).WithSppPlacementGroupID(id).
		WithSharedProcessorPoolID(sppID)
	postok, err := f.session.Power.PCloudsppPlacementGroups.PcloudSppplacementgroupsMembersPostContext(f.ctx, params, f.session.AuthInfo(f.cloudInstanceID))
	if err != nil {
		return nil, ibmpisession.SDKFailWithAPIError(err, fmt.Errorf(errors.AddMemberSPPPlacementGroupOperationFailed, sppID, id, err))
	}
	if postok == nil || postok.Payload == nil {
		return nil, fmt.Errorf("failed to Add Member for pool %s and shared processor pool placement group %s", sppID, id)
	}
	return postok.Payload, nil
}

// Remove an Instance to a SPP Placement Group
func (f *IBMPISPPPlacementGroupClient) DeleteMember(id string, sppID string) (*models.SPPPlacementGroup, error) {
	params := p_cloud_s_p_p_placement_groups.NewPcloudSppplacementgroupsMembersDeleteParams().
		WithTimeout(helpers.PIDeleteTimeOut).
		WithCloudInstanceID(f.cloudInstanceID).WithSppPlacementGroupID(id).
		WithSharedProcessorPoolID(sppID)
	delok, err := f.session.Power.PCloudsppPlacementGroups.PcloudSppplacementgroupsMembersDeleteContext(f.ctx, params, f.session.AuthInfo(f.cloudInstanceID))
	if err != nil {
		return nil, ibmpisession.SDKFailWithAPIError(err, fmt.Errorf(errors.DeleteMemberSPPPlacementGroupOperationFailed, sppID, id, err))
	}
	if delok == nil || delok.Payload == nil {
		return nil, fmt.Errorf("failed to Delete Member for pool %s and  shared processor pool placement group %s", sppID, id)
	}
	return delok.Payload, nil
}
