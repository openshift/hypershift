package nodeclass

import (
	"fmt"
	"strings"
	"time"

	hyperkarpenterv1 "github.com/openshift/hypershift/api/karpenter/v1"

	awskarpenterv1 "github.com/aws/karpenter-provider-aws/pkg/apis/v1"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func karpenterBlockDeviceMappingFromNodeClassSpec(spec hyperkarpenterv1.OpenshiftEC2NodeClassSpec) []*awskarpenterv1.BlockDeviceMapping {
	if spec.BlockDeviceMappings == nil {
		return nil
	}
	var blockDeviceMapping []*awskarpenterv1.BlockDeviceMapping
	for _, mapping := range spec.BlockDeviceMappings {
		blockDeviceMapping = append(blockDeviceMapping, &awskarpenterv1.BlockDeviceMapping{
			DeviceName: ptrIfNonEmpty(mapping.DeviceName),
			RootVolume: mapping.RootVolume == hyperkarpenterv1.RootVolumeDesignationRootVolume,
			EBS:        karpenterBlockDeviceFromBlockDevice(mapping.EBS),
		})
	}

	return blockDeviceMapping
}

func karpenterCapacityReservationSelectorTermsFromNodeClassSpec(spec hyperkarpenterv1.OpenshiftEC2NodeClassSpec) []awskarpenterv1.CapacityReservationSelectorTerm {
	if spec.CapacityReservationSelectorTerms == nil {
		return nil
	}
	var terms []awskarpenterv1.CapacityReservationSelectorTerm
	for _, term := range spec.CapacityReservationSelectorTerms {
		terms = append(terms, awskarpenterv1.CapacityReservationSelectorTerm{
			Tags:    term.Tags,
			ID:      term.ID,
			OwnerID: term.OwnerID,
			// Our API uses PascalCase enum values (Open, Targeted) while upstream
			// karpenter uses lowercase (open, targeted), so we convert here.
			InstanceMatchCriteria: strings.ToLower(string(term.InstanceMatchCriteria)),
		})
	}
	return terms
}

func karpenterInstanceStorePolicyFromNodeClassSpec(spec hyperkarpenterv1.OpenshiftEC2NodeClassSpec) *awskarpenterv1.InstanceStorePolicy {
	if spec.InstanceStorePolicy == "" {
		return nil
	}
	return (*awskarpenterv1.InstanceStorePolicy)(&spec.InstanceStorePolicy)
}

func karpenterAssociatePublicIPAddressFromNodeClassSpec(spec hyperkarpenterv1.OpenshiftEC2NodeClassSpec) *bool {
	switch spec.IPAddressAssociation {
	case hyperkarpenterv1.IPAddressAssociationPublic:
		return ptr.To(true)
	case hyperkarpenterv1.IPAddressAssociationSubnetDefault:
		return ptr.To(false)
	default:
		return nil
	}
}

func karpenterMetadataOptionsFromNodeClassSpec(spec hyperkarpenterv1.OpenshiftEC2NodeClassSpec) *awskarpenterv1.MetadataOptions {
	mo := spec.MetadataOptions
	if mo.Access == "" && mo.HTTPIPProtocol == "" && mo.HTTPPutResponseHopLimit == 0 && mo.HTTPTokens == "" {
		return nil
	}
	opts := &awskarpenterv1.MetadataOptions{}
	switch mo.Access {
	case hyperkarpenterv1.MetadataAccessHTTPEndpoint:
		opts.HTTPEndpoint = ptr.To("enabled")
	case hyperkarpenterv1.MetadataAccessNone:
		opts.HTTPEndpoint = ptr.To("disabled")
	}
	switch mo.HTTPIPProtocol {
	case hyperkarpenterv1.MetadataHTTPProtocolIPv6:
		opts.HTTPProtocolIPv6 = ptr.To("enabled")
	case hyperkarpenterv1.MetadataHTTPProtocolIPv4:
		opts.HTTPProtocolIPv6 = ptr.To("disabled")
	}
	if mo.HTTPPutResponseHopLimit != 0 {
		opts.HTTPPutResponseHopLimit = ptr.To(mo.HTTPPutResponseHopLimit)
	}
	switch mo.HTTPTokens {
	case hyperkarpenterv1.MetadataHTTPTokensStateRequired:
		opts.HTTPTokens = ptr.To("required")
	case hyperkarpenterv1.MetadataHTTPTokensStateOptional:
		opts.HTTPTokens = ptr.To("optional")
	}
	return opts
}

func karpenterDetailedMonitoringFromNodeClassSpec(spec hyperkarpenterv1.OpenshiftEC2NodeClassSpec) *bool {
	switch spec.Monitoring {
	case hyperkarpenterv1.MonitoringStateDetailed:
		return ptr.To(true)
	case hyperkarpenterv1.MonitoringStateBasic:
		return ptr.To(false)
	default:
		return nil
	}
}

func karpenterBlockDeviceFromBlockDevice(bd hyperkarpenterv1.BlockDevice) *awskarpenterv1.BlockDevice {
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

func deleteOnTerminationToBool(policy hyperkarpenterv1.DeleteOnTerminationPolicy) *bool {
	switch policy {
	case hyperkarpenterv1.DeleteOnTerminationPolicyDelete:
		return ptr.To(true)
	case hyperkarpenterv1.DeleteOnTerminationPolicyRetain:
		return ptr.To(false)
	default:
		return nil
	}
}

func encryptionStateToBool(state hyperkarpenterv1.EncryptionState) *bool {
	switch state {
	case hyperkarpenterv1.EncryptionStateEncrypted:
		return ptr.To(true)
	case hyperkarpenterv1.EncryptionStateUnencrypted:
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

func volumeTypeToKarpenter(vt hyperkarpenterv1.VolumeType) *string {
	if vt == "" {
		return nil
	}
	// Upstream Karpenter uses lowercase volume type values.
	v := strings.ToLower(string(vt))
	return &v
}

func karpenterKubeletConfigurationFromNodeClassSpec(spec hyperkarpenterv1.OpenshiftEC2NodeClassSpec) *awskarpenterv1.KubeletConfiguration {
	if !spec.Kubelet.HasTypedFields() {
		return nil
	}
	return &awskarpenterv1.KubeletConfiguration{
		ImageGCHighThresholdPercent: spec.Kubelet.ImageGCHighThresholdPercent,
		ImageGCLowThresholdPercent:  spec.Kubelet.ImageGCLowThresholdPercent,
		MaxPods:                     ptrIfNonZero(spec.Kubelet.MaxPods),
		CPUCFSQuota:                 spec.Kubelet.CPUCFSQuota,
		EvictionHard:                spec.Kubelet.EvictionHard,
		EvictionSoft:                spec.Kubelet.EvictionSoft,
		EvictionSoftGracePeriod:     evictionSoftGracePeriodToDuration(spec.Kubelet.EvictionSoftGracePeriod),
		EvictionMaxPodGracePeriod:   spec.Kubelet.EvictionMaxPodGracePeriod,
		PodsPerCore:                 ptrIfNonZero(spec.Kubelet.PodsPerCore),
		SystemReserved:              spec.Kubelet.SystemReserved,
		KubeReserved:                spec.Kubelet.KubeReserved,
	}
}

func evictionSoftGracePeriodToDuration(m map[string]string) map[string]metav1.Duration {
	if m == nil {
		return nil
	}
	result := make(map[string]metav1.Duration, len(m))
	for k, v := range m {
		d, err := time.ParseDuration(v)
		if err != nil {
			continue
		}
		result[k] = metav1.Duration{Duration: d}
	}
	return result
}

func ptrIfNonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func ptrIfNonZero(v int32) *int32 {
	if v == 0 {
		return nil
	}
	return ptr.To(v)
}
