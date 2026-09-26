package karpenterignition

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperkarpenterv1 "github.com/openshift/hypershift/api/karpenter/v1"
	"github.com/openshift/hypershift/support/capabilities"
	karpenterutil "github.com/openshift/hypershift/support/karpenter"
	"github.com/openshift/hypershift/support/ntotuning"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// mirroredTuningConfigLabel is duplicated from hypershift-operator/controllers/hostedcluster for the ConfigMap watch predicate.
	mirroredTuningConfigLabel = "hypershift.openshift.io/mirrored-tuning-config"
)

func tuningNodePool(hcp *hyperv1.HostedControlPlane, openshiftEC2NodeClass *hyperkarpenterv1.OpenshiftEC2NodeClass) *hyperv1.NodePool {
	return &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      karpenterutil.KarpenterNodePoolName(openshiftEC2NodeClass),
			Namespace: hcp.Namespace,
		},
	}
}

// reconcileTuningConfigs reads mirrored tuning ConfigMaps from the HCP namespace and writes
// the tuned-* ConfigMap consumed by NTO. Source ConfigMaps are copied from the HostedCluster
// namespace by the HostedCluster controller.
func (r *KarpenterIgnitionReconciler) reconcileTuningConfigs(
	ctx context.Context,
	hcp *hyperv1.HostedControlPlane,
	openshiftEC2NodeClass *hyperkarpenterv1.OpenshiftEC2NodeClass,
) error {
	nodePool := tuningNodePool(hcp, openshiftEC2NodeClass)
	if !capabilities.IsNodeTuningCapabilityEnabled(hcp.Spec.Capabilities) {
		return ntotuning.DeleteTuningOutputs(ctx, r.ManagementClient, hcp.Namespace, nodePool.Name)
	}
	if len(openshiftEC2NodeClass.Spec.TuningConfig) == 0 {
		return ntotuning.DeleteTuningOutputs(ctx, r.ManagementClient, hcp.Namespace, nodePool.Name)
	}

	// Mirrored ConfigMaps in the HCP namespace are prefixed to avoid name collisions
	// with existing HCP ConfigMaps (e.g. root-ca).
	mirroredRefs := make([]corev1.LocalObjectReference, len(openshiftEC2NodeClass.Spec.TuningConfig))
	for i, ref := range openshiftEC2NodeClass.Spec.TuningConfig {
		mirroredRefs[i] = corev1.LocalObjectReference{Name: karpenterutil.MirroredTuningConfigPrefix + ref.Name}
	}

	tunedConfig, performanceProfileConfig, performanceProfileConfigMapName, err := ntotuning.GetTuningConfig(
		ctx, r.ManagementClient, hcp.Namespace, mirroredRefs,
	)
	if err != nil {
		return fmt.Errorf("failed to get tuningConfig: %w", err)
	}

	return ntotuning.ReconcileTuningOutputs(
		ctx,
		r.ManagementClient,
		r.CreateOrUpdate,
		hcp.Namespace,
		nodePool,
		tunedConfig,
		performanceProfileConfig,
		performanceProfileConfigMapName,
	)
}
