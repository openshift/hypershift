/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package hostedcluster

import (
	"context"
	"fmt"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	karpenterv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/karpenter"
	karpenteroperatorv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/karpenteroperator"
	controlplanecomponent "github.com/openshift/hypershift/support/controlplane-component"
	karpenterutil "github.com/openshift/hypershift/support/karpenter"
	"github.com/openshift/hypershift/support/upsert"
	hyperutil "github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *HostedClusterReconciler) reconcileKarpenterOperator(cpContext controlplanecomponent.ControlPlaneContext, createOrUpdate upsert.CreateOrUpdateFN, hcluster *hyperv1.HostedCluster, hypershiftOperatorImage, controlPlaneOperatorImage string) error {
	if karpenterutil.IsKarpenterEnabled(hcluster.Spec.AutoNode) && hcluster.Status.KubeConfig != nil && hcluster.Status.IgnitionEndpoint != "" {
		// Generate configMap with KubeletConfig to register Nodes with karpenter expected taint.
		configMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      karpenterutil.KarpenterTaintConfigMapName,
				Namespace: cpContext.HCP.Namespace,
			},
		}

		kubeletConfig := fmt.Sprintf(`apiVersion: machineconfiguration.openshift.io/v1
kind: KubeletConfig
metadata:
  name: %s
spec:
  kubeletConfig:
    registerWithTaints:
      - key: "karpenter.sh/unregistered"
        value: "true"
        effect: "NoExecute"`, karpenterutil.KarpenterTaintConfigMapName)

		_, err := createOrUpdate(cpContext, r.Client, configMap, func() error {
			configMap.Data = map[string]string{
				"config": kubeletConfig,
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("failed to create configmap: %w", err)
		}
	}

	// When Karpenter is disabled, clear any stale AutoNode status from the HCP.
	// The karpenter-operator only runs when enabled, so it cannot clear this itself.
	if !karpenterutil.IsKarpenterEnabled(hcluster.Spec.AutoNode) {
		if cpContext.HCP.Status.AutoNode != nil {
			patch := client.MergeFrom(cpContext.HCP.DeepCopy())
			cpContext.HCP.Status.AutoNode = nil
			if err := cpContext.Client.Status().Patch(cpContext, cpContext.HCP, patch); err != nil {
				return fmt.Errorf("failed to clear AutoNode status: %w", err)
			}
		}
		// Delete the taint ConfigMap if it exists — it is only valid while Karpenter is enabled.
		if _, err := hyperutil.DeleteIfNeeded(cpContext, r.Client, &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      karpenterutil.KarpenterTaintConfigMapName,
				Namespace: cpContext.HCP.Namespace,
			},
		}); err != nil {
			return fmt.Errorf("failed to delete karpenter taint configmap: %w", err)
		}
	}

	// Always reconcile the karpenter-operator component — its predicate handles enable/disable and
	// triggers deletion of the ControlPlaneComponent CR and associated resources when Karpenter is disabled.
	karpenteroperator := karpenteroperatorv2.NewComponent(&karpenteroperatorv2.KarpenterOperatorOptions{
		HyperShiftOperatorImage:   hypershiftOperatorImage,
		ControlPlaneOperatorImage: controlPlaneOperatorImage,
		IgnitionEndpoint:          hcluster.Status.IgnitionEndpoint,
	})
	if err := karpenteroperator.Reconcile(cpContext); err != nil {
		return fmt.Errorf("failed to reconcile karpenter-operator component: %w", err)
	}

	// Always reconcile the karpenter component — its predicate handles enable/disable.
	if err := karpenterv2.NewComponent().Reconcile(cpContext); err != nil {
		return fmt.Errorf("failed to reconcile karpenter component: %w", err)
	}

	return nil
}

// reconcileAutoNodeEnabledCondition returns the AutoNodeEnabled condition reflecting both the desired
// state (spec) and the actual rollout progress of the Karpenter ControlPlaneComponent resources.
//
// States:
//   - True  / AsExpected          — Karpenter enabled in spec AND both components fully rolled out.
//   - False / AutoNodeProgressing — Enable or disable operation is in progress.
//   - False / AutoNodeNotConfigured — Karpenter not in spec AND no components present.
func (r *HostedClusterReconciler) reconcileAutoNodeEnabledCondition(ctx context.Context, hcluster *hyperv1.HostedCluster, hcpNamespace string) metav1.Condition {
	condition := metav1.Condition{
		Type:               string(hyperv1.AutoNodeEnabled),
		ObservedGeneration: hcluster.Generation,
	}

	karpenterEnabled := karpenterutil.IsKarpenterEnabled(hcluster.Spec.AutoNode)

	// List all ControlPlaneComponent resources in the HCP namespace and pick out the Karpenter ones.
	componentList := &hyperv1.ControlPlaneComponentList{}
	if err := r.Client.List(ctx, componentList, client.InNamespace(hcpNamespace)); err != nil {
		// Cannot determine component state; fall back to spec-only logic.
		if karpenterEnabled {
			condition.Status = metav1.ConditionFalse
			condition.Reason = hyperv1.AutoNodeProgressingReason
			condition.Message = "AutoNode (Karpenter) is being enabled: waiting for components"
		} else {
			condition.Status = metav1.ConditionFalse
			condition.Reason = hyperv1.AutoNodeNotConfiguredReason
			condition.Message = "AutoNode provisioner is not configured"
		}
		return condition
	}

	var karpenterComponents []hyperv1.ControlPlaneComponent
	for _, c := range componentList.Items {
		if c.Name == karpenteroperatorv2.ComponentName || c.Name == karpenterv2.ComponentName {
			karpenterComponents = append(karpenterComponents, c)
		}
	}

	if karpenterEnabled {
		if len(karpenterComponents) < 2 {
			condition.Status = metav1.ConditionFalse
			condition.Reason = hyperv1.AutoNodeProgressingReason
			condition.Message = "AutoNode (Karpenter) is being enabled: waiting for components to be created"
			return condition
		}
		var notReady []string
		for _, c := range karpenterComponents {
			rollout := meta.FindStatusCondition(c.Status.Conditions, string(hyperv1.ControlPlaneComponentRolloutComplete))
			if rollout == nil || rollout.Status != metav1.ConditionTrue {
				msg := "not rolled out"
				if rollout != nil {
					msg = rollout.Message
				}
				notReady = append(notReady, fmt.Sprintf("%s: %s", c.Name, msg))
			}
		}
		if len(notReady) > 0 {
			condition.Status = metav1.ConditionFalse
			condition.Reason = hyperv1.AutoNodeProgressingReason
			condition.Message = fmt.Sprintf("AutoNode (Karpenter) is being enabled: %s", strings.Join(notReady, "; "))
			return condition
		}
		condition.Status = metav1.ConditionTrue
		condition.Reason = hyperv1.AsExpectedReason
		condition.Message = "AutoNode (Karpenter) is ready"
		return condition
	}

	// Karpenter not enabled — check if components are still being removed.
	if len(karpenterComponents) > 0 {
		var names []string
		for _, c := range karpenterComponents {
			names = append(names, c.Name)
		}
		condition.Status = metav1.ConditionFalse
		condition.Reason = hyperv1.AutoNodeProgressingReason
		condition.Message = fmt.Sprintf("AutoNode (Karpenter) is being disabled: waiting for components to be removed: %s", strings.Join(names, ", "))
		return condition
	}

	condition.Status = metav1.ConditionFalse
	condition.Reason = hyperv1.AutoNodeNotConfiguredReason
	condition.Message = "AutoNode provisioner is not configured"
	return condition
}
