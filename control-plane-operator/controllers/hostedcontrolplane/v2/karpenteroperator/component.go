package karpenteroperator

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	ComponentName = "karpenter-operator"
)

var _ component.ComponentOptions = &KarpenterOperatorOptions{}

type KarpenterOperatorOptions struct {
	HyperShiftOperatorImage   string
	ControlPlaneOperatorImage string
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (c *KarpenterOperatorOptions) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (c *KarpenterOperatorOptions) MultiZoneSpread() bool {
	return false
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (c *KarpenterOperatorOptions) NeedsManagementKASAccess() bool {
	return true
}

func NewComponent(options *KarpenterOperatorOptions) component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, options).
		WithAdaptFunction(options.adaptDeployment).
		WithManifestAdapter("karpenter-credentials.yaml",
			component.WithAdaptFunction(adaptCredentialsSecret),
		).
		WithManifestAdapter("podmonitor.yaml",
			component.WithAdaptFunction(adaptPodMonitor),
		).
		WithPredicate(predicate).
		InjectTokenMinterContainer(component.TokenMinterContainerOptions{
			TokenType:               component.CloudToken,
			ServiceAccountName:      "karpenter",
			ServiceAccountNameSpace: "kube-system",
			KubeconfigSecretName:    "service-network-admin-kubeconfig",
		}).
		InjectAvailabilityProberContainer(util.AvailabilityProberOpts{}).
		Build()
}

func predicate(cpContext component.WorkloadContext) (bool, error) {
	hcp := cpContext.HCP

	if hcp.Spec.AutoNode == nil || hcp.Spec.AutoNode.Provisioner == nil || hcp.Spec.AutoNode.Provisioner.Karpenter == nil ||
		hcp.Spec.AutoNode.Provisioner.Karpenter.Platform != hyperv1.AWSPlatform {
		return false, nil
	}

	// The deployment depends on the kubeconfig being reported.
	if hcp.Status.KubeConfig == nil {
		return false, nil
	}
	// Resolve the kubeconfig secret for HCCO which is used for karpenter for convenience.
	kubeConfigSecret := manifests.HCCOKubeconfigSecret(hcp.Namespace)
	err := cpContext.Client.Get(cpContext, client.ObjectKeyFromObject(kubeConfigSecret), kubeConfigSecret)
	if err != nil {
		return false, fmt.Errorf("failed to get hosted controlplane kubeconfig secret %q: %w", kubeConfigSecret.Name, err)
	}

	return true, nil
}
