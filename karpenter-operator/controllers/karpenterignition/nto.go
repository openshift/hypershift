package karpenterignition

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperkarpenterv1 "github.com/openshift/hypershift/api/karpenter/v1"
	"github.com/openshift/hypershift/support/capabilities"
	karpenterutil "github.com/openshift/hypershift/support/karpenter"
	"github.com/openshift/hypershift/support/ntotuning"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func tuningNodePool(hcp *hyperv1.HostedControlPlane, openshiftEC2NodeClass *hyperkarpenterv1.OpenshiftEC2NodeClass) *hyperv1.NodePool {
	return &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      karpenterutil.KarpenterNodePoolName(openshiftEC2NodeClass),
			Namespace: hcp.Namespace,
		},
	}
}

// reconcileTuningConfigs validates embedded tuning manifests from the NodeClass
// and writes tuned-* / pp-* ConfigMaps in the HCP namespace for NTO.
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

	tunedConfig, ppConfig, ppCMName, err := ntotuning.GetTuningConfigFromRaw(openshiftEC2NodeClass.Spec.TuningConfig)
	if err != nil {
		return fmt.Errorf("failed to validate tuningConfig: %w", err)
	}

	return ntotuning.ReconcileTuningOutputs(
		ctx,
		r.ManagementClient,
		r.CreateOrUpdate,
		hcp.Namespace,
		nodePool,
		tunedConfig,
		ppConfig,
		ppCMName,
	)
}
