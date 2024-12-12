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
	karpenteroperatormanifest "github.com/openshift/hypershift/karpenter-operator/manifests"
	"github.com/openshift/hypershift/support/upsert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
)

func (r *HostedClusterReconciler) reconcileKarpenterOperator(ctx context.Context, createOrUpdate upsert.CreateOrUpdateFN, hcluster *hyperv1.HostedCluster, hcp *hyperv1.HostedControlPlane, hypershiftOperatorImage, controlPlaneOperatorImage string) error {
	if hcluster.Spec.AutoNode == nil || hcluster.Spec.AutoNode.Provisioner.Name != hyperv1.ProvisionerKarpeneter ||
		hcluster.Spec.AutoNode.Provisioner.Karpenter.Platform != hyperv1.AWSPlatform {
		return nil
	}

	// Generate configMap with KubeletConfig to register Nodes with karpenter expected taint.
	taintConfigName := "set-karpenter-taint"
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      taintConfigName,
			Namespace: hcluster.Namespace,
		},
	}

	kubeletConfig := `apiVersion: machineconfiguration.openshift.io/v1
kind: KubeletConfig
metadata:
  name: set-karpenter-taint
spec:
  kubeletConfig:
    registerWithTaints:
      - key: "karpenter.sh/unregistered"
        value: "true"
        effect: "NoExecute"`

	_, err := createOrUpdate(ctx, r.Client, configMap, func() error {
		configMap.Data = map[string]string{
			"config": kubeletConfig,
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to create configmap: %w", err)
	}

	// Managed a NodePool to generate userData for Karpenter instances
	// TODO(alberto): consider invoking the token library to manage the karpenter userdata programatically,
	// instead of via NodePool API.
	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "karpenter",
			Namespace: hcluster.Namespace,
		},
	}
	_, err = createOrUpdate(ctx, r.Client, nodePool, func() error {
		nodePool.Spec = hyperv1.NodePoolSpec{
			ClusterName: hcluster.Name,
			Replicas:    ptr.To(int32(0)),
			Release:     hcluster.Spec.Release,
			Config: []corev1.LocalObjectReference{
				{
					Name: taintConfigName,
				},
			},
			Management: hyperv1.NodePoolManagement{
				UpgradeType: hyperv1.UpgradeTypeReplace,
				Replace: &hyperv1.ReplaceUpgrade{
					Strategy: hyperv1.UpgradeStrategyRollingUpdate,
					RollingUpdate: &hyperv1.RollingUpdate{
						MaxUnavailable: ptr.To(intstr.FromInt(0)),
						MaxSurge:       ptr.To(intstr.FromInt(1)),
					},
				},
				AutoRepair: false,
			},
			Platform: hyperv1.NodePoolPlatform{
				Type: hyperv1.AWSPlatform,
				AWS: &hyperv1.AWSNodePoolPlatform{
					InstanceType: "m5.large",
					Subnet: hyperv1.AWSResourceReference{
						ID: ptr.To("subnet-none"),
					},
				},
			},
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to create configmap: %w", err)
	}
	// TODO(alberto): Ensure deletion if autoNode is disabled.

	// Run karpenter Operator to manage CRs management and guest side.
	if err := karpenteroperatormanifest.ReconcileKarpenterOperator(ctx, createOrUpdate, r.Client, hypershiftOperatorImage, controlPlaneOperatorImage, hcp); err != nil {
		return err
	}
	return nil
}
