package conditions

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	support "github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ExpectedHCConditions(hostedCluster *hyperv1.HostedCluster) map[hyperv1.ConditionType]metav1.ConditionStatus {
	conditions := map[hyperv1.ConditionType]metav1.ConditionStatus{
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
		hyperv1.ValidHostedControlPlaneConfiguration: metav1.ConditionTrue,
		hyperv1.ValidReleaseImage:                    metav1.ConditionTrue,
		hyperv1.PlatformCredentialsFound:             metav1.ConditionTrue,

		hyperv1.HostedClusterProgressing:  metav1.ConditionFalse,
		hyperv1.HostedClusterDegraded:     metav1.ConditionFalse,
		hyperv1.ClusterVersionProgressing: metav1.ConditionFalse,
		hyperv1.ControlPlaneUpToDate:      metav1.ConditionTrue,
	}

	switch hostedCluster.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		// TODO: hyperv1.ValidOIDCConfiguration should really be platform-agnostic, but tooday it's not
		conditions[hyperv1.ValidOIDCConfiguration] = metav1.ConditionTrue

		conditions[hyperv1.ValidAWSIdentityProvider] = metav1.ConditionTrue
		conditions[hyperv1.AWSDefaultSecurityGroupCreated] = metav1.ConditionTrue
		if hostedCluster.Spec.Platform.AWS != nil {
			if hostedCluster.Spec.Platform.AWS.EndpointAccess == hyperv1.Private || hostedCluster.Spec.Platform.AWS.EndpointAccess == hyperv1.PublicAndPrivate {
				conditions[hyperv1.AWSEndpointAvailable] = metav1.ConditionTrue
				conditions[hyperv1.AWSEndpointServiceAvailable] = metav1.ConditionTrue
			}
		}
		if hostedCluster.Spec.SecretEncryption == nil || hostedCluster.Spec.SecretEncryption.KMS == nil || hostedCluster.Spec.SecretEncryption.KMS.AWS == nil {
			// AWS KMS is not configured
			conditions[hyperv1.ValidAWSKMSConfig] = metav1.ConditionUnknown
		} else {
			conditions[hyperv1.ValidAWSKMSConfig] = metav1.ConditionTrue
		}
	case hyperv1.AzurePlatform:
		if hostedCluster.Spec.SecretEncryption == nil || hostedCluster.Spec.SecretEncryption.KMS == nil || hostedCluster.Spec.SecretEncryption.KMS.Azure == nil {
			// Azure KMS is not configured
			conditions[hyperv1.ValidAzureKMSConfig] = metav1.ConditionUnknown
		} else {
			conditions[hyperv1.ValidAzureKMSConfig] = metav1.ConditionTrue
		}
	case hyperv1.KubevirtPlatform:
		if hostedCluster.Spec.Networking.NetworkType == hyperv1.OVNKubernetes {
			if hostedCluster.Annotations[hyperv1.ManagementPlatformAnnotation] == string(hyperv1.AWSPlatform) {
				// AWS platform supports Jumbo frames
				conditions[hyperv1.ValidKubeVirtInfraNetworkMTU] = metav1.ConditionTrue
			} else if hostedCluster.Annotations[hyperv1.ManagementPlatformAnnotation] == string(hyperv1.AzurePlatform) {
				// Azure platform doesn't support Jumbo frames
				conditions[hyperv1.ValidKubeVirtInfraNetworkMTU] = metav1.ConditionFalse
			}
		}
		if hostedCluster.Annotations[hyperv1.ManagementPlatformAnnotation] == string(hyperv1.AWSPlatform) {
			// in the e2e we're using a filesystem VolumeMode by default for the VMs,
			// thus the PVC is RWO and VMs are expected to be non-live-migratable
			conditions[hyperv1.KubeVirtNodesLiveMigratable] = metav1.ConditionFalse
		}
	}

	if hostedCluster.Spec.Etcd.ManagementType == hyperv1.Unmanaged {
		conditions[hyperv1.UnmanagedEtcdAvailable] = metav1.ConditionTrue
	}

	kasExternalHostname := support.ServiceExternalDNSHostnameByHC(hostedCluster, hyperv1.APIServer)
	if kasExternalHostname == "" {
		// ExternalDNS is not configured
		conditions[hyperv1.ExternalDNSReachable] = metav1.ConditionUnknown
	} else {
		conditions[hyperv1.ExternalDNSReachable] = metav1.ConditionTrue
	}

	return conditions
}

func ExpectedNodePoolConditions(nodePool *hyperv1.NodePool) map[string]corev1.ConditionStatus {
	conditions := map[string]corev1.ConditionStatus{
		hyperv1.NodePoolReadyConditionType:                   corev1.ConditionTrue,
		hyperv1.NodePoolValidReleaseImageConditionType:       corev1.ConditionTrue,
		hyperv1.NodePoolValidGeneratedPayloadConditionType:   corev1.ConditionTrue,
		hyperv1.NodePoolValidMachineConfigConditionType:      corev1.ConditionTrue,
		hyperv1.NodePoolValidTuningConfigConditionType:       corev1.ConditionTrue,
		hyperv1.NodePoolAllMachinesReadyConditionType:        corev1.ConditionTrue,
		hyperv1.NodePoolAllNodesHealthyConditionType:         corev1.ConditionTrue,
		hyperv1.NodePoolReconciliationActiveConditionType:    corev1.ConditionTrue,
		hyperv1.NodePoolReachedIgnitionEndpoint:              corev1.ConditionTrue,
		hyperv1.NodePoolUpdateManagementEnabledConditionType: corev1.ConditionTrue,
		hyperv1.NodePoolValidArchPlatform:                    corev1.ConditionTrue,

		hyperv1.NodePoolUpdatingVersionConditionType:                 corev1.ConditionFalse,
		hyperv1.NodePoolUpdatingConfigConditionType:                  corev1.ConditionFalse,
		hyperv1.NodePoolUpdatingPlatformMachineTemplateConditionType: corev1.ConditionFalse,
	}

	switch nodePool.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		conditions[hyperv1.NodePoolAWSSecurityGroupAvailableConditionType] = corev1.ConditionTrue
		conditions[hyperv1.NodePoolValidPlatformImageType] = corev1.ConditionTrue
	case hyperv1.KubevirtPlatform:
		conditions[hyperv1.NodePoolValidPlatformImageType] = corev1.ConditionTrue
	case hyperv1.PowerVSPlatform:
		conditions[hyperv1.NodePoolValidPlatformImageType] = corev1.ConditionTrue
	}

	if nodePool.Spec.AutoScaling != nil {
		conditions[hyperv1.NodePoolAutoscalingEnabledConditionType] = corev1.ConditionTrue
	} else {
		conditions[hyperv1.NodePoolAutoscalingEnabledConditionType] = corev1.ConditionFalse
	}

	if nodePool.Spec.Management.AutoRepair {
		conditions[hyperv1.NodePoolAutorepairEnabledConditionType] = corev1.ConditionTrue
	} else {
		conditions[hyperv1.NodePoolAutorepairEnabledConditionType] = corev1.ConditionFalse
	}

	return conditions
}

func SetFalseCondition(hcp *hyperv1.HostedControlPlane, conditionType hyperv1.ConditionType, reason, message string) {
	condition := metav1.Condition{
		Type:               string(conditionType),
		ObservedGeneration: hcp.Generation,
		Status:             metav1.ConditionFalse,
		Message:            message,
		Reason:             reason,
	}
	meta.SetStatusCondition(&hcp.Status.Conditions, condition)
}
