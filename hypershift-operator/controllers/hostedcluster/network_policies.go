package hostedcluster

import (
	"context"
	"fmt"

	"github.com/blang/semver"
	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/networkpolicy"
	"github.com/openshift/hypershift/support/capabilities"
	"github.com/openshift/hypershift/support/upsert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *HostedClusterReconciler) reconcileNetworkPolicies(ctx context.Context, createOrUpdate upsert.CreateOrUpdateFN, hcluster *hyperv1.HostedCluster, hcp *hyperv1.HostedControlPlane, version semver.Version, controlPlaneOperatorAppliesManagementKASNetworkPolicyLabel bool) error {
	controlPlaneNamespaceName := manifests.HostedControlPlaneNamespace(hcluster.Namespace, hcluster.Name).Name

	// Reconcile openshift-ingress Network Policy
	policy := networkpolicy.OpenshiftIngressNetworkPolicy(controlPlaneNamespaceName)
	if _, err := createOrUpdate(ctx, r.Client, policy, func() error {
		return hostedcontrolplane.ReconcileOpenshiftIngressNetworkPolicy(policy)
	}); err != nil {
		return fmt.Errorf("failed to reconcile ingress network policy: %w", err)
	}

	// Reconcile same-namespace Network Policy
	policy = networkpolicy.SameNamespaceNetworkPolicy(controlPlaneNamespaceName)
	if _, err := createOrUpdate(ctx, r.Client, policy, func() error {
		return hostedcontrolplane.ReconcileSameNamespaceNetworkPolicy(policy)
	}); err != nil {
		return fmt.Errorf("failed to reconcile same namespace network policy: %w", err)
	}

	// Reconcile KAS Network Policy
	var managementClusterNetwork *configv1.Network
	if r.ManagementClusterCapabilities.Has(capabilities.CapabilityNetworks) {
		managementClusterNetwork = &configv1.Network{ObjectMeta: metav1.ObjectMeta{Name: "cluster"}}
		if err := r.Get(ctx, client.ObjectKeyFromObject(managementClusterNetwork), managementClusterNetwork); err != nil {
			return fmt.Errorf("failed to get management cluster network config: %w", err)
		}
	}
	policy = networkpolicy.KASNetworkPolicy(controlPlaneNamespaceName)
	if _, err := createOrUpdate(ctx, r.Client, policy, func() error {
		return hostedcontrolplane.ReconcileKASNetworkPolicy(policy, hcp, r.ManagementClusterCapabilities.Has(capabilities.CapabilityDNS), managementClusterNetwork)
	}); err != nil {
		return fmt.Errorf("failed to reconcile kube-apiserver network policy: %w", err)
	}

	// Reconcile management KAS network policy
	kubernetesEndpoint := &corev1.Endpoints{ObjectMeta: metav1.ObjectMeta{Name: "kubernetes", Namespace: "default"}}
	if err := r.Get(ctx, client.ObjectKeyFromObject(kubernetesEndpoint), kubernetesEndpoint); err != nil {
		return fmt.Errorf("failed to get management cluster network config: %w", err)
	}

	// ManagementKASNetworkPolicy restricts traffic for pods unless they have a known annotation.
	if controlPlaneOperatorAppliesManagementKASNetworkPolicyLabel && hcluster.Spec.Platform.Type == hyperv1.AWSPlatform {
		policy = networkpolicy.ManagementKASNetworkPolicy(controlPlaneNamespaceName)
		if _, err := createOrUpdate(ctx, r.Client, policy, func() error {
			return hostedcontrolplane.ReconcileManagementKASNetworkPolicy(policy, managementClusterNetwork, kubernetesEndpoint, r.ManagementClusterCapabilities.Has(capabilities.CapabilityDNS))
		}); err != nil {
			return fmt.Errorf("failed to reconcile kube-apiserver network policy: %w", err)
		}

	}

	// Reconcile openshift-monitoring Network Policy
	policy = networkpolicy.OpenshiftMonitoringNetworkPolicy(controlPlaneNamespaceName)
	if _, err := createOrUpdate(ctx, r.Client, policy, func() error {
		return hostedcontrolplane.ReconcileOpenshiftMonitoringNetworkPolicy(policy)
	}); err != nil {
		return fmt.Errorf("failed to reconcile monitoring network policy: %w", err)
	}

	// Reconcile private-router Network Policy
	if hcluster.Spec.Platform.Type == hyperv1.AWSPlatform {
		policy = networkpolicy.PrivateRouterNetworkPolicy(controlPlaneNamespaceName)
		// TODO: Network policy code should move to the control plane operator. For now,
		// only setup ingress rules (and not egress rules) when version is < 4.14
		ingressOnly := version.Major == 4 && version.Minor < 14
		if _, err := createOrUpdate(ctx, r.Client, policy, func() error {
			return hostedcontrolplane.ReconcilePrivateRouterNetworkPolicy(policy, kubernetesEndpoint, r.ManagementClusterCapabilities.Has(capabilities.CapabilityDNS), managementClusterNetwork, ingressOnly)
		}); err != nil {
			return fmt.Errorf("failed to reconcile private router network policy: %w", err)
		}
	} else if hcluster.Spec.Platform.Type == hyperv1.KubevirtPlatform {
		if hcluster.Spec.Platform.Kubevirt.Credentials == nil {
			// network policy is being set on centralized infra only, not on external infra
			policy = networkpolicy.VirtLauncherNetworkPolicy(controlPlaneNamespaceName)
			if _, err := createOrUpdate(ctx, r.Client, policy, func() error {
				return hostedcontrolplane.ReconcileVirtLauncherNetworkPolicy(policy, hcp, managementClusterNetwork)
			}); err != nil {
				return fmt.Errorf("failed to reconcile virt launcher policy: %w", err)
			}
		}
	}

	for _, svc := range hcluster.Spec.Services {
		switch svc.Service {
		case hyperv1.OAuthServer:
			if svc.ServicePublishingStrategy.Type == hyperv1.NodePort {
				// Reconcile nodeport-oauth Network Policy
				policy = networkpolicy.NodePortOauthNetworkPolicy(controlPlaneNamespaceName)
				if _, err := createOrUpdate(ctx, r.Client, policy, func() error {
					return hostedcontrolplane.ReconcileNodePortOauthNetworkPolicy(policy)
				}); err != nil {
					return fmt.Errorf("failed to reconcile oauth server nodeport network policy: %w", err)
				}
			}
		case hyperv1.Ignition:
			if svc.ServicePublishingStrategy.Type == hyperv1.NodePort {
				// Reconcile nodeport-ignition Network Policy
				policy = networkpolicy.NodePortIgnitionNetworkPolicy(controlPlaneNamespaceName)
				if _, err := createOrUpdate(ctx, r.Client, policy, func() error {
					return hostedcontrolplane.ReconcileNodePortIgnitionNetworkPolicy(policy)
				}); err != nil {
					return fmt.Errorf("failed to reconcile ignition nodeport network policy: %w", err)
				}
			}
		case hyperv1.Konnectivity:
			if svc.ServicePublishingStrategy.Type == hyperv1.NodePort {
				// Reconcile nodeport-konnectivity Network Policy
				policy = networkpolicy.NodePortKonnectivityNetworkPolicy(controlPlaneNamespaceName)
				if _, err := createOrUpdate(ctx, r.Client, policy, func() error {
					return hostedcontrolplane.ReconcileNodePortKonnectivityNetworkPolicy(policy)
				}); err != nil {
					return fmt.Errorf("failed to reconcile konnectivity nodeport network policy: %w", err)
				}
			}
		}
	}

	return nil
}
