package v1beta1

import (
	"fmt"
	"strings"

	awskarpenterv1 "github.com/aws/karpenter-provider-aws/pkg/apis/v1"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"
)

func (spec OpenshiftEC2NodeClassSpec) KarpenterBlockDeviceMapping() []*awskarpenterv1.BlockDeviceMapping {
	if spec.BlockDeviceMappings == nil {
		return nil
	}
	var blockDeviceMapping []*awskarpenterv1.BlockDeviceMapping
	for _, mapping := range spec.BlockDeviceMappings {
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
	switch spec.IPAddressAssociation {
	case IPAddressAssociationPublic:
		return ptr.To(true)
	case IPAddressAssociationSubnetDefault:
		return ptr.To(false)
	default:
		return nil
	}
}

func (spec OpenshiftEC2NodeClassSpec) KarpenterMetadataOptions() *awskarpenterv1.MetadataOptions {
	if spec.MetadataOptions == nil {
		return nil
	}
	return &awskarpenterv1.MetadataOptions{
		HTTPEndpoint:            spec.MetadataOptions.HTTPEndpoint,
		HTTPProtocolIPv6:        spec.MetadataOptions.HTTPProtocolIPv6,
		HTTPPutResponseHopLimit: spec.MetadataOptions.HTTPPutResponseHopLimit,
		HTTPTokens:              spec.MetadataOptions.HTTPTokens,
	}
}

func (spec OpenshiftEC2NodeClassSpec) KarpenterDetailedMonitoring() *bool {
	switch spec.Monitoring {
	case MonitoringStateDetailed:
		return ptr.To(true)
	case MonitoringStateBasic:
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
		VolumeSize:          volumeSizeGiBToQuantity(bd.VolumeSizeGiB),
		VolumeType:          volumeTypeToKarpenter(bd.VolumeType),
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

func volumeSizeGiBToQuantity(sizeGiB int64) *resource.Quantity {
	if sizeGiB == 0 {
		return nil
	}
	q := resource.MustParse(fmt.Sprintf("%dGi", sizeGiB))
	return &q
}

func volumeTypeToKarpenter(vt VolumeType) *string {
	if vt == "" {
		return nil
	}
	// Upstream Karpenter uses lowercase volume type values.
	v := strings.ToLower(string(vt))
	return &v
}

func ptrIfNonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
