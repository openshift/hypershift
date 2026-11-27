package instance

import (
	"context"
	"fmt"

	"github.com/IBM-Cloud/power-go-client/helpers"
	"github.com/IBM-Cloud/power-go-client/ibmpisession"
	"github.com/IBM-Cloud/power-go-client/power/client/p_cloud_p_vm_instances"
	"github.com/IBM-Cloud/power-go-client/power/models"
)

// IBMPIInstanceClient
type IBMPIInstanceClient struct {
	IBMPIClient
}

// NewIBMPIInstanceClient
func NewIBMPIInstanceClient(ctx context.Context, sess *ibmpisession.IBMPISession, cloudInstanceID string) *IBMPIInstanceClient {
	return &IBMPIInstanceClient{
		*NewIBMPIClient(ctx, sess, cloudInstanceID),
	}
}

// Get an Instance
func (f *IBMPIInstanceClient) Get(id string) (*models.PVMInstance, error) {
	params := p_cloud_p_vm_instances.NewPcloudPvminstancesGetParams().
		WithTimeout(helpers.PIGetTimeOut).
		WithCloudInstanceID(f.cloudInstanceID).WithPvmInstanceID(id)
	resp, err := f.session.Power.PCloudpVMInstances.PcloudPvminstancesGetContext(f.ctx, params, f.session.AuthInfo(f.cloudInstanceID))
	if err != nil {
		return nil, ibmpisession.SDKFailWithAPIError(err, fmt.Errorf("failed to Get PVM Instance %s :%w", id, err))
	}
	if resp == nil || resp.Payload == nil {
		return nil, fmt.Errorf("failed to Get PVM Instance %s", id)
	}
	return resp.Payload, nil
}

// Get All Instances
func (f *IBMPIInstanceClient) GetAll() (*models.PVMInstances, error) {
	params := p_cloud_p_vm_instances.NewPcloudPvminstancesGetallParams().
		WithTimeout(helpers.PIGetTimeOut).
		WithCloudInstanceID(f.cloudInstanceID)
	resp, err := f.session.Power.PCloudpVMInstances.PcloudPvminstancesGetallContext(f.ctx, params, f.session.AuthInfo(f.cloudInstanceID))
	if err != nil {
		return nil, ibmpisession.SDKFailWithAPIError(err, fmt.Errorf("failed to Get all PVM Instances of Power Instance %s :%w", f.cloudInstanceID, err))
	}
	if resp == nil || resp.Payload == nil {
		return nil, fmt.Errorf("failed to Get all PVM Instances of Power Instance %s", f.cloudInstanceID)
	}
	return resp.Payload, nil
}

// Create an Instance
func (f *IBMPIInstanceClient) Create(body *models.PVMInstanceCreate) (*models.PVMInstanceList, error) {
	// Check for satellite differences in this endpoint
	if f.session.IsOnPrem() && (body.DeploymentTarget != nil || body.DeploymentType != "") {
		return nil, fmt.Errorf("deployment target and deployment type parameters are not supported in on-prem location")
	}
	params := p_cloud_p_vm_instances.NewPcloudPvminstancesPostParams().
		WithTimeout(helpers.PICreateTimeOut).
		WithCloudInstanceID(f.cloudInstanceID).WithBody(body)
	postok, postcreated, postAccepted, err := f.session.Power.PCloudpVMInstances.PcloudPvminstancesPostContext(f.ctx, params, f.session.AuthInfo(f.cloudInstanceID))
	if err != nil {
		return nil, ibmpisession.SDKFailWithAPIError(err, fmt.Errorf("failed to Create PVM Instance :%w", err))
	}
	if postok != nil && len(postok.Payload) > 0 {
		return &postok.Payload, nil
	}
	if postcreated != nil && len(postcreated.Payload) > 0 {
		return &postcreated.Payload, nil
	}
	if postAccepted != nil && len(postAccepted.Payload) > 0 {
		return &postAccepted.Payload, nil
	}
	return nil, fmt.Errorf("failed to Create PVM Instance")
}

// Delete an Instance
func (f *IBMPIInstanceClient) Delete(id string) error {
	params := p_cloud_p_vm_instances.NewPcloudPvminstancesDeleteParams().
		WithTimeout(helpers.PIDeleteTimeOut).
		WithCloudInstanceID(f.cloudInstanceID).WithPvmInstanceID(id)
	_, err := f.session.Power.PCloudpVMInstances.PcloudPvminstancesDeleteContext(f.ctx, params, f.session.AuthInfo(f.cloudInstanceID))
	if err != nil {
		return fmt.Errorf("failed to Delete PVM Instance %s :%w", id, err)
	}
	return nil
}

// Delete an Instance with body
func (f *IBMPIInstanceClient) DeleteWithBody(id string, body *models.PVMInstanceDelete) error {
	params := p_cloud_p_vm_instances.NewPcloudPvminstancesDeleteParams().
		WithTimeout(helpers.PIDeleteTimeOut).
		WithCloudInstanceID(f.cloudInstanceID).WithPvmInstanceID(id).WithBody(body)
	_, err := f.session.Power.PCloudpVMInstances.PcloudPvminstancesDeleteContext(f.ctx, params, f.session.AuthInfo(f.cloudInstanceID))
	if err != nil {
		return fmt.Errorf("failed to Delete PVM Instance %s :%w", id, err)
	}
	return nil
}

// Update an Instance
func (f *IBMPIInstanceClient) Update(id string, body *models.PVMInstanceUpdate) (*models.PVMInstanceUpdateResponse, error) {
	// Check for satellite differences in this endpoint
	if f.session.IsOnPrem() && body.SapProfileID != "" {
		return nil, fmt.Errorf("sap profile id parameter is not supported in on-prem location")
	}
	params := p_cloud_p_vm_instances.NewPcloudPvminstancesPutParams().
		WithTimeout(helpers.PICreateTimeOut).
		WithCloudInstanceID(f.cloudInstanceID).WithPvmInstanceID(id).WithBody(body)
	resp, err := f.session.Power.PCloudpVMInstances.PcloudPvminstancesPutContext(f.ctx, params, f.session.AuthInfo(f.cloudInstanceID))
	if err != nil {
		return nil, ibmpisession.SDKFailWithAPIError(err, fmt.Errorf("failed to Update PVM Instance %s :%w", id, err))
	}
	if resp == nil || resp.Payload == nil {
		return nil, fmt.Errorf("failed to Update PVM Instance %s", id)
	}
	return resp.Payload, nil
}

// Action Operation for an Instance
func (f *IBMPIInstanceClient) Action(id string, body *models.PVMInstanceAction) error {
	params := p_cloud_p_vm_instances.NewPcloudPvminstancesActionPostParams().
		WithTimeout(helpers.PICreateTimeOut).
		WithCloudInstanceID(f.cloudInstanceID).WithPvmInstanceID(id).
		WithBody(body)
	_, err := f.session.Power.PCloudpVMInstances.PcloudPvminstancesActionPostContext(f.ctx, params, f.session.AuthInfo(f.cloudInstanceID))
	if err != nil {
		return fmt.Errorf("failed to perform Action on PVM Instance %s :%w", id, err)
	}
	return nil

}

// Generate the Console URL for an Instance
func (f *IBMPIInstanceClient) PostConsoleURL(id string) (*models.PVMInstanceConsole, error) {
	params := p_cloud_p_vm_instances.NewPcloudPvminstancesConsolePostParams().
		WithTimeout(helpers.PICreateTimeOut).
		WithCloudInstanceID(f.cloudInstanceID).WithPvmInstanceID(id)
	postok, err := f.session.Power.PCloudpVMInstances.PcloudPvminstancesConsolePostContext(f.ctx, params, f.session.AuthInfo(f.cloudInstanceID))
	if err != nil {
		return nil, ibmpisession.SDKFailWithAPIError(err, fmt.Errorf("failed to Generate the Console URL PVM Instance %s :%w", id, err))
	}
	if postok == nil || postok.Payload == nil {
		return nil, fmt.Errorf("failed to Generate the Console URL PVM Instance %s", id)
	}
	return postok.Payload, nil
}

// List the available Console Languages for an Instance
func (f *IBMPIInstanceClient) GetConsoleLanguages(id string) (*models.ConsoleLanguages, error) {
	if f.session.IsOnPrem() {
		return nil, fmt.Errorf(helpers.NotOnPremSupported)
	}
	params := p_cloud_p_vm_instances.NewPcloudPvminstancesConsoleGetParams().
		WithTimeout(helpers.PIGetTimeOut).
		WithCloudInstanceID(f.cloudInstanceID).WithPvmInstanceID(id)
	resp, err := f.session.Power.PCloudpVMInstances.PcloudPvminstancesConsoleGetContext(f.ctx, params, f.session.AuthInfo(f.cloudInstanceID))
	if err != nil {
		return nil, ibmpisession.SDKFailWithAPIError(err, fmt.Errorf("failed to Get Console Languages for PVM Instance %s :%w", id, err))
	}
	if resp == nil || resp.Payload == nil {
		return nil, fmt.Errorf("failed to Get Console Languages for PVM Instance %s", id)
	}
	return resp.Payload, nil
}

// Update the available Console Languages for an Instance
func (f *IBMPIInstanceClient) UpdateConsoleLanguage(id string, body *models.ConsoleLanguage) (*models.ConsoleLanguage, error) {
	if f.session.IsOnPrem() {
		return nil, fmt.Errorf(helpers.NotOnPremSupported)
	}
	params := p_cloud_p_vm_instances.NewPcloudPvminstancesConsolePutParams().
		WithTimeout(helpers.PIUpdateTimeOut).
		WithCloudInstanceID(f.cloudInstanceID).WithPvmInstanceID(id).
		WithBody(body)
	resp, err := f.session.Power.PCloudpVMInstances.PcloudPvminstancesConsolePutContext(f.ctx, params, f.session.AuthInfo(f.cloudInstanceID))
	if err != nil {
		return nil, ibmpisession.SDKFailWithAPIError(err, fmt.Errorf("failed to Update Console Language for PVM Instance %s :%w", id, err))
	}
	if resp == nil || resp.Payload == nil {
		return nil, fmt.Errorf("failed to Update Console Language for PVM Instance %s", id)
	}
	return resp.Payload, nil
}

// Capture an Instance
func (f *IBMPIInstanceClient) CaptureInstanceToImageCatalog(id string, body *models.PVMInstanceCapture) error {
	// Check for satellite differences in this endpoint
	if !f.session.IsOnPrem() && body.Checksum {
		return fmt.Errorf("checksum parameter is not supported in off-prem location")
	}
	params := p_cloud_p_vm_instances.NewPcloudPvminstancesCapturePostParams().
		WithTimeout(helpers.PIGetTimeOut).
		WithCloudInstanceID(f.cloudInstanceID).WithPvmInstanceID(id).
		WithBody(body)
	_, _, err := f.session.Power.PCloudpVMInstances.PcloudPvminstancesCapturePostContext(f.ctx, params, f.session.AuthInfo(f.cloudInstanceID))
	if err != nil {
		return fmt.Errorf("failed to Capture the PVM Instance %s: %w", id, err)
	}
	return nil

}

// Capture an Instance (V2)
func (f *IBMPIInstanceClient) CaptureInstanceToImageCatalogV2(id string, body *models.PVMInstanceCapture) (*models.JobReference, error) {
	params := p_cloud_p_vm_instances.NewPcloudV2PvminstancesCapturePostParams().
		WithTimeout(helpers.PIGetTimeOut).
		WithCloudInstanceID(f.cloudInstanceID).WithPvmInstanceID(id).
		WithBody(body)
	resp, err := f.session.Power.PCloudpVMInstances.PcloudV2PvminstancesCapturePostContext(f.ctx, params, f.session.AuthInfo(f.cloudInstanceID))
	if err != nil {
		return nil, ibmpisession.SDKFailWithAPIError(err, fmt.Errorf("failed to Capture the PVM Instance %s: %w", id, err))
	}
	if resp == nil || resp.Payload == nil {
		return nil, fmt.Errorf("failed to Capture the PVM Instance %s", id)
	}
	return resp.Payload, nil
}

// Create a snapshot of an Instance
func (f *IBMPIInstanceClient) CreatePvmSnapShot(id string, body *models.SnapshotCreate) (*models.SnapshotCreateResponse, error) {
	params := p_cloud_p_vm_instances.NewPcloudPvminstancesSnapshotsPostParams().
		WithTimeout(helpers.PICreateTimeOut).
		WithCloudInstanceID(f.cloudInstanceID).WithPvmInstanceID(id).
		WithBody(body)
	snapshotpostaccepted, err := f.session.Power.PCloudpVMInstances.PcloudPvminstancesSnapshotsPostContext(f.ctx, params, f.session.AuthInfo(f.cloudInstanceID))
	if err != nil {
		return nil, ibmpisession.SDKFailWithAPIError(err, fmt.Errorf("failed to Create the snapshot for the pvminstance %s: %w", id, err))
	}
	if snapshotpostaccepted == nil || snapshotpostaccepted.Payload == nil {
		return nil, fmt.Errorf("failed to Create the snapshot for the pvminstance %s", id)
	}
	return snapshotpostaccepted.Payload, nil
}

// Get an Instance's Snapshots
func (f *IBMPIInstanceClient) GetSnapShotVM(id string) (*models.Snapshots, error) {
	params := p_cloud_p_vm_instances.NewPcloudPvminstancesSnapshotsGetallParams().
		WithTimeout(helpers.PICreateTimeOut).
		WithCloudInstanceID(f.cloudInstanceID).WithPvmInstanceID(id)
	resp, err := f.session.Power.PCloudpVMInstances.PcloudPvminstancesSnapshotsGetallContext(f.ctx, params, f.session.AuthInfo(f.cloudInstanceID))
	if err != nil {
		return nil, ibmpisession.SDKFailWithAPIError(err, fmt.Errorf("failed to Get the snapshot for the pvminstance %s: %w", id, err))
	}
	if resp == nil || resp.Payload == nil {
		return nil, fmt.Errorf("failed to Get the snapshot for the pvminstance %s", id)
	}
	return resp.Payload, nil

}

// Restore a Snapshot of an Instance
func (f *IBMPIInstanceClient) RestoreSnapShotVM(id, snapshotid, restoreAction string, body *models.SnapshotRestore) (*models.Snapshot, error) {
	params := p_cloud_p_vm_instances.NewPcloudPvminstancesSnapshotsRestorePostParams().
		WithTimeout(helpers.PICreateTimeOut).
		WithCloudInstanceID(f.cloudInstanceID).WithPvmInstanceID(id).
		WithSnapshotID(snapshotid).WithRestoreFailAction(&restoreAction).
		WithBody(body)
	resp, err := f.session.Power.PCloudpVMInstances.PcloudPvminstancesSnapshotsRestorePostContext(f.ctx, params, f.session.AuthInfo(f.cloudInstanceID))
	if err != nil {
		return nil, ibmpisession.SDKFailWithAPIError(err, fmt.Errorf("failed to restrore the snapshot for the pvminstance %s: %w", id, err))
	}
	if resp == nil || resp.Payload == nil {
		return nil, fmt.Errorf("failed to restrore the snapshot for the pvminstance %s", id)
	}
	return resp.Payload, nil
}

// Add a Network to an Instance
func (f *IBMPIInstanceClient) AddNetwork(id string, body *models.PVMInstanceAddNetwork) (*models.PVMInstanceNetwork, error) {
	params := p_cloud_p_vm_instances.NewPcloudPvminstancesNetworksPostParams().
		WithTimeout(helpers.PICreateTimeOut).
		WithCloudInstanceID(f.cloudInstanceID).WithPvmInstanceID(id).
		WithBody(body)
	resp, err := f.session.Power.PCloudpVMInstances.PcloudPvminstancesNetworksPostContext(f.ctx, params, f.session.AuthInfo(f.cloudInstanceID))
	if err != nil {
		return nil, ibmpisession.SDKFailWithAPIError(err, fmt.Errorf("failed to attach the network to the pvminstanceid %s: %w", id, err))
	}
	if resp == nil || resp.Payload == nil {
		return nil, fmt.Errorf("failed to attach the network to the pvminstanceid %s", id)
	}
	return resp.Payload, nil
}

// Delete a Network from an Instance
func (f *IBMPIInstanceClient) DeleteNetwork(id, networkID string, body *models.PVMInstanceRemoveNetwork) error {
	params := p_cloud_p_vm_instances.NewPcloudPvminstancesNetworksDeleteParams().
		WithTimeout(helpers.PICreateTimeOut).
		WithCloudInstanceID(f.cloudInstanceID).WithPvmInstanceID(id).WithNetworkID(networkID).
		WithBody(body)
	_, err := f.session.Power.PCloudpVMInstances.PcloudPvminstancesNetworksDeleteContext(f.ctx, params, f.session.AuthInfo(f.cloudInstanceID))
	if err != nil {
		return fmt.Errorf("failed to delete the network to the pvminstanceid %s: %w", id, err)
	}
	return nil
}

// Shelve an Instance
func (f *IBMPIInstanceClient) Shelve(id string) error {
	params := p_cloud_p_vm_instances.NewPcloudPvminstancesShelvePostParams().
		WithTimeout(helpers.PICreateTimeOut).
		WithCloudInstanceID(f.cloudInstanceID).WithPvmInstanceID(id)
	_, err := f.session.Power.PCloudpVMInstances.PcloudPvminstancesShelvePostContext(f.ctx, params, f.session.AuthInfo(f.cloudInstanceID))
	if err != nil {
		return fmt.Errorf("failed to Shelve PVM Instance %s :%w", id, err)
	}
	return nil
}

// Unshelve an Instance
func (f *IBMPIInstanceClient) Unshelve(id string, body *models.PVMInstanceUnshelve) error {
	params := p_cloud_p_vm_instances.NewPcloudPvminstancesUnshelvePostParams().
		WithTimeout(helpers.PICreateTimeOut).
		WithCloudInstanceID(f.cloudInstanceID).WithPvmInstanceID(id).
		WithBody(body)
	resp, err := f.session.Power.PCloudpVMInstances.PcloudPvminstancesUnshelvePostContext(f.ctx, params, f.session.AuthInfo(f.cloudInstanceID))
	if err != nil {
		return ibmpisession.SDKFailWithAPIError(err, fmt.Errorf("failed to Unshelve PVM Instance %s :%w", id, err))
	}
	if resp == nil || resp.Payload == nil {
		return fmt.Errorf("failed to Unshelve PVM Instance %s", id)
	}
	return nil
}
