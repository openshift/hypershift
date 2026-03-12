package v1beta1

import (
	awskarpenterv1 "github.com/aws/karpenter-provider-aws/pkg/apis/v1"

	"k8s.io/utils/ptr"
)

func (spec OpenshiftEC2NodeClassSpec) KarpenterBlockDeviceMapping() []*awskarpenterv1.BlockDeviceMapping {
	if spec.BlockDeviceMappings == nil {
		return nil
	}
	var blockDeviceMapping []*awskarpenterv1.BlockDeviceMapping
	for _, mapping := range spec.BlockDeviceMappings {
		if mapping == nil {
			continue
		}

		blockDeviceMapping = append(blockDeviceMapping, &awskarpenterv1.BlockDeviceMapping{
			DeviceName: ptrIfNonEmpty(mapping.DeviceName),
			RootVolume: mapping.RootVolume == RootVolumeDesignationRootVolume,
			EBS:        mapping.EBS.ToKarpenterTypes(),
		})
	}

	return blockDeviceMapping
}

func (spec OpenshiftEC2NodeClassSpec) KarpenterInstanceStorePolicy() *awskarpenterv1.InstanceStorePolicy {
	if spec.InstanceStorePolicy == "" {
		return nil
	}
	return (*awskarpenterv1.InstanceStorePolicy)(&spec.InstanceStorePolicy)
}

func (spec OpenshiftEC2NodeClassSpec) KarpenterAssociatePublicIPAddress() *bool {
	switch spec.AssociatePublicIPAddress {
	case PublicIPAddressAssignmentEnabled:
		return ptr.To(true)
	case PublicIPAddressAssignmentDisabled:
		return ptr.To(false)
	default:
		return nil
	}
}

func (spec OpenshiftEC2NodeClassSpec) KarpenterDetailedMonitoring() *bool {
	switch spec.DetailedMonitoring {
	case DetailedMonitoringEnabled:
		return ptr.To(true)
	case DetailedMonitoringDisabled:
		return ptr.To(false)
	default:
		return nil
	}
}

func (bd BlockDevice) ToKarpenterTypes() *awskarpenterv1.BlockDevice {
	return &awskarpenterv1.BlockDevice{
		DeleteOnTermination: deleteOnTerminationToBool(bd.DeleteOnTermination),
		Encrypted:           encryptionStateToBool(bd.Encrypted),
		IOPS:                bd.IOPS,
		KMSKeyID:            ptrIfNonEmpty(bd.KMSKeyID),
		SnapshotID:          ptrIfNonEmpty(bd.SnapshotID),
		Throughput:          bd.Throughput,
		VolumeSize:          bd.VolumeSize,
		VolumeType:          ptrIfNonEmpty(bd.VolumeType),
	}
}

func deleteOnTerminationToBool(policy DeleteOnTerminationPolicy) *bool {
	switch policy {
	case DeleteOnTerminationPolicyDelete:
		return ptr.To(true)
	case DeleteOnTerminationPolicyRetain:
		return ptr.To(false)
	default:
		return nil
	}
}

func encryptionStateToBool(state EncryptionState) *bool {
	switch state {
	case EncryptionStateEncrypted:
		return ptr.To(true)
	case EncryptionStateUnencrypted:
		return ptr.To(false)
	default:
		return nil
	}
}

func ptrIfNonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
