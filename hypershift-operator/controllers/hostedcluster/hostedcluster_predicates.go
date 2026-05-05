package hostedcluster

import (
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperkarpenterv1 "github.com/openshift/hypershift/api/karpenter/v1"
	hyperutil "github.com/openshift/hypershift/support/util"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// hostedClusterMirroredAnnotations are the HostedCluster annotations consumed by reconciliation and mirrored to the HostedControlPlane.
// Keep this list in sync with reconcileHostedControlPlaneAnnotations.
var hostedClusterMirroredAnnotations = []string{
	hyperv1.DisablePKIReconciliationAnnotation,
	hyperv1.OauthLoginURLOverrideAnnotation,
	hyperv1.KonnectivityAgentImageAnnotation,
	hyperv1.KonnectivityServerImageAnnotation,
	hyperv1.ClusterAutoscalerImage,
	hyperv1.IBMCloudKMSProviderImage,
	hyperv1.AWSKMSProviderImage,
	hyperv1.PortierisImageAnnotation,
	hyperutil.DebugDeploymentsAnnotation,
	hyperv1.DisableProfilingAnnotation,
	hyperv1.PrivateIngressControllerAnnotation,
	hyperv1.IngressControllerLoadBalancerScope,
	hyperv1.CleanupCloudResourcesAnnotation,
	hyperv1.ControlPlanePriorityClass,
	hyperv1.APICriticalPriorityClass,
	hyperv1.EtcdPriorityClass,
	hyperv1.EnsureExistsPullSecretReconciliation,
	hyperv1.TopologyAnnotation,
	hyperv1.DisableMachineManagement,
	hyperv1.CertifiedOperatorsCatalogImageAnnotation,
	hyperv1.CommunityOperatorsCatalogImageAnnotation,
	hyperv1.RedHatMarketplaceCatalogImageAnnotation,
	hyperv1.RedHatOperatorsCatalogImageAnnotation,
	hyperv1.OLMCatalogsISRegistryOverridesAnnotation,
	hyperv1.KubeAPIServerGOGCAnnotation,
	hyperv1.KubeAPIServerGOMemoryLimitAnnotation,
	hyperv1.RequestServingNodeAdditionalSelectorAnnotation,
	hyperv1.AWSLoadBalancerSubnetsAnnotation,
	hyperv1.AWSLoadBalancerTargetNodesAnnotation,
	hyperv1.AWSLoadBalancerHealthProbeModeAnnotation,
	hyperv1.AzureLoadBalancerHealthProbeModeAnnotation,
	hyperv1.SharedLoadBalancerHealthProbePathAnnotation,
	hyperv1.SharedLoadBalancerHealthProbePortAnnotation,
	hyperv1.ManagementPlatformAnnotation,
	hyperv1.KubeAPIServerVerbosityLevelAnnotation,
	hyperv1.KubeAPIServerMaximumRequestsInFlight,
	hyperv1.KubeAPIServerMaximumMutatingRequestsInFlight,
	hyperv1.DisableIgnitionServerAnnotation,
	hyperv1.AWSMachinePublicIPs,
	hyperv1.AWSKarpenterDefaultInstanceProfile,
	hyperkarpenterv1.KarpenterProviderAWSImage,
	hyperv1.KubeAPIServerGoAwayChance,
	hyperv1.KubeAPIServerServiceAccountTokenMaxExpiration,
	hyperv1.HostedClusterRestoredFromBackupAnnotation,
	// TODO: Remove this once the input is in the HostedCluster AWS API.
	"hypershift.openshift.io/aws-termination-handler-queue-url",
	hyperv1.SwiftPodNetworkInstanceAnnotation,
	hyperv1.EnableMetricsForwarding,
}

// hostedClusterActionableAnnotationPrefixes are the HostedCluster annotation prefixes consumed by reconciliation and mirrored to the HostedControlPlane.
var hostedClusterActionableAnnotationPrefixes = []string{
	hyperv1.IdentityProviderOverridesAnnotationPrefix,
	hyperv1.ResourceRequestOverrideAnnotationPrefix,
}

var hostedClusterAdditionalActionableAnnotations = []string{
	hyperv1.RestartDateAnnotation,
	hyperv1.ForceUpgradeToAnnotation,
	hyperv1.AllowUnsupportedKubeVirtRHCOSVariantsAnnotation,
	hyperv1.HCDestroyGracePeriodAnnotation,
	hyperv1.PodSecurityAdmissionLabelOverrideAnnotation,
	hyperv1.ClusterAPIManagerImage,
	hyperv1.ClusterAPIProviderAWSImage,
	hyperv1.ClusterAPIAzureProviderImage,
	hyperv1.ClusterAPIGCPProviderImage,
	hyperv1.ClusterAPIAgentProviderImage,
	hyperv1.ClusterAPIKubeVirtProviderImage,
	hyperv1.ClusterAPIPowerVSProviderImage,
	hyperv1.ClusterAPIOpenStackProviderImage,
	hyperv1.OpenStackResourceControllerImage,
	hyperv1.SkipReleaseImageValidation,
	hyperv1.SkipKASConflicSANValidation,
	hyperv1.SkipControlPlaneNamespaceDeletionAnnotation,
	hyperutil.HostedClustersScopeAnnotation,
}

var hostedClusterActionableLabelPrefixes = []string{
	"api.openshift.com",
}

func hostedClusterPrimaryPredicate(r client.Reader) predicate.Predicate {
	return predicate.And(
		hyperutil.PredicatesForHostedClusterAnnotationScoping(r),
		predicate.Or(
			predicate.GenerationChangedPredicate{},
			predicate.TypedFuncs[client.Object]{
				UpdateFunc: func(e event.TypedUpdateEvent[client.Object]) bool {
					oldHC, ok := e.ObjectOld.(*hyperv1.HostedCluster)
					if !ok || oldHC == nil {
						return false
					}

					newHC, ok := e.ObjectNew.(*hyperv1.HostedCluster)
					if !ok || newHC == nil {
						return false
					}

					return hostedClusterDeletionTimestampChanged(oldHC, newHC) ||
						hostedClusterActionableAnnotationChanged(oldHC.GetAnnotations(), newHC.GetAnnotations()) ||
						hostedClusterActionableLabelChanged(oldHC.GetLabels(), newHC.GetLabels())
				},
			},
		),
	)
}

func hostedClusterActionableAnnotationChanged(oldAnnotations, newAnnotations map[string]string) bool {
	for _, key := range hostedClusterMirroredAnnotations {
		if annotationValueChanged(oldAnnotations, newAnnotations, key) {
			return true
		}
	}

	for _, prefix := range hostedClusterActionableAnnotationPrefixes {
		if annotationPrefixChanged(oldAnnotations, newAnnotations, prefix) {
			return true
		}
	}

	for _, key := range hostedClusterAdditionalActionableAnnotations {
		if annotationValueChanged(oldAnnotations, newAnnotations, key) {
			return true
		}
	}

	return false
}

func annotationValueChanged(oldAnnotations, newAnnotations map[string]string, key string) bool {
	oldValue, oldHasValue := oldAnnotations[key]
	newValue, newHasValue := newAnnotations[key]

	return oldHasValue != newHasValue || oldValue != newValue
}

func annotationPrefixChanged(oldAnnotations, newAnnotations map[string]string, prefix string) bool {
	oldPrefixedAnnotations := prefixedAnnotations(oldAnnotations, prefix)
	newPrefixedAnnotations := prefixedAnnotations(newAnnotations, prefix)

	if len(oldPrefixedAnnotations) != len(newPrefixedAnnotations) {
		return true
	}

	for key, oldValue := range oldPrefixedAnnotations {
		if newValue, ok := newPrefixedAnnotations[key]; !ok || newValue != oldValue {
			return true
		}
	}

	return false
}

func prefixedAnnotations(annotations map[string]string, prefix string) map[string]string {
	prefixed := map[string]string{}
	for key, value := range annotations {
		if strings.HasPrefix(key, prefix) {
			prefixed[key] = value
		}
	}
	return prefixed
}

func hostedClusterActionableLabelChanged(oldLabels, newLabels map[string]string) bool {
	for _, prefix := range hostedClusterActionableLabelPrefixes {
		if annotationPrefixChanged(oldLabels, newLabels, prefix) {
			return true
		}
	}

	return false
}

func hostedClusterDeletionTimestampChanged(oldHC, newHC *hyperv1.HostedCluster) bool {
	oldDeletionTimestamp := oldHC.GetDeletionTimestamp()
	newDeletionTimestamp := newHC.GetDeletionTimestamp()

	switch {
	case oldDeletionTimestamp == nil && newDeletionTimestamp == nil:
		return false
	case oldDeletionTimestamp == nil || newDeletionTimestamp == nil:
		return true
	default:
		return !oldDeletionTimestamp.Equal(newDeletionTimestamp)
	}
}
