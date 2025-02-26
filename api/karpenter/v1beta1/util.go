package v1beta1

import (
	awskarpenterv1 "github.com/aws/karpenter-provider-aws/pkg/apis/v1"
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
			DeviceName: mapping.DeviceName,
			RootVolume: mapping.RootVolume,
			EBS:        mapping.EBS.ToKarpenterTypes(),
		})
	}

	return blockDeviceMapping
}

func (spec OpenshiftEC2NodeClassSpec) KarpenterInstanceStorePolicy() *awskarpenterv1.InstanceStorePolicy {
	if spec.InstanceStorePolicy == nil {
		return nil
	}
	return (*awskarpenterv1.InstanceStorePolicy)(spec.InstanceStorePolicy)
}

func (bd *BlockDevice) ToKarpenterTypes() *awskarpenterv1.BlockDevice {
	if bd == nil {
		return nil
	}

	return &awskarpenterv1.BlockDevice{
		DeleteOnTermination: bd.DeleteOnTermination,
		Encrypted:           bd.Encrypted,
		IOPS:                bd.IOPS,
		KMSKeyID:            bd.KMSKeyID,
		SnapshotID:          bd.SnapshotID,
		Throughput:          bd.Throughput,
		VolumeSize:          bd.VolumeSize,
		VolumeType:          bd.VolumeType,
	}
}
