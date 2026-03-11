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

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	karpenteroperatorv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/karpenteroperator"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/controlplaneoperator"
	controlplanecomponent "github.com/openshift/hypershift/support/controlplane-component"
	karpenterutil "github.com/openshift/hypershift/support/karpenter"
	"github.com/openshift/hypershift/support/upsert"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func (r *HostedClusterReconciler) reconcileKarpenterOperator(cpContext controlplanecomponent.ControlPlaneContext, createOrUpdate upsert.CreateOrUpdateFN, hcluster *hyperv1.HostedCluster, hypershiftOperatorImage, controlPlaneOperatorImage string) error {
	if !karpenterutil.IsKarpenterEnabled(hcluster.Spec.AutoNode) || hcluster.Status.KubeConfig == nil || hcluster.Status.IgnitionEndpoint == "" {
		return nil
	}

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

	// TODO(alberto): Ensure deletion if autoNode is disabled.

	// Run karpenter Operator to manage CRs management and guest side.

	karpenteroperator := karpenteroperatorv2.NewComponent(&karpenteroperatorv2.KarpenterOperatorOptions{
		HyperShiftOperatorImage:   hypershiftOperatorImage,
		ControlPlaneOperatorImage: controlPlaneOperatorImage,
		IgnitionEndpoint:          hcluster.Status.IgnitionEndpoint,
	})

	if err := karpenteroperator.Reconcile(cpContext); err != nil {
		return fmt.Errorf("failed to reconcile controlplane operator component: %w", err)
	}

	return nil
}

// resolveKarpenterFinalizer removes the karpenter finalizer from the HCP
// when the karpenter-operator cannot do so itself because the guest KAS is down.
//
// This breaks the deadlock where:
//  1. The HostedCluster deletion waits for the HCP to be removed
//  2. The HCP can't be removed because it has the karpenter finalizer
//  3. The karpenter-operator can't remove the finalizer because the guest KAS is down
//     and it depends on guest-side watches to function
func (r *HostedClusterReconciler) resolveKarpenterFinalizer(ctx context.Context, hc *hyperv1.HostedCluster) error {
	if !karpenterutil.IsKarpenterEnabled(hc.Spec.AutoNode) {
		return nil
	}

	log := ctrl.LoggerFrom(ctx)
	cpNamespace := manifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name)

	hcp := controlplaneoperator.HostedControlPlane(cpNamespace, hc.Name)
	if err := r.Get(ctx, client.ObjectKeyFromObject(hcp), hcp); err != nil {
		return client.IgnoreNotFound(err)
	}

	if !controllerutil.ContainsFinalizer(hcp, karpenterutil.KarpenterFinalizer) {
		return nil
	}

	shouldSkip, reason, err := r.karpenterCleanupTracker.ShouldSkipCleanup(ctx, hcp, r.Client, true)
	if err != nil {
		return fmt.Errorf("failed to check cleanup status: %w", err)
	}

	if !shouldSkip {
		return nil
	}

	log.Info("Force-removing karpenter finalizer to unblock HCP deletion. Orphaned cloud resources may require manual cleanup.",
		"hcp", client.ObjectKeyFromObject(hcp).String(), "reason", reason)

	original := hcp.DeepCopy()
	controllerutil.RemoveFinalizer(hcp, karpenterutil.KarpenterFinalizer)
	if err := r.Patch(ctx, hcp, client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{})); err != nil {
		return fmt.Errorf("failed to remove karpenter finalizer: %w", err)
	}

	log.Info("Successfully removed karpenter finalizer from HCP")
	return nil
}
