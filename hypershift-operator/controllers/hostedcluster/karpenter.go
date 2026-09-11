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
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/controlplaneoperator"
	"github.com/openshift/hypershift/support/capabilities"
	controlplanecomponent "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/k8sutil"
	karpenterutil "github.com/openshift/hypershift/support/karpenter"
	"github.com/openshift/hypershift/support/statuspatching"
	"github.com/openshift/hypershift/support/upsert"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/utils/ptr"
	"k8s.io/utils/set"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	tuningConfigDataKey       = "tuning"
	mirroredTuningConfigLabel = "hypershift.openshift.io/mirrored-tuning-config"
)

func (r *HostedClusterReconciler) reconcileKarpenterOperator(cpContext controlplanecomponent.ControlPlaneContext, hcluster *hyperv1.HostedCluster, hypershiftOperatorImage, controlPlaneOperatorImage string) error {
	// The taint ConfigMap (set-karpenter-taint) is created by the karpenter-operator itself.

	if !karpenterutil.IsKarpenterEnabled(hcluster.Spec.AutoNode) {

		// Reconcile Karpenter so it has a chance to clean up, the predicate on the component
		// will ensure it's deleted
		if err := karpenterv2.NewComponent().Reconcile(cpContext); err != nil {
			return fmt.Errorf("failed to reconcile karpenter component: %w", err)
		}

		// Clear stale node-counts from the HCP. HCP.Status.AutoNode holds NodeCount/NodeClaimCount
		// written by the karpenter-operator while it was running. Since the karpenter-operator only
		// runs when enabled, it cannot clear this itself.
		// No pre-check on cpContext.HCP.Status.AutoNode here: it's a cache read from earlier in the
		// reconcile loop and can be stale relative to the live object, silently skipping the patch.
		// PatchStatus re-fetches and no-ops via DeepEqual when there's nothing to clear, so the guard
		// only adds a staleness risk without saving real work.
		if err := statuspatching.PatchStatus(cpContext, cpContext.Client, cpContext.HCP, func() error {
			cpContext.HCP.Status.AutoNode = hyperv1.AutoNodeStatus{}
			return nil
		}); err != nil {
			return fmt.Errorf("failed to clear AutoNode status: %w", err)
		}
		// Also delete the taint ConfigMap — it is only valid while Karpenter is enabled.
		if _, err := k8sutil.DeleteIfNeeded(cpContext, r.Client, &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      karpenterutil.KarpenterTaintConfigMapName,
				Namespace: cpContext.HCP.Namespace,
			},
		}); err != nil {
			return fmt.Errorf("failed to delete karpenter taint configmap: %w", err)
		}
	}

	karpenteroperator := karpenteroperatorv2.NewComponent(&karpenteroperatorv2.KarpenterOperatorOptions{
		HyperShiftOperatorImage:            hypershiftOperatorImage,
		ControlPlaneOperatorImage:          controlPlaneOperatorImage,
		IgnitionEndpoint:                   hcluster.Status.IgnitionEndpoint,
		StandaloneKarpenterOperatorEnabled: karpenterutil.IsStandaloneKarpenterOperatorEnabled(),
	})

	// Always reconcile the Karpenter Operator so it has a chance to clean up, the predicate on
	// the component will ensure it's deleted
	if err := karpenteroperator.Reconcile(cpContext); err != nil {
		return fmt.Errorf("failed to reconcile karpenter-operator component: %w", err)
	}

	return nil
}

// resolveKarpenterFinalizer removes the karpenter finalizer from the HCP
// when the karpenter-operator cannot do so itself. This covers two scenarios:
//
//  1. The guest KAS is down during HostedCluster deletion, so the karpenter-operator
//     cannot reach its guest-side watches to process the finalizer.
//  2. AutoNode was disabled before the HostedCluster was deleted, so the karpenter-operator
//     deployment was removed and cannot process the finalizer at all.
//
// Without this fallback the HCP would be stuck in terminating with the karpenter finalizer
// blocking deletion indefinitely.
func (r *HostedClusterReconciler) resolveKarpenterFinalizer(ctx context.Context, hc *hyperv1.HostedCluster) error {
	log := ctrl.LoggerFrom(ctx)
	cpNamespace := manifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name)

	hcp := controlplaneoperator.HostedControlPlane(cpNamespace, hc.Name)
	if err := r.Get(ctx, client.ObjectKeyFromObject(hcp), hcp); err != nil {
		return client.IgnoreNotFound(err)
	}

	if !controllerutil.ContainsFinalizer(hcp, karpenterutil.KarpenterFinalizer) {
		return nil
	}

	// When AutoNode is still enabled, defer to the karpenter-operator for graceful
	// cleanup — only force-remove the finalizer once the guest KAS is down and the
	// operator can no longer reach its watches.
	// When AutoNode is disabled, the karpenter-operator deployment is already gone,
	// so there is no controller to process the finalizer and we must remove it immediately.
	if karpenterutil.IsKarpenterEnabled(hc.Spec.AutoNode) {
		kasAvailable, err := isKASAvailable(ctx, hcp.Namespace, r.Client)
		if err != nil {
			return fmt.Errorf("failed to check KAS availability: %w", err)
		}
		if kasAvailable {
			return nil
		}
	}

	original := hcp.DeepCopy()
	if !controllerutil.RemoveFinalizer(hcp, karpenterutil.KarpenterFinalizer) {
		return nil
	}

	log.Info("Force-removing karpenter finalizer to unblock HCP deletion. Orphaned cloud resources may require manual cleanup.",
		"hcp", client.ObjectKeyFromObject(hcp).String())

	if err := r.Patch(ctx, hcp, client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{})); err != nil {
		return fmt.Errorf("failed to remove karpenter finalizer: %w", err)
	}

	log.Info("Successfully removed karpenter finalizer from HCP")
	return nil
}

// isKASAvailable checks if the kube-apiserver deployment in the control plane namespace
// has the Available condition set to True.
func isKASAvailable(ctx context.Context, cpNamespace string, c client.Client) (bool, error) {
	deployment := &appsv1.Deployment{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: cpNamespace, Name: "kube-apiserver"}, deployment); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	for _, cond := range deployment.Status.Conditions {
		if cond.Type == appsv1.DeploymentAvailable {
			return cond.Status == corev1.ConditionTrue, nil
		}
	}
	return false, nil
}

// reconcileAutoNodeEnabledCondition returns the AutoNodeEnabled condition reflecting both the desired
// state (spec) and the actual rollout progress of the Karpenter ControlPlaneComponent resources.
//
// States:
//   - True  / AsExpected          — Karpenter enabled in spec AND both components fully rolled out.
//   - False / AutoNodeProgressing — Enable or disable operation is in progress.
//   - False / AutoNodeNotConfigured — Karpenter not in spec AND no components present.
//
// reconcileAutoNodeEnabledCondition returns the AutoNodeEnabled condition and whether
// the caller should requeue to poll for progress. The second return value is true when
// an enable or disable operation is still in flight; the HC reconciler does not watch
// ControlPlaneComponent resources, so a periodic requeue is needed to pick up status
// changes from the karpenter-operator's CPC updates.
func (r *HostedClusterReconciler) reconcileAutoNodeEnabledCondition(ctx context.Context, hcluster *hyperv1.HostedCluster, hcpNamespace string) (metav1.Condition, bool) {
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
		return condition, false
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
			return condition, true
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
			return condition, true
		}
		// Otherwise report ready
		condition.Status = metav1.ConditionTrue
		condition.Reason = hyperv1.AsExpectedReason
		condition.Message = "AutoNode is ready"
		return condition, false
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
			return condition, false
		}
	}

	if len(runningDeployments) > 0 {
		condition.Status = metav1.ConditionFalse
		condition.Reason = hyperv1.AutoNodeProgressingReason
		condition.Message = fmt.Sprintf("AutoNode is being disabled: waiting for deployments to be removed: %s", strings.Join(runningDeployments, ", "))
		return condition, true
	}

	condition.Status = metav1.ConditionFalse
	condition.Reason = hyperv1.AutoNodeNotConfiguredReason
	condition.Message = "AutoNode provisioner is not configured"
	return condition, false
}

func hostedClusterNamespacedRef(hcluster *hyperv1.HostedCluster) string {
	return fmt.Sprintf("%s/%s", hcluster.Namespace, hcluster.Name)
}

// reconcileTuningConfigSync mirrors tuning ConfigMaps from the HostedCluster namespace
// into the hosted control plane namespace. Any ConfigMap in the HostedCluster namespace
// with a tuning data key is copied into the Karpenter cluster's HCP namespace on reconcile.
func (r *HostedClusterReconciler) reconcileTuningConfigSync(
	ctx context.Context,
	hcluster *hyperv1.HostedCluster,
	createOrUpdate upsert.CreateOrUpdateFN,
	controlPlaneNamespace string,
) error {
	if !karpenterutil.IsKarpenterEnabled(hcluster.Spec.AutoNode) ||
		!capabilities.IsNodeTuningCapabilityEnabled(hcluster.Spec.Capabilities) {
		return r.deleteMirroredTuningConfigMaps(ctx, controlPlaneNamespace)
	}

	tuningConfigNames, err := r.collectKarpenterTuningConfigMapNames(ctx, hcluster)
	if err != nil {
		return err
	}

	clusterRef := hostedClusterNamespacedRef(hcluster)
	for name := range tuningConfigNames {
		sourceCM := &corev1.ConfigMap{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: hcluster.Namespace, Name: name}, sourceCM); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return fmt.Errorf("failed to get tuning configmap %s/%s: %w", hcluster.Namespace, name, err)
		}
		if _, ok := sourceCM.Data[tuningConfigDataKey]; !ok {
			continue
		}
		if err := ensureReferencedResourceAnnotation(ctx, r.Client, hcluster.Name, sourceCM); err != nil {
			return fmt.Errorf("failed to set referenced resource annotation on tuning configmap %s: %w", name, err)
		}
		destCM := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: controlPlaneNamespace,
			},
		}
		if _, err := createOrUpdate(ctx, r.Client, destCM, func() error {
			if destCM.Labels == nil {
				destCM.Labels = make(map[string]string)
			}
			for k, v := range sourceCM.Labels {
				destCM.Labels[k] = v
			}
			destCM.Labels[mirroredTuningConfigLabel] = "true"
			destCM.Labels[karpenterutil.ManagedByKarpenterLabel] = "true"
			if destCM.Annotations == nil {
				destCM.Annotations = make(map[string]string)
			}
			destCM.Annotations[k8sutil.HostedClusterAnnotation] = clusterRef
			destCM.Data = sourceCM.Data
			destCM.BinaryData = sourceCM.BinaryData
			destCM.Immutable = ptr.To(false)
			return nil
		}); err != nil {
			return fmt.Errorf("failed to reconcile mirrored tuning configmap %s/%s: %w", controlPlaneNamespace, name, err)
		}
	}

	return r.deleteMirroredTuningConfigMaps(ctx, controlPlaneNamespace, tuningConfigNames)
}

func (r *HostedClusterReconciler) collectKarpenterTuningConfigMapNames(ctx context.Context, hcluster *hyperv1.HostedCluster) (set.Set[string], error) {
	names := set.New[string]()

	configMapList := &corev1.ConfigMapList{}
	if err := r.List(ctx, configMapList, &client.ListOptions{
		Namespace: hcluster.Namespace,
	}); err != nil {
		return nil, fmt.Errorf("failed to list tuning configmaps: %w", err)
	}
	for i := range configMapList.Items {
		configMap := &configMapList.Items[i]
		if _, ok := configMap.Data[tuningConfigDataKey]; ok {
			names.Insert(configMap.Name)
		}
	}

	return names, nil
}

func (r *HostedClusterReconciler) deleteMirroredTuningConfigMaps(
	ctx context.Context,
	controlPlaneNamespace string,
	keep ...set.Set[string],
) error {
	keepSet := set.New[string]()
	for _, s := range keep {
		keepSet = keepSet.Union(s)
	}

	configMapList := &corev1.ConfigMapList{}
	if err := r.List(ctx, configMapList, &client.ListOptions{
		Namespace: controlPlaneNamespace,
		LabelSelector: labels.SelectorFromSet(labels.Set{
			mirroredTuningConfigLabel: "true",
		}),
	}); err != nil {
		return fmt.Errorf("failed to list mirrored tuning configmaps: %w", err)
	}
	for i := range configMapList.Items {
		configMap := &configMapList.Items[i]
		if keepSet.Has(configMap.Name) {
			continue
		}
		if _, err := k8sutil.DeleteIfNeeded(ctx, r.Client, configMap); err != nil {
			return fmt.Errorf("failed to delete mirrored tuning configmap %s: %w", configMap.Name, err)
		}
	}
	return nil
}
