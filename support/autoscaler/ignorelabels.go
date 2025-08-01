package autoscaler

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

// AWS cloud provider ignore labels for the autoscaler.
const (
	// AwsIgnoredLabelEbsCsiZone is a label used by the AWS EBS CSI driver as a target for Persistent Volume Node Affinity.
	AwsIgnoredLabelEbsCsiZone = "topology.ebs.csi.aws.com/zone"

	// AwsIgnoredLabelK8sEniconfig is a label used by the AWS CNI for custom networking.
	AwsIgnoredLabelK8sEniconfig = "k8s.amazonaws.com/eniConfig"

	// AwsIgnoredLabelLifecycle is a label used by the AWS for spot.
	AwsIgnoredLabelLifecycle = "lifecycle"

	// AwsIgnoredLabelZoneID is a label used for the AWS-specific zone identifier, see https://github.com/kubernetes/cloud-provider-aws/issues/300 for a more detailed explanation of its use.
	AwsIgnoredLabelZoneID = "topology.k8s.aws/zone-id"
)

// IBM cloud provider ignore labels for the autoscaler.
const (
	// IbmcloudIgnoredLabelWorkerId is a label used by the IBM Cloud Cloud Controller Manager.
	IbmcloudIgnoredLabelWorkerId = "ibm-cloud.kubernetes.io/worker-id"

	// IbmcloudIgnoredLabelVpcBlockCsi is a label used by the IBM Cloud CSI driver as a target for Persistent Volume Node Affinity.
	IbmcloudIgnoredLabelVpcBlockCsi = "vpc-block-csi-driver-labels"
)

// Azure cloud provider ignore labels for the autoscaler.
const (
	// AzureDiskTopologyKey is the topology key of Azure Disk CSI driver.
	AzureDiskTopologyKey = "topology.disk.csi.azure.com/zone"

	// AzureNodepoolLegacyLabel is a label specifying which Azure node pool a particular node belongs to.
	AzureNodepoolLegacyLabel = "agentpool"

	// AzureNodepoolLabel is an AKS label specifying which nodepool a particular node belongs to.
	AzureNodepoolLabel = "kubernetes.azure.com/agentpool"
)

// Common ignore labels for the autoscaler that are applied to all platforms.
const (
	CommonIgnoredLabelNodePool          = "hypershift.openshift.io/nodePool"
	CommonIgnoredLabelAWSEBSZone        = "topology.ebs.csi.aws.com/zone"
	CommonIgnoredLabelAzureDiskZone     = "topology.disk.csi.azure.com/zone"
	CommonIgnoredLabelIBMCloudWorkerID  = "ibm-cloud.kubernetes.io/worker-id"
	CommonIgnoredLabelVPCBlockCSIDriver = "vpc-block-csi-driver-labels"
)

// GetIgnoreLabels returns a list of labels that the cluster autoscaler should ignore
// when balancing similar node groups. The labels include common labels for all platforms
// as well as platform-specific labels.
func GetIgnoreLabels(platformType hyperv1.PlatformType) []string {
	// Common labels for all platforms
	labels := []string{
		CommonIgnoredLabelNodePool,
		CommonIgnoredLabelAWSEBSZone,
		CommonIgnoredLabelAzureDiskZone,
		CommonIgnoredLabelIBMCloudWorkerID,
		CommonIgnoredLabelVPCBlockCSIDriver,
	}

	// Platform-specific labels
	switch platformType {
	case hyperv1.AWSPlatform:
		labels = append(labels,
			AwsIgnoredLabelK8sEniconfig,
			AwsIgnoredLabelLifecycle,
			AwsIgnoredLabelZoneID)
	case hyperv1.AzurePlatform:
		labels = append(labels,
			AzureNodepoolLegacyLabel,
			AzureNodepoolLabel)
	}

	return labels
}
