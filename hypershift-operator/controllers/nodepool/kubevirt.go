package nodepool

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/nodepool/kubevirt"
	"github.com/openshift/hypershift/support/releaseinfo"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	capikubevirt "sigs.k8s.io/cluster-api-provider-kubevirt/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *NodePoolReconciler) addKubeVirtCacheNameToStatus(kubevirtBootImage kubevirt.BootImage, nodePool *hyperv1.NodePool) {
	if namer, ok := kubevirtBootImage.(kubevirt.BootImageNamer); ok {
		if cacheName := namer.GetCacheName(); len(cacheName) > 0 {
			if nodePool.Status.Platform == nil {
				nodePool.Status.Platform = &hyperv1.NodePoolPlatformStatus{}
			}

			if nodePool.Status.Platform.KubeVirt == nil {
				nodePool.Status.Platform.KubeVirt = &hyperv1.KubeVirtNodePoolStatus{}
			}

			nodePool.Status.Platform.KubeVirt.CacheName = cacheName
		}
	}
}

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

func (r *NodePoolReconciler) setAllMachinesLMCondition(ctx context.Context, nodePool *hyperv1.NodePool, hcluster *hyperv1.HostedCluster) error {
	controlPlaneNamespace := manifests.HostedControlPlaneNamespace(hcluster.Namespace, hcluster.Name)
	kubevirtMachines := &capikubevirt.KubevirtMachineList{}
	err := r.List(ctx, kubevirtMachines, &client.ListOptions{
		Namespace: controlPlaneNamespace,
	})
	if err != nil {
		return fmt.Errorf("failed to list KubeVirt Machines: %w", err)
	}

	if len(kubevirtMachines.Items) == 0 {
		// not setting the condition if there are no kubevirt machines present
		return nil
	}

	numNotLiveMigratable := 0
	messageMap := make(map[string][]string)
	var mapReason, mapMessage string
	for _, kubevirtmachine := range kubevirtMachines.Items {
		for _, cond := range kubevirtmachine.Status.Conditions {
			if cond.Type == capikubevirt.VMLiveMigratableCondition && cond.Status == corev1.ConditionFalse {
				mapReason = cond.Reason
				mapMessage = fmt.Sprintf("Machine %s: %s: %s\n", kubevirtmachine.Name, cond.Reason, cond.Message)
				numNotLiveMigratable++
				messageMap[mapReason] = append(messageMap[mapReason], mapMessage)
			}
		}
	}

	if numNotLiveMigratable == 0 {
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolKubeVirtLiveMigratableType,
			Status:             corev1.ConditionTrue,
			Reason:             hyperv1.AsExpectedReason,
			Message:            hyperv1.AllIsWellMessage,
			ObservedGeneration: nodePool.Generation,
		})
	} else {
		reason, message := aggregateMachineReasonsAndMessages(messageMap, len(kubevirtMachines.Items), numNotLiveMigratable, aggregatorMachineStateLiveMigratable)
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolKubeVirtLiveMigratableType,
			Status:             corev1.ConditionFalse,
			Reason:             reason,
			Message:            message,
			ObservedGeneration: nodePool.Generation,
		})
	}
	return nil
}

func (c *CAPI) kubevirtMachineTemplate(templateNameGenerator func(spec any) (string, error)) (*capikubevirt.KubevirtMachineTemplate, error) {
	nodePool := c.nodePool
	spec, err := kubevirt.MachineTemplateSpec(nodePool, c.hostedCluster, c.releaseImage, nil)
	if err != nil {
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolValidMachineTemplateConditionType,
			Status:             corev1.ConditionFalse,
			Reason:             hyperv1.InvalidKubevirtMachineTemplate,
			Message:            err.Error(),
			ObservedGeneration: nodePool.Generation,
		})

		return nil, err
	} else {
		removeStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolValidMachineTemplateConditionType)
	}

	templateName, err := templateNameGenerator(spec)
	if err != nil {
		return nil, fmt.Errorf("failed to generate template name: %w", err)
	}

	template := &capikubevirt.KubevirtMachineTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name: templateName,
		},
		Spec: *spec,
	}

	return template, nil
}
