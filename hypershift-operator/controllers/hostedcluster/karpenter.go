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

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *HostedClusterReconciler) reconcileKarpenterOperator(cpContext controlplanecomponent.ControlPlaneContext, createOrUpdate upsert.CreateOrUpdateFN, hcluster *hyperv1.HostedCluster, hypershiftOperatorImage, controlPlaneOperatorImage string) error {
	// TODO(jkyros): I rearranged this so we always reconcile so it can at least attempt to disable if it's turned off. I was planning on moving the KubeletConfig configmap creation
	// into the karpenter-operator as part of the KubeletConfig work so this should get cleaner.
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

	if !karpenterutil.IsKarpenterEnabled(hcluster.Spec.AutoNode) {

		// Reconcile Karpenter so it has a chance to clean up, the predicate on the component
		// will ensure it's deleted
		if err := karpenterv2.NewComponent().Reconcile(cpContext); err != nil {
			return fmt.Errorf("failed to reconcile karpenter component: %w", err)
		}

		// When Karpenter is disabled, clear stale node-counts from the HCP.
		// HCP.Status.AutoNode holds NodeCount/NodeClaimCount written by the karpenter-operator
		// while it was running. Since the karpenter-operator only runs when enabled, it cannot
		// clear this itself.
		if cpContext.HCP.Status.AutoNode != nil {
			patch := client.MergeFrom(cpContext.HCP.DeepCopy())
			cpContext.HCP.Status.AutoNode = nil
			if err := cpContext.Client.Status().Patch(cpContext, cpContext.HCP, patch); err != nil {
				return fmt.Errorf("failed to clear AutoNode status: %w", err)
			}
		}
		// Also delete the taint ConfigMap — it is only valid while Karpenter is enabled.
		if _, err := hyperutil.DeleteIfNeeded(cpContext, r.Client, &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      karpenterutil.KarpenterTaintConfigMapName,
				Namespace: cpContext.HCP.Namespace,
			},
		}); err != nil {
			return fmt.Errorf("failed to delete karpenter taint configmap: %w", err)
		}
	}

	karpenteroperator := karpenteroperatorv2.NewComponent(&karpenteroperatorv2.KarpenterOperatorOptions{
		HyperShiftOperatorImage:   hypershiftOperatorImage,
		ControlPlaneOperatorImage: controlPlaneOperatorImage,
		IgnitionEndpoint:          hcluster.Status.IgnitionEndpoint,
	})

	// Always reconcile the Karpenter Operator so it has a chance to clean up, the predicate on
	// the component will ensure it's deleted
	if err := karpenteroperator.Reconcile(cpContext); err != nil {
		return fmt.Errorf("failed to reconcile karpenter-operator component: %w", err)
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
		condition.Status = metav1.ConditionUnknown
		condition.Reason = hyperv1.AutoNodeEvaluationFailedReason
		condition.Message = fmt.Sprintf("failed to list ControlPlaneComponents: %v", err)
		return condition
	}

	// Grab all of our karpenter components
	var karpenterComponents []hyperv1.ControlPlaneComponent
	for _, c := range componentList.Items {
		if c.Name == karpenteroperatorv2.ComponentName || c.Name == karpenterv2.ComponentName {
			karpenterComponents = append(karpenterComponents, c)
		}
	}

	if karpenterEnabled {
		// Check if they're there
		if len(karpenterComponents) < 2 {
			condition.Status = metav1.ConditionFalse
			condition.Reason = hyperv1.AutoNodeProgressingReason
			condition.Message = "AutoNode is being enabled: waiting for components to be created"
			return condition
		}
		// Check if they're ready
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
		// Report the things that aren't ready
		if len(notReady) > 0 {
			condition.Status = metav1.ConditionFalse
			condition.Reason = hyperv1.AutoNodeProgressingReason
			condition.Message = fmt.Sprintf("AutoNode is being enabled: %s", strings.Join(notReady, "; "))
			return condition
		}
		// Otherwise report ready
		condition.Status = metav1.ConditionTrue
		condition.Reason = hyperv1.AsExpectedReason
		condition.Message = "AutoNode is ready"
		return condition
	}

	// Karpenter not enabled — check if Deployments are still terminating.
	// The ControlPlaneComponent CR is deleted synchronously before the pod actually terminates,
	// so we check Deployment existence to accurately track teardown progress.
	var runningDeployments []string
	for _, name := range []string{karpenterv2.ComponentName, karpenteroperatorv2.ComponentName} {
		dep := &appsv1.Deployment{}
		err := r.Client.Get(ctx, client.ObjectKey{Namespace: hcpNamespace, Name: name}, dep)
		if err == nil {
			runningDeployments = append(runningDeployments, name)
		} else if !apierrors.IsNotFound(err) {
			condition.Status = metav1.ConditionUnknown
			condition.Reason = hyperv1.AutoNodeEvaluationFailedReason
			condition.Message = fmt.Sprintf("failed to check karpenter deployments: %v", err)
			return condition
		}
	}

	if len(runningDeployments) > 0 {
		condition.Status = metav1.ConditionFalse
		condition.Reason = hyperv1.AutoNodeProgressingReason
		condition.Message = fmt.Sprintf("AutoNode is being disabled: waiting for deployments to be removed: %s", strings.Join(runningDeployments, ", "))
		return condition
	}

	condition.Status = metav1.ConditionFalse
	condition.Reason = hyperv1.AutoNodeNotConfiguredReason
	condition.Message = "AutoNode provisioner is not configured"
	return condition
}
