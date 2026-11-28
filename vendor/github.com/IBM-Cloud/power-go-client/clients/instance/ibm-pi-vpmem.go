package instance

import (
	"context"
	"fmt"

	"github.com/IBM-Cloud/power-go-client/helpers"
	"github.com/IBM-Cloud/power-go-client/ibmpisession"
	"github.com/IBM-Cloud/power-go-client/power/client/p_cloud_v_p_mem_volumes"
	"github.com/IBM-Cloud/power-go-client/power/models"
)

// IBMPIVPMEMClient
type IBMPIVPMEMClient struct {
	IBMPIClient
}

// NewIBMPIVPMEMClient
func NewIBMPIVPMEMClient(ctx context.Context, sess *ibmpisession.IBMPISession, cloudInstanceID string) *IBMPIVPMEMClient {
	return &IBMPIVPMEMClient{
		*NewIBMPIClient(ctx, sess, cloudInstanceID),
	}
}

// PvmInstance create VPMEM volumes
func (f *IBMPIVPMEMClient) CreatePvmVpmemVolumes(pvminstanceID string, body *models.VPMemVolumeAttach) (*models.VPMemVolumes, error) {
	params := p_cloud_v_p_mem_volumes.NewPcloudPvminstancesVpmemVolumesPostParams().
		WithTimeout(helpers.PICreateTimeOut).WithCloudInstanceID(f.cloudInstanceID).WithPvmInstanceID(pvminstanceID).
		WithBody(body)
	resp, err := f.session.Power.PCloudvpMemVolumes.PcloudPvminstancesVpmemVolumesPostContext(f.ctx, params, f.session.AuthInfo(f.cloudInstanceID))
	if err != nil {
		return nil, ibmpisession.SDKFailWithAPIError(err, fmt.Errorf("failed to create VPMEM volumes for pvminstance %s in %s with %w", pvminstanceID, f.cloudInstanceID, err))
	}
	if resp == nil || resp.Payload == nil {
		return nil, fmt.Errorf("failed to create VPMEM volumes for pvminstance %s in %s", pvminstanceID, f.cloudInstanceID)
	}
	return resp.Payload, nil
}

// PvmInstance get VPMEM volume
func (f *IBMPIVPMEMClient) GetPvmVpmemVolume(pvminstanceID, vpmemVolumeID string) (*models.VPMemVolumeReference, error) {
	params := p_cloud_v_p_mem_volumes.NewPcloudPvminstancesVpmemVolumesGetParams().WithTimeout(helpers.PIGetTimeOut).
		WithCloudInstanceID(f.cloudInstanceID).WithPvmInstanceID(pvminstanceID).WithVpmemVolumeID(vpmemVolumeID)
	resp, err := f.session.Power.PCloudvpMemVolumes.PcloudPvminstancesVpmemVolumesGetContext(f.ctx, params, f.session.AuthInfo(f.cloudInstanceID))
	if err != nil {
		return nil, ibmpisession.SDKFailWithAPIError(err, fmt.Errorf("failed to get VPMEM volume %s for pvminstance %s in %s with %w", vpmemVolumeID, pvminstanceID, f.cloudInstanceID, err))
	}
	if resp == nil || resp.Payload == nil {
		return nil, fmt.Errorf("failed to get VPMEM volume %s for pvminstance %s in %s", vpmemVolumeID, pvminstanceID, f.cloudInstanceID)
	}
	return resp.Payload, nil
}

// PvmInstance delete VPMEM volume
func (f *IBMPIVPMEMClient) DeletePvmVpmemVolume(pvminstanceID, vpmemVolumeID string) error {
	params := p_cloud_v_p_mem_volumes.NewPcloudPvminstancesVpmemVolumesDeleteParams().
		WithTimeout(helpers.PIDeleteTimeOut).WithCloudInstanceID(f.cloudInstanceID).WithPvmInstanceID(pvminstanceID).WithVpmemVolumeID(vpmemVolumeID)
	_, err := f.session.Power.PCloudvpMemVolumes.PcloudPvminstancesVpmemVolumesDeleteContext(f.ctx, params, f.session.AuthInfo(f.cloudInstanceID))
	if err != nil {
		return fmt.Errorf("failed to delete VPMEM volume %s for pvminstance %s in %s :%w", vpmemVolumeID, pvminstanceID, f.cloudInstanceID, err)
	}
	return nil
}

// PvmInstance get all VPMEM volumes
func (f *IBMPIVPMEMClient) GetAllPvmVpmemVolumes(pvminstanceID string) (*models.VPMemVolumes, error) {
	params := p_cloud_v_p_mem_volumes.NewPcloudPvminstancesVpmemVolumesGetallParams().
		WithTimeout(helpers.PIGetTimeOut).WithCloudInstanceID(f.cloudInstanceID).WithPvmInstanceID(pvminstanceID)
	resp, err := f.session.Power.PCloudvpMemVolumes.PcloudPvminstancesVpmemVolumesGetallContext(f.ctx, params, f.session.AuthInfo(f.cloudInstanceID))
	if err != nil {
		return nil, ibmpisession.SDKFailWithAPIError(err, fmt.Errorf("failed to get VPMEM volumes for pvminstance %s in %s with %w", pvminstanceID, f.cloudInstanceID, err))
	}
	if resp == nil || resp.Payload == nil {
		return nil, fmt.Errorf("failed to get VPMEM volumes for pvminstance %s in %s", pvminstanceID, f.cloudInstanceID)
	}
	return resp.Payload, nil
}

// PvmInstance update VPMEM volume
func (f *IBMPIVPMEMClient) UpdatePvmVpmemVolume(pvminstanceID, vpmemVolumeID string, body *models.VPMemVolumeUpdate) error {
	params := p_cloud_v_p_mem_volumes.NewPcloudPvminstancesVpmemVolumesPutParams().WithTimeout(helpers.PIUpdateTimeOut).
		WithCloudInstanceID(f.cloudInstanceID).WithPvmInstanceID(pvminstanceID).WithVpmemVolumeID(vpmemVolumeID).WithBody(body)
	resp, err := f.session.Power.PCloudvpMemVolumes.PcloudPvminstancesVpmemVolumesPutContext(f.ctx, params, f.session.AuthInfo(f.cloudInstanceID))
	if err != nil {
		return ibmpisession.SDKFailWithAPIError(err, fmt.Errorf("failed to update VPMEM volume %s for pvminstance %s in %s with %w", vpmemVolumeID, pvminstanceID, f.cloudInstanceID, err))
	}
	if resp == nil || resp.Payload == nil {
		return ibmpisession.SDKFailWithAPIError(err, fmt.Errorf("failed to update VPMEM volume %s for pvminstance %s in %s", vpmemVolumeID, pvminstanceID, f.cloudInstanceID))
	}
	return nil
}
