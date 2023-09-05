package conditions

import (
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ExpectedHCConditions() map[hyperv1.ConditionType]metav1.ConditionStatus {
	return map[hyperv1.ConditionType]metav1.ConditionStatus{
		hyperv1.HostedClusterAvailable:               metav1.ConditionTrue,
		hyperv1.InfrastructureReady:                  metav1.ConditionTrue,
		hyperv1.KubeAPIServerAvailable:               metav1.ConditionTrue,
		hyperv1.IgnitionEndpointAvailable:            metav1.ConditionTrue,
		hyperv1.EtcdAvailable:                        metav1.ConditionTrue,
		hyperv1.ValidReleaseInfo:                     metav1.ConditionTrue,
		hyperv1.ValidHostedClusterConfiguration:      metav1.ConditionTrue,
		hyperv1.SupportedHostedCluster:               metav1.ConditionTrue,
		hyperv1.ClusterVersionSucceeding:             metav1.ConditionTrue,
		hyperv1.ClusterVersionAvailable:              metav1.ConditionTrue,
		hyperv1.ClusterVersionReleaseAccepted:        metav1.ConditionTrue,
		hyperv1.ReconciliationActive:                 metav1.ConditionTrue,
		hyperv1.ReconciliationSucceeded:              metav1.ConditionTrue,
		hyperv1.ValidOIDCConfiguration:               metav1.ConditionTrue,
		hyperv1.ValidAWSIdentityProvider:             metav1.ConditionTrue,
		hyperv1.AWSDefaultSecurityGroupCreated:       metav1.ConditionTrue,
		hyperv1.ValidHostedControlPlaneConfiguration: metav1.ConditionTrue,
		hyperv1.ValidReleaseImage:                    metav1.ConditionTrue,
		hyperv1.PlatformCredentialsFound:             metav1.ConditionTrue,
		hyperv1.AWSEndpointAvailable:                 metav1.ConditionTrue,
		hyperv1.AWSEndpointServiceAvailable:          metav1.ConditionTrue,

		hyperv1.HostedClusterProgressing:  metav1.ConditionFalse,
		hyperv1.HostedClusterDegraded:     metav1.ConditionFalse,
		hyperv1.ClusterVersionFailing:     metav1.ConditionFalse,
		hyperv1.ClusterVersionProgressing: metav1.ConditionFalse,

		// conditions with no strong gurantees
		//
		// UnmanagedEtcdAvailable is only set if hcluster.Spec.Etcd.ManagementType == hyperv1.Unmanaged.
		hyperv1.UnmanagedEtcdAvailable: metav1.ConditionTrue,
		// ClusterVersionUpgradeable is not always guranteed to be true.
		hyperv1.ClusterVersionUpgradeable: metav1.ConditionTrue,
		// ExternalDNSReachable could be set to ConditionUnknown if external DNS is not configured or HC is private.
		hyperv1.ExternalDNSReachable: metav1.ConditionTrue,
		// ValidAWSKMSConfig could be set to ConditionUnknown if KMS key/role are not configured.
		hyperv1.ValidAWSKMSConfig: metav1.ConditionTrue,
	}
}

func ExpectedNodePoolConditions() map[string]corev1.ConditionStatus {
	return map[string]corev1.ConditionStatus{
		hyperv1.NodePoolReadyConditionType:                     corev1.ConditionTrue,
		hyperv1.NodePoolValidReleaseImageConditionType:         corev1.ConditionTrue,
		hyperv1.NodePoolValidGeneratedPayloadConditionType:     corev1.ConditionTrue,
		hyperv1.NodePoolValidPlatformImageType:                 corev1.ConditionTrue,
		hyperv1.NodePoolValidMachineConfigConditionType:        corev1.ConditionTrue,
		hyperv1.NodePoolValidTuningConfigConditionType:         corev1.ConditionTrue,
		hyperv1.NodePoolAllMachinesReadyConditionType:          corev1.ConditionTrue,
		hyperv1.NodePoolAllNodesHealthyConditionType:           corev1.ConditionTrue,
		hyperv1.NodePoolReconciliationActiveConditionType:      corev1.ConditionTrue,
		hyperv1.NodePoolReachedIgnitionEndpoint:                corev1.ConditionTrue,
		hyperv1.NodePoolAWSSecurityGroupAvailableConditionType: corev1.ConditionTrue,
		hyperv1.NodePoolUpdateManagementEnabledConditionType:   corev1.ConditionTrue,

		hyperv1.NodePoolUpdatingVersionConditionType:                 corev1.ConditionFalse,
		hyperv1.NodePoolUpdatingConfigConditionType:                  corev1.ConditionFalse,
		hyperv1.NodePoolUpdatingPlatformMachineTemplateConditionType: corev1.ConditionFalse,
	}
}
