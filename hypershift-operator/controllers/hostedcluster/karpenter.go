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
	hyperkarpenterv1 "github.com/openshift/hypershift/api/karpenter/v1beta1"
	karpenteroperatorv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/karpenteroperator"
	"github.com/openshift/hypershift/hypershift-operator/controllers/nodepool"
	haproxy "github.com/openshift/hypershift/hypershift-operator/controllers/nodepool/apiserver-haproxy"
	controlplanecomponent "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/upsert"
	hyperutil "github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func (r *HostedClusterReconciler) reconcileKarpenterOperator(cpContext controlplanecomponent.ControlPlaneContext, createOrUpdate upsert.CreateOrUpdateFN, hcluster *hyperv1.HostedCluster, hcp *hyperv1.HostedControlPlane, hypershiftOperatorImage, controlPlaneOperatorImage string) error {
	if hcluster.Spec.AutoNode == nil || hcluster.Spec.AutoNode.Provisioner.Name != hyperv1.ProvisionerKarpeneter ||
		hcluster.Spec.AutoNode.Provisioner.Karpenter.Platform != hyperv1.AWSPlatform || hcluster.Status.KubeConfig == nil {
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

	_, err := createOrUpdate(cpContext, r.Client, configMap, func() error {
		configMap.Data = map[string]string{
			"config": kubeletConfig,
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to create configmap: %w", err)
	}

	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      hyperkarpenterv1.KarpenterNodePool,
			Namespace: hcluster.Namespace,
		},
		Spec: hyperv1.NodePoolSpec{
			ClusterName: hcluster.Name,
			Replicas:    ptr.To(int32(0)),
			Release:     hcluster.Spec.Release,
			Config: []corev1.LocalObjectReference{
				{
					Name: taintConfigName,
				},
			},
			Arch: hyperv1.ArchitectureAMD64, // used to find default AMI
		},
	}

	err = r.RegistryProvider.Reconcile(cpContext, r.Client)
	if err != nil {
		return err
	}

	pullSecretBytes, err := hyperutil.GetPullSecretBytes(cpContext, r.Client, hcluster)
	if err != nil {
		return err
	}

	releaseImage, err := r.RegistryProvider.GetReleaseProvider().Lookup(cpContext, nodePool.Spec.Release.Image, pullSecretBytes)
	if err != nil {
		return err
	}

	if err := r.reconcileKarpenterUserDataSecret(cpContext, hcluster, releaseImage, nodePool, r.RegistryProvider.GetReleaseProvider(), r.RegistryProvider.GetMetadataProvider()); err != nil {
		return err
	}

	// TODO(alberto): Ensure deletion if autoNode is disabled.

	// Run karpenter Operator to manage CRs management and guest side.

	karpenteroperator := karpenteroperatorv2.NewComponent(&karpenteroperatorv2.KarpenterOperatorOptions{
		HyperShiftOperatorImage:   hypershiftOperatorImage,
		ControlPlaneOperatorImage: controlPlaneOperatorImage,
	})

	if err := karpenteroperator.Reconcile(cpContext); err != nil {
		return fmt.Errorf("failed to reconcile controlplane operator component: %w", err)
	}

	return nil
}

func (r *HostedClusterReconciler) reconcileKarpenterUserDataSecret(cpContext context.Context, hcluster *hyperv1.HostedCluster, releaseImage *releaseinfo.ReleaseImage, nodePool *hyperv1.NodePool, releaseProvider releaseinfo.Provider, imageMetadataProvider hyperutil.ImageMetadataProvider) error {
	haProxyImage, ok := releaseImage.ComponentImages()[haproxy.HAProxyRouterImageName]
	if !ok {
		return fmt.Errorf("release image doesn't have %s image", haproxy.HAProxyRouterImageName)
	}

	haproxy := haproxy.HAProxy{
		Client:                  r.Client,
		HAProxyImage:            haProxyImage,
		HypershiftOperatorImage: r.HypershiftOperatorImage,
		ReleaseProvider:         releaseProvider,
		ImageMetadataProvider:   imageMetadataProvider,
	}
	haproxyRawConfig, err := haproxy.GenerateHAProxyRawConfig(cpContext, hcluster)
	if err != nil {
		return err
	}

	configGenerator, err := nodepool.NewConfigGenerator(cpContext, r.Client, hcluster, nodePool, releaseImage, haproxyRawConfig)
	if err != nil {
		return fmt.Errorf("failed to generate config: %w", err)
	}

	token, err := nodepool.NewToken(cpContext, configGenerator, &nodepool.CPOCapabilities{
		DecompressAndDecodeConfig: true,
	})
	if err != nil {
		return err
	}

	if err := token.Reconcile(cpContext); err != nil {
		return err
	}

	return nil
}
