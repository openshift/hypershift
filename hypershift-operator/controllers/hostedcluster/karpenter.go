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
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	karpenteroperatorv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/karpenteroperator"
	controlplanecomponent "github.com/openshift/hypershift/support/controlplane-component"
	karpenterutil "github.com/openshift/hypershift/support/karpenter"
	"github.com/openshift/hypershift/support/upsert"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
