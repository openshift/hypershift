package nodepool

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/nodepool/kubevirt"
	"github.com/openshift/hypershift/support/releaseinfo"
	corev1 "k8s.io/api/core/v1"
)

func (r *NodePoolReconciler) setKubevirtConditions(ctx context.Context, nodePool *hyperv1.NodePool, hcluster *hyperv1.HostedCluster, controlPlaneNamespace string, releaseImage *releaseinfo.ReleaseImage) error {
	// moved KubeVirt specific handling up here, so the caching of the boot image will start as early as possible
	// in order to actually save time. Caching form the original location will take more time, because the VMs can't
	// be created before the caching is 100% done. But moving this logic here, the caching will be done in parallel
	// to the ignition settings, and so it will be ready, or almost ready, when the VMs are created.
	if err := kubevirt.PlatformValidation(nodePool); err != nil {
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolValidMachineConfigConditionType,
			Status:             corev1.ConditionFalse,
			Reason:             hyperv1.NodePoolValidationFailedReason,
			Message:            fmt.Sprintf("validation of NodePool KubeVirt platform failed: %s", err.Error()),
			ObservedGeneration: nodePool.Generation,
		})
		return fmt.Errorf("validation of NodePool KubeVirt platform failed: %w", err)
	}
	removeStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolValidMachineConfigConditionType)

	infraNS := controlPlaneNamespace
	if hcluster.Spec.Platform.Kubevirt != nil &&
		hcluster.Spec.Platform.Kubevirt.Credentials != nil &&
		len(hcluster.Spec.Platform.Kubevirt.Credentials.InfraNamespace) > 0 {

		infraNS = hcluster.Spec.Platform.Kubevirt.Credentials.InfraNamespace

		if nodePool.Status.Platform == nil {
			nodePool.Status.Platform = &hyperv1.NodePoolPlatformStatus{}
		}

		if nodePool.Status.Platform.KubeVirt == nil {
			nodePool.Status.Platform.KubeVirt = &hyperv1.KubeVirtNodePoolStatus{}
		}

		nodePool.Status.Platform.KubeVirt.Credentials = hcluster.Spec.Platform.Kubevirt.Credentials.DeepCopy()
	}
	kubevirtBootImage, err := kubevirt.GetImage(nodePool, releaseImage, infraNS)
	if err != nil {
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolValidPlatformImageType,
			Status:             corev1.ConditionFalse,
			Reason:             hyperv1.NodePoolValidationFailedReason,
			Message:            fmt.Sprintf("Couldn't discover a KubeVirt Image for release image %q: %s", nodePool.Spec.Release.Image, err.Error()),
			ObservedGeneration: nodePool.Generation,
		})
		return fmt.Errorf("couldn't discover a KubeVirt Image in release payload image: %w", err)
	}

	SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
		Type:               hyperv1.NodePoolValidPlatformImageType,
		Status:             corev1.ConditionTrue,
		Reason:             hyperv1.AsExpectedReason,
		Message:            fmt.Sprintf("Bootstrap KubeVirt Image is %s", kubevirtBootImage.String()),
		ObservedGeneration: nodePool.Generation,
	})

	uid := string(nodePool.GetUID())

	var creds *hyperv1.KubevirtPlatformCredentials

	if hcluster.Spec.Platform.Kubevirt != nil && hcluster.Spec.Platform.Kubevirt.Credentials != nil {
		creds = hcluster.Spec.Platform.Kubevirt.Credentials
	}

	kvInfraClient, err := r.KubevirtInfraClients.DiscoverKubevirtClusterClient(ctx, r.Client, uid, creds, controlPlaneNamespace, hcluster.GetNamespace())
	if err != nil {
		return fmt.Errorf("failed to get KubeVirt external infra-cluster: %w", err)
	}
	err = kubevirtBootImage.CacheImage(ctx, kvInfraClient.GetInfraClient(), nodePool, uid)
	if err != nil {
		return fmt.Errorf("failed to create or validate KubeVirt image cache: %w", err)
	}

	r.addKubeVirtCacheNameToStatus(kubevirtBootImage, nodePool)

	// If this is a new nodepool, or we're currently updating a nodepool, then it is safe to
	// use the new topologySpreadConstraints feature over pod anti-affinity when
	// spreading out the VMs across the infra cluster
	if nodePool.Status.Version == "" || isUpdatingVersion(nodePool, releaseImage.Version()) {
		nodePool.Annotations[hyperv1.NodePoolSupportsKubevirtTopologySpreadConstraintsAnnotation] = "true"
	}

	return nil
}
