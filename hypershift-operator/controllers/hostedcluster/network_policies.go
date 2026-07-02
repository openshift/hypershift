package hostedcluster

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"os"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/networkpolicy"
	"github.com/openshift/hypershift/hypershift-operator/controllers/sharedingress"
	"github.com/openshift/hypershift/support/awsutil"
	"github.com/openshift/hypershift/support/capabilities"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/k8sutil"
	"github.com/openshift/hypershift/support/netutil"
	"github.com/openshift/hypershift/support/rhobsmonitoring"
	"github.com/openshift/hypershift/support/upsert"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	utilsnet "k8s.io/utils/net"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/blang/semver"
	"github.com/go-logr/logr"
)

const (
	NeedManagementKASAccessLabel = "hypershift.openshift.io/need-management-kas-access"
	NeedMetricsServerAccessLabel = "hypershift.openshift.io/need-metrics-server-access"
)

func (r *HostedClusterReconciler) reconcileNetworkPolicies(ctx context.Context, log logr.Logger, createOrUpdate upsert.CreateOrUpdateFN, hcluster *hyperv1.HostedCluster, hcp *hyperv1.HostedControlPlane, version semver.Version, controlPlaneOperatorAppliesManagementKASNetworkPolicyLabel bool) error {
	controlPlaneNamespaceName := manifests.HostedControlPlaneNamespace(hcluster.Namespace, hcluster.Name)

	if err := r.reconcileIngressNetworkPolicy(ctx, createOrUpdate, hcp, controlPlaneNamespaceName); err != nil {
		return err
	}

	policy := networkpolicy.SameNamespaceNetworkPolicy(controlPlaneNamespaceName)
	if _, err := createOrUpdate(ctx, r.Client, policy, func() error {
		return reconcileSameNamespaceNetworkPolicy(policy)
	}); err != nil {
		return fmt.Errorf("failed to reconcile same namespace network policy: %w", err)
	}

	managementClusterNetwork, err := r.getManagementClusterNetwork(ctx)
	if err != nil {
		return err
	}

	policy = networkpolicy.KASNetworkPolicy(controlPlaneNamespaceName)
	if _, err := createOrUpdate(ctx, r.Client, policy, func() error {
		return reconcileKASNetworkPolicy(policy, hcluster, r.ManagementClusterCapabilities.Has(capabilities.CapabilityDNS), managementClusterNetwork)
	}); err != nil {
		return fmt.Errorf("failed to reconcile kube-apiserver network policy: %w", err)
	}

	var kasBlock []string
	if len(r.HCPEgressBlockCIDRs) > 0 {
		kasBlock = append(kasBlock, r.HCPEgressBlockCIDRs...)
	} else {
		//nolint:staticcheck // SA1019: corev1.Endpoints is intentionally used for backward compatibility
		kubernetesEndpoint := &corev1.Endpoints{ObjectMeta: metav1.ObjectMeta{Name: "kubernetes", Namespace: "default"}}
		if err := r.Get(ctx, client.ObjectKeyFromObject(kubernetesEndpoint), kubernetesEndpoint); err != nil {
			return fmt.Errorf("getting management cluster kubernetes endpoints: %w", err)
		}
		kasBlock = kasEndpointsToCIDRs(kubernetesEndpoint)
	}

	if err := r.reconcileManagementKASPolicies(ctx, createOrUpdate, hcluster, hcp, controlPlaneNamespaceName, managementClusterNetwork, kasBlock, controlPlaneOperatorAppliesManagementKASNetworkPolicyLabel); err != nil {
		return err
	}

	if netutil.UseSharedIngressHC(hcluster) {
		policy := networkpolicy.SharedIngressNetworkPolicy(controlPlaneNamespaceName)
		if _, err := createOrUpdate(ctx, r.Client, policy, func() error {
			return reconcileSharedIngressNetworkPolicy(policy, hcluster)
		}); err != nil {
			return fmt.Errorf("failed to reconcile sharedingress network policy: %w", err)
		}
	}

	policy = networkpolicy.OpenshiftMonitoringNetworkPolicy(controlPlaneNamespaceName)
	if _, err := createOrUpdate(ctx, r.Client, policy, func() error {
		return reconcileOpenshiftMonitoringNetworkPolicy(policy, hcluster)
	}); err != nil {
		return fmt.Errorf("failed to reconcile monitoring network policy: %w", err)
	}

	if err := r.reconcilePlatformNetworkPolicies(ctx, log, createOrUpdate, hcluster, kasBlock, managementClusterNetwork, version, controlPlaneNamespaceName); err != nil {
		return err
	}

	return r.reconcileServiceNetworkPolicies(ctx, createOrUpdate, hcluster, controlPlaneNamespaceName)
}

func (r *HostedClusterReconciler) reconcileIngressNetworkPolicy(ctx context.Context, createOrUpdate upsert.CreateOrUpdateFN, hcp *hyperv1.HostedControlPlane, controlPlaneNamespaceName string) error {
	// Only needed when routes are served by the management cluster's default ingress controller,
	// i.e., when routes are NOT labeled for the HCP router.
	policy := networkpolicy.OpenshiftIngressNetworkPolicy(controlPlaneNamespaceName)
	if !netutil.LabelHCPRoutes(hcp) {
		if _, err := createOrUpdate(ctx, r.Client, policy, func() error {
			return reconcileOpenshiftIngressNetworkPolicy(policy)
		}); err != nil {
			return fmt.Errorf("failed to reconcile ingress network policy: %w", err)
		}
	} else {
		if _, err := k8sutil.DeleteIfNeeded(ctx, r.Client, policy); err != nil {
			return fmt.Errorf("failed to delete ingress network policy: %w", err)
		}
	}
	return nil
}

func (r *HostedClusterReconciler) getManagementClusterNetwork(ctx context.Context) (*configv1.Network, error) {
	if !r.ManagementClusterCapabilities.Has(capabilities.CapabilityNetworks) {
		return nil, nil
	}
	managementClusterNetwork := &configv1.Network{ObjectMeta: metav1.ObjectMeta{Name: "cluster"}}
	if err := r.Get(ctx, client.ObjectKeyFromObject(managementClusterNetwork), managementClusterNetwork); err != nil {
		return nil, fmt.Errorf("failed to get management cluster network config: %w", err)
	}
	return managementClusterNetwork, nil
}

func (r *HostedClusterReconciler) reconcileManagementKASPolicies(ctx context.Context, createOrUpdate upsert.CreateOrUpdateFN, hcluster *hyperv1.HostedCluster, hcp *hyperv1.HostedControlPlane, controlPlaneNamespaceName string, managementClusterNetwork *configv1.Network, kasBlock []string, controlPlaneOperatorAppliesManagementKASNetworkPolicyLabel bool) error {
	if !controlPlaneOperatorAppliesManagementKASNetworkPolicyLabel || hcluster.Spec.Platform.Type != hyperv1.AWSPlatform {
		return nil
	}

	policy := networkpolicy.ManagementKASNetworkPolicy(controlPlaneNamespaceName)
	if _, err := createOrUpdate(ctx, r.Client, policy, func() error {
		return reconcileManagementKASNetworkPolicy(policy, managementClusterNetwork, kasBlock, r.ManagementClusterCapabilities.Has(capabilities.CapabilityDNS))
	}); err != nil {
		return fmt.Errorf("failed to reconcile kube-apiserver network policy: %w", err)
	}

	enableMetricsAccess := r.EnableCVOManagementClusterMetricsAccess || (os.Getenv(rhobsmonitoring.EnvironmentVariable) == "1" && awsutil.IsROSAHCP(hcp))
	if enableMetricsAccess {
		policy = networkpolicy.MetricsServerNetworkPolicy(controlPlaneNamespaceName)
		if _, err := createOrUpdate(ctx, r.Client, policy, func() error {
			return reconcileMetricsServerNetworkPolicy(policy, hcp)
		}); err != nil {
			return fmt.Errorf("failed to reconcile metrics server network policy: %w", err)
		}
	}
	return nil
}

func (r *HostedClusterReconciler) reconcilePlatformNetworkPolicies(ctx context.Context, log logr.Logger, createOrUpdate upsert.CreateOrUpdateFN, hcluster *hyperv1.HostedCluster, kasBlock []string, managementClusterNetwork *configv1.Network, version semver.Version, controlPlaneNamespaceName string) error {
	switch hcluster.Spec.Platform.Type {
	case hyperv1.AWSPlatform, hyperv1.AzurePlatform, hyperv1.GCPPlatform:
		policy := networkpolicy.PrivateRouterNetworkPolicy(controlPlaneNamespaceName)
		ingressOnly := version.Major == 4 && version.Minor < 14
		if _, err := createOrUpdate(ctx, r.Client, policy, func() error {
			return reconcilePrivateRouterNetworkPolicy(policy, hcluster, kasBlock, r.ManagementClusterCapabilities.Has(capabilities.CapabilityDNS), managementClusterNetwork, ingressOnly)
		}); err != nil {
			return fmt.Errorf("failed to reconcile private router network policy: %w", err)
		}
	case hyperv1.KubevirtPlatform:
		if hcluster.Spec.Platform.Kubevirt.Credentials == nil {
			// Centralized infra: policy targets the control plane namespace on the management cluster
			policy := networkpolicy.VirtLauncherNetworkPolicy(controlPlaneNamespaceName)
			if _, err := createOrUpdate(ctx, r.Client, policy, func() error {
				return reconcileVirtLauncherNetworkPolicy(log, policy, hcluster, managementClusterNetwork)
			}); err != nil {
				return fmt.Errorf("failed to reconcile virt launcher policy: %w", err)
			}
		} else {
			// External infra (credentials != nil): policy targets the infra namespace on the infrastructure cluster
			if err := r.reconcileExternalInfraVirtLauncherPolicy(ctx, log, createOrUpdate, hcluster); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *HostedClusterReconciler) reconcileServiceNetworkPolicies(ctx context.Context, createOrUpdate upsert.CreateOrUpdateFN, hcluster *hyperv1.HostedCluster, controlPlaneNamespaceName string) error {
	for _, svc := range hcluster.Spec.Services {
		switch svc.Service {
		case hyperv1.OAuthServer:
			if err := r.reconcileOAuthNetworkPolicies(ctx, createOrUpdate, hcluster, svc, controlPlaneNamespaceName); err != nil {
				return err
			}
		case hyperv1.Ignition:
			if err := r.reconcileIgnitionNetworkPolicies(ctx, createOrUpdate, hcluster, svc, controlPlaneNamespaceName); err != nil {
				return err
			}
		case hyperv1.Konnectivity:
			if err := r.reconcileKonnectivityNetworkPolicies(ctx, createOrUpdate, hcluster, svc, controlPlaneNamespaceName); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *HostedClusterReconciler) reconcileOAuthNetworkPolicies(ctx context.Context, createOrUpdate upsert.CreateOrUpdateFN, hcluster *hyperv1.HostedCluster, svc hyperv1.ServicePublishingStrategyMapping, controlPlaneNamespaceName string) error {
	if svc.ServicePublishingStrategy.Type == hyperv1.NodePort {
		policy := networkpolicy.NodePortOauthNetworkPolicy(controlPlaneNamespaceName)
		if _, err := createOrUpdate(ctx, r.Client, policy, func() error {
			return reconcileNodePortOauthNetworkPolicy(policy, hcluster)
		}); err != nil {
			return fmt.Errorf("failed to reconcile oauth server nodeport network policy: %w", err)
		}
	}
	if svc.ServicePublishingStrategy.Type == hyperv1.LoadBalancer {
		policy := networkpolicy.LoadBalancerOauthNetworkPolicy(controlPlaneNamespaceName)
		if _, err := createOrUpdate(ctx, r.Client, policy, func() error {
			return reconcileLoadBalancerOauthNetworkPolicy(policy)
		}); err != nil {
			return fmt.Errorf("failed to reconcile oauth server loadbalancer network policy: %w", err)
		}
	}
	return nil
}

func (r *HostedClusterReconciler) reconcileIgnitionNetworkPolicies(ctx context.Context, createOrUpdate upsert.CreateOrUpdateFN, hcluster *hyperv1.HostedCluster, svc hyperv1.ServicePublishingStrategyMapping, controlPlaneNamespaceName string) error {
	if svc.ServicePublishingStrategy.Type != hyperv1.NodePort {
		return nil
	}
	policy := networkpolicy.NodePortIgnitionNetworkPolicy(controlPlaneNamespaceName)
	if _, err := createOrUpdate(ctx, r.Client, policy, func() error {
		return reconcileNodePortIgnitionNetworkPolicy(policy, hcluster)
	}); err != nil {
		return fmt.Errorf("failed to reconcile ignition nodeport network policy: %w", err)
	}
	policy = networkpolicy.NodePortIgnitionProxyNetworkPolicy(controlPlaneNamespaceName)
	if _, err := createOrUpdate(ctx, r.Client, policy, func() error {
		return reconcileNodePortIgnitionProxyNetworkPolicy(policy, hcluster)
	}); err != nil {
		return fmt.Errorf("failed to reconcile ignition proxy nodeport network policy: %w", err)
	}
	return nil
}

func (r *HostedClusterReconciler) reconcileKonnectivityNetworkPolicies(ctx context.Context, createOrUpdate upsert.CreateOrUpdateFN, hcluster *hyperv1.HostedCluster, svc hyperv1.ServicePublishingStrategyMapping, controlPlaneNamespaceName string) error {
	if svc.ServicePublishingStrategy.Type != hyperv1.NodePort {
		return nil
	}
	policy := networkpolicy.NodePortKonnectivityNetworkPolicy(controlPlaneNamespaceName)
	if _, err := createOrUpdate(ctx, r.Client, policy, func() error {
		return reconcileNodePortKonnectivityNetworkPolicy(policy, hcluster)
	}); err != nil {
		return fmt.Errorf("failed to reconcile konnectivity nodeport network policy: %w", err)
	}
	policy = networkpolicy.NodePortKonnectivityKASNetworkPolicy(controlPlaneNamespaceName)
	if _, err := createOrUpdate(ctx, r.Client, policy, func() error {
		return reconcileNodePortKonnectivityKASNetworkPolicy(policy, hcluster)
	}); err != nil {
		return fmt.Errorf("failed to reconcile konnectivity nodeport network policy: %w", err)
	}
	return nil
}

func reconcileKASNetworkPolicy(policy *networkingv1.NetworkPolicy, hcluster *hyperv1.HostedCluster, _ bool, _ *configv1.Network) error {
	port := intstr.FromInt32(config.KASSVCPort)
	if hcluster.Spec.Platform.Type == hyperv1.IBMCloudPlatform {
		port = intstr.FromInt32(config.KASSVCIBMCloudPort)
	}
	protocol := corev1.ProtocolTCP
	policy.Spec.PolicyTypes = []networkingv1.PolicyType{networkingv1.PolicyTypeIngress}
	policy.Spec.Ingress = []networkingv1.NetworkPolicyIngressRule{
		{
			From: []networkingv1.NetworkPolicyPeer{},
			Ports: []networkingv1.NetworkPolicyPort{
				{
					Port:     &port,
					Protocol: &protocol,
				},
			},
		},
	}
	// We have to keep this in order to support 4.11 clusters where the KAS listen port == the external port
	if hcluster.Spec.Networking.APIServer != nil && hcluster.Spec.Networking.APIServer.Port != nil {
		externalPort := intstr.FromInt(int(*hcluster.Spec.Networking.APIServer.Port))
		if port.IntValue() != externalPort.IntValue() {
			policy.Spec.Ingress = append(policy.Spec.Ingress, networkingv1.NetworkPolicyIngressRule{
				From: []networkingv1.NetworkPolicyPeer{},
				Ports: []networkingv1.NetworkPolicyPort{
					{
						Port:     &externalPort,
						Protocol: &protocol,
					},
				},
			})
		}
	}

	policy.Spec.PodSelector = metav1.LabelSelector{
		MatchLabels: map[string]string{
			"app": "kube-apiserver",
		},
	}

	return nil
}

func reconcilePrivateRouterNetworkPolicy(policy *networkingv1.NetworkPolicy, _ *hyperv1.HostedCluster, kasBlockExceptions []string, isOpenShiftDNS bool, managementClusterNetwork *configv1.Network, ingressOnly bool) error {
	httpPort := intstr.FromInt(8080)
	httpsPort := intstr.FromInt(8443)
	protocol := corev1.ProtocolTCP
	policy.Spec.Ingress = []networkingv1.NetworkPolicyIngressRule{
		{
			From: []networkingv1.NetworkPolicyPeer{},
			Ports: []networkingv1.NetworkPolicyPort{
				{
					Port:     &httpPort,
					Protocol: &protocol,
				},
				{
					Port:     &httpsPort,
					Protocol: &protocol,
				},
			},
		},
	}
	policy.Spec.PodSelector = metav1.LabelSelector{
		MatchLabels: map[string]string{
			"app": "private-router",
		},
	}
	// TODO: Network policy code should move to the control plane operator. For now,
	// only setup ingress rules (and not egress rules) when version is < 4.14
	if ingressOnly {
		policy.Spec.PolicyTypes = []networkingv1.PolicyType{networkingv1.PolicyTypeIngress}
		return nil
	}

	clusterNetworks := make([]string, 0)
	// In vanilla kube management cluster this would be nil.
	if managementClusterNetwork != nil {
		for _, network := range managementClusterNetwork.Spec.ClusterNetwork {
			clusterNetworks = append(clusterNetworks, network.CIDR)
		}
	}

	// Allow to any destination not on the management cluster pod network and
	// not on the KAS block CIDRs (either KAS endpoint /32s or the MC machine
	// network CIDR depending on operator configuration).
	exceptions := make([]string, 0, len(kasBlockExceptions)+len(clusterNetworks))
	exceptions = append(exceptions, kasBlockExceptions...)
	exceptions = append(exceptions, clusterNetworks...)
	policy.Spec.Egress = []networkingv1.NetworkPolicyEgressRule{
		{
			To: []networkingv1.NetworkPolicyPeer{
				{
					IPBlock: &networkingv1.IPBlock{
						CIDR:   "0.0.0.0/0",
						Except: exceptions,
					},
				},
			},
		},
	}

	// Allow egress to request-serving pods.
	policy.Spec.Egress = append(policy.Spec.Egress, networkingv1.NetworkPolicyEgressRule{
		To: []networkingv1.NetworkPolicyPeer{
			{
				PodSelector: &metav1.LabelSelector{
					MatchExpressions: []metav1.LabelSelectorRequirement{
						{
							Key:      hyperv1.RequestServingComponentLabel,
							Operator: metav1.LabelSelectorOpExists,
						},
					},
				},
			},
		},
	})

	if isOpenShiftDNS {
		// Allow traffic to openshift-dns namespace
		dnsUDPPort := intstr.FromInt(5353)
		dnsUDPProtocol := corev1.ProtocolUDP
		dnsTCPPort := intstr.FromInt(5353)
		dnsTCPProtocol := corev1.ProtocolTCP
		policy.Spec.Egress = append(policy.Spec.Egress, networkingv1.NetworkPolicyEgressRule{
			To: []networkingv1.NetworkPolicyPeer{
				{
					NamespaceSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"kubernetes.io/metadata.name": "openshift-dns",
						},
					},
				},
			},
			Ports: []networkingv1.NetworkPolicyPort{
				{
					Port:     &dnsUDPPort,
					Protocol: &dnsUDPProtocol,
				},
				{
					Port:     &dnsTCPPort,
					Protocol: &dnsTCPProtocol,
				},
			},
		})
	} else {
		// All traffic to any destination on port 53 for both TCP and UDP
		dnsUDPPort := intstr.FromInt(53)
		dnsUDPProtocol := corev1.ProtocolUDP
		dnsTCPPort := intstr.FromInt(53)
		dnsTCPProtocol := corev1.ProtocolTCP
		policy.Spec.Egress = append(policy.Spec.Egress, networkingv1.NetworkPolicyEgressRule{
			Ports: []networkingv1.NetworkPolicyPort{
				{
					Port:     &dnsUDPPort,
					Protocol: &dnsUDPProtocol,
				},
				{
					Port:     &dnsTCPPort,
					Protocol: &dnsTCPProtocol,
				},
			},
		})
	}

	policy.Spec.PolicyTypes = []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress}

	return nil
}

func reconcileNodePortOauthNetworkPolicy(policy *networkingv1.NetworkPolicy, _ *hyperv1.HostedCluster) error {
	port := intstr.FromInt(6443)
	protocol := corev1.ProtocolTCP
	policy.Spec.Ingress = []networkingv1.NetworkPolicyIngressRule{
		{
			From: []networkingv1.NetworkPolicyPeer{},
			Ports: []networkingv1.NetworkPolicyPort{
				{
					Port:     &port,
					Protocol: &protocol,
				},
			},
		},
	}
	policy.Spec.PodSelector = metav1.LabelSelector{
		MatchLabels: map[string]string{
			"app": "oauth-openshift",
		},
	}
	policy.Spec.PolicyTypes = []networkingv1.PolicyType{networkingv1.PolicyTypeIngress}
	return nil
}

func reconcileLoadBalancerOauthNetworkPolicy(policy *networkingv1.NetworkPolicy) error {
	port := intstr.FromInt(6443)
	protocol := corev1.ProtocolTCP
	policy.Spec.Ingress = []networkingv1.NetworkPolicyIngressRule{
		{
			From: []networkingv1.NetworkPolicyPeer{},
			Ports: []networkingv1.NetworkPolicyPort{
				{
					Port:     &port,
					Protocol: &protocol,
				},
			},
		},
	}
	policy.Spec.PodSelector = metav1.LabelSelector{
		MatchLabels: map[string]string{
			"app": "oauth-openshift",
		},
	}
	policy.Spec.PolicyTypes = []networkingv1.PolicyType{networkingv1.PolicyTypeIngress}
	return nil
}

func reconcileNodePortIgnitionProxyNetworkPolicy(policy *networkingv1.NetworkPolicy, _ *hyperv1.HostedCluster) error {
	port := intstr.FromInt(8443)
	protocol := corev1.ProtocolTCP
	policy.Spec.Ingress = []networkingv1.NetworkPolicyIngressRule{
		{
			From: []networkingv1.NetworkPolicyPeer{},
			Ports: []networkingv1.NetworkPolicyPort{
				{
					Port:     &port,
					Protocol: &protocol,
				},
			},
		},
	}
	policy.Spec.PodSelector = metav1.LabelSelector{
		MatchLabels: map[string]string{
			"app": "ignition-server-proxy",
		},
	}
	policy.Spec.PolicyTypes = []networkingv1.PolicyType{networkingv1.PolicyTypeIngress}
	return nil
}

func reconcileNodePortIgnitionNetworkPolicy(policy *networkingv1.NetworkPolicy, _ *hyperv1.HostedCluster) error {
	port := intstr.FromInt(9090)
	protocol := corev1.ProtocolTCP
	policy.Spec.Ingress = []networkingv1.NetworkPolicyIngressRule{
		{
			From: []networkingv1.NetworkPolicyPeer{},
			Ports: []networkingv1.NetworkPolicyPort{
				{
					Port:     &port,
					Protocol: &protocol,
				},
			},
		},
	}
	policy.Spec.PodSelector = metav1.LabelSelector{
		MatchLabels: map[string]string{
			"app": "ignition-server",
		},
	}
	policy.Spec.PolicyTypes = []networkingv1.PolicyType{networkingv1.PolicyTypeIngress}
	return nil
}

func reconcileNodePortKonnectivityKASNetworkPolicy(policy *networkingv1.NetworkPolicy, _ *hyperv1.HostedCluster) error {
	port := intstr.FromInt(8091)
	protocol := corev1.ProtocolTCP
	policy.Spec.Ingress = []networkingv1.NetworkPolicyIngressRule{
		{
			From: []networkingv1.NetworkPolicyPeer{},
			Ports: []networkingv1.NetworkPolicyPort{
				{
					Port:     &port,
					Protocol: &protocol,
				},
			},
		},
	}
	policy.Spec.PodSelector = metav1.LabelSelector{
		MatchLabels: map[string]string{
			"app": "kube-apiserver",
		},
	}
	policy.Spec.PolicyTypes = []networkingv1.PolicyType{networkingv1.PolicyTypeIngress}
	return nil
}

func reconcileNodePortKonnectivityNetworkPolicy(policy *networkingv1.NetworkPolicy, _ *hyperv1.HostedCluster) error {
	port := intstr.FromInt(8091)
	protocol := corev1.ProtocolTCP
	policy.Spec.Ingress = []networkingv1.NetworkPolicyIngressRule{
		{
			From: []networkingv1.NetworkPolicyPeer{},
			Ports: []networkingv1.NetworkPolicyPort{
				{
					Port:     &port,
					Protocol: &protocol,
				},
			},
		},
	}
	policy.Spec.PodSelector = metav1.LabelSelector{
		MatchLabels: map[string]string{
			"app": "konnectivity-server",
		},
	}
	policy.Spec.PolicyTypes = []networkingv1.PolicyType{networkingv1.PolicyTypeIngress}
	return nil
}

func reconcileOpenshiftMonitoringNetworkPolicy(policy *networkingv1.NetworkPolicy, _ *hyperv1.HostedCluster) error {
	policy.Spec.Ingress = []networkingv1.NetworkPolicyIngressRule{
		{
			From: []networkingv1.NetworkPolicyPeer{
				{
					NamespaceSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"network.openshift.io/policy-group": "monitoring",
						},
					},
				},
			},
		},
	}
	policy.Spec.PodSelector = metav1.LabelSelector{}
	policy.Spec.PolicyTypes = []networkingv1.PolicyType{networkingv1.PolicyTypeIngress}
	return nil
}

func reconcileSharedIngressNetworkPolicy(policy *networkingv1.NetworkPolicy, _ *hyperv1.HostedCluster) error {
	policy.Spec.Ingress = []networkingv1.NetworkPolicyIngressRule{
		{
			From: []networkingv1.NetworkPolicyPeer{
				{
					NamespaceSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"kubernetes.io/metadata.name": sharedingress.RouterNamespace,
						},
					},
				},
			},
		},
	}
	policy.Spec.PodSelector = metav1.LabelSelector{}
	policy.Spec.PolicyTypes = []networkingv1.PolicyType{networkingv1.PolicyTypeIngress}
	return nil
}

func addToBlockedNetworks(network string, blockedIPv4Networks []string, blockedIPv6Networks []string) ([]string, []string) {
	if utilsnet.IsIPv6CIDRString(network) {
		blockedIPv6Networks = append(blockedIPv6Networks, network)
	} else {
		blockedIPv4Networks = append(blockedIPv4Networks, network)
	}
	return blockedIPv4Networks, blockedIPv6Networks
}

func reconcileVirtLauncherNetworkPolicy(log logr.Logger, policy *networkingv1.NetworkPolicy, hcluster *hyperv1.HostedCluster, managementClusterNetwork *configv1.Network) error {
	blockedIPv4Networks := []string{}
	blockedIPv6Networks := []string{}
	if managementClusterNetwork == nil {
		return buildVirtLauncherNetworkPolicyBase(log, policy, hcluster, blockedIPv4Networks, blockedIPv6Networks, nil)
	}
	for _, network := range managementClusterNetwork.Spec.ClusterNetwork {
		blockedIPv4Networks, blockedIPv6Networks = addToBlockedNetworks(network.CIDR, blockedIPv4Networks, blockedIPv6Networks)
	}
	for _, network := range managementClusterNetwork.Spec.ServiceNetwork {
		blockedIPv4Networks, blockedIPv6Networks = addToBlockedNetworks(network, blockedIPv4Networks, blockedIPv6Networks)
	}

	controlPlanePeers := []networkingv1.NetworkPolicyPeer{
		{
			PodSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"hypershift.openshift.io/control-plane-component": "kube-apiserver",
				},
			},
		},
		{
			PodSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"hypershift.openshift.io/control-plane-component": "oauth-openshift",
				},
			},
		},
		{
			PodSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "ignition-server-proxy",
				},
			},
		},
	}

	return buildVirtLauncherNetworkPolicyBase(log, policy, hcluster, blockedIPv4Networks, blockedIPv6Networks, controlPlanePeers)
}

// reconcileVirtLauncherNetworkPolicyExternalInfra builds the virt-launcher
// NetworkPolicy for deployments where the KubeVirt VMs run on a separate
// infrastructure cluster. Unlike the centralized variant, this omits egress
// rules for control-plane pods (kube-apiserver, oauth, ignition-server-proxy)
// because those pods reside on the management cluster and are reached via
// external IPs already permitted by the broad 0.0.0.0/0 allow rule.
// infraClusterNetwork may be nil when the infra kubeconfig lacks cluster-
// scoped read access to networks.config.openshift.io. In that case the
// policy is still created but without CIDR-based egress blocking.
func reconcileVirtLauncherNetworkPolicyExternalInfra(log logr.Logger, policy *networkingv1.NetworkPolicy, hcluster *hyperv1.HostedCluster, infraClusterNetwork *configv1.Network) error {
	blockedIPv4Networks := []string{}
	blockedIPv6Networks := []string{}
	if infraClusterNetwork != nil {
		for _, network := range infraClusterNetwork.Spec.ClusterNetwork {
			blockedIPv4Networks, blockedIPv6Networks = addToBlockedNetworks(network.CIDR, blockedIPv4Networks, blockedIPv6Networks)
		}
		for _, network := range infraClusterNetwork.Spec.ServiceNetwork {
			blockedIPv4Networks, blockedIPv6Networks = addToBlockedNetworks(network, blockedIPv4Networks, blockedIPv6Networks)
		}
	}

	return buildVirtLauncherNetworkPolicyBase(log, policy, hcluster, blockedIPv4Networks, blockedIPv6Networks, nil)
}

// buildVirtLauncherNetworkPolicyBase constructs the common virt-launcher
// NetworkPolicy structure shared by both centralized and external infra
// deployments. extraEgressPeers are appended to the primary egress rule
// (e.g. control-plane pod selectors for centralized infra).
func buildVirtLauncherNetworkPolicyBase(log logr.Logger, policy *networkingv1.NetworkPolicy, hcluster *hyperv1.HostedCluster, blockedIPv4Networks, blockedIPv6Networks []string, extraEgressPeers []networkingv1.NetworkPolicyPeer) error {
	protocolTCP := corev1.ProtocolTCP
	protocolUDP := corev1.ProtocolUDP
	protocolSCTP := corev1.ProtocolSCTP

	policy.Spec.PolicyTypes = []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress}
	policy.Spec.PodSelector = metav1.LabelSelector{
		MatchLabels: map[string]string{
			hyperv1.InfraIDLabel: hcluster.Spec.InfraID,
			"kubevirt.io":        "virt-launcher",
		},
	}
	policy.Spec.Ingress = []networkingv1.NetworkPolicyIngressRule{
		{
			Ports: []networkingv1.NetworkPolicyPort{
				{Protocol: &protocolTCP},
				{Protocol: &protocolUDP},
				{Protocol: &protocolSCTP},
			},
		},
	}

	egressPeers := []networkingv1.NetworkPolicyPeer{
		{
			IPBlock: &networkingv1.IPBlock{
				CIDR:   netip.PrefixFrom(netip.IPv4Unspecified(), 0).String(),
				Except: blockedIPv4Networks,
			},
		},
		{
			IPBlock: &networkingv1.IPBlock{
				CIDR:   netip.PrefixFrom(netip.IPv6Unspecified(), 0).String(),
				Except: blockedIPv6Networks,
			},
		},
		{
			PodSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					hyperv1.InfraIDLabel: hcluster.Spec.InfraID,
					"kubevirt.io":        "virt-launcher",
				},
			},
		},
		{
			PodSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"dns.operator.openshift.io/daemonset-dns": "default",
				},
			},
			NamespaceSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"kubernetes.io/metadata.name": "openshift-dns",
				},
			},
		},
		{
			PodSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"ingresscontroller.operator.openshift.io/deployment-ingresscontroller": "default",
				},
			},
			NamespaceSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"name": "openshift-ingress",
				},
			},
		},
	}
	egressPeers = append(egressPeers, extraEgressPeers...)

	policy.Spec.Egress = []networkingv1.NetworkPolicyEgressRule{
		{To: egressPeers},
	}

	nodeAddressesMap := make(map[string]bool)
	for _, hcService := range hcluster.Spec.Services {
		if hcService.Type != hyperv1.NodePort {
			continue
		}
		nodeAddress := hcService.NodePort.Address
		if nodeAddressesMap[nodeAddress] {
			continue
		}
		nodeAddressesMap[nodeAddress] = true
		var prefixLength int
		if utilsnet.IsIPv4String(nodeAddress) {
			prefixLength = 32
		} else if utilsnet.IsIPv6String(nodeAddress) {
			prefixLength = 128
		} else {
			log.Info(fmt.Sprintf("could not determine if %s is an IPv4 or IPv6 address, skipping virt-launcher network policy for service %q", nodeAddress, hcService.Type))
			continue
		}
		parsedNodeAddress, err := netip.ParseAddr(nodeAddress)
		if err != nil {
			return fmt.Errorf("parsing nodeport address (%s) from service %s: %w", nodeAddress, hcService.Service, err)
		}
		policy.Spec.Egress = append(policy.Spec.Egress, networkingv1.NetworkPolicyEgressRule{
			To: []networkingv1.NetworkPolicyPeer{
				{
					IPBlock: &networkingv1.IPBlock{
						CIDR: netip.PrefixFrom(parsedNodeAddress, prefixLength).String(),
					},
				},
			},
		})
	}
	return nil
}

func reconcileOpenshiftIngressNetworkPolicy(policy *networkingv1.NetworkPolicy) error {
	policy.Spec.Ingress = []networkingv1.NetworkPolicyIngressRule{
		{
			From: []networkingv1.NetworkPolicyPeer{
				{
					NamespaceSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"network.openshift.io/policy-group": "ingress",
						},
					},
				},
			},
		},
	}
	policy.Spec.PodSelector = metav1.LabelSelector{}
	policy.Spec.PolicyTypes = []networkingv1.PolicyType{networkingv1.PolicyTypeIngress}
	return nil
}

func reconcileSameNamespaceNetworkPolicy(policy *networkingv1.NetworkPolicy) error {
	policy.Spec.Ingress = []networkingv1.NetworkPolicyIngressRule{
		{
			From: []networkingv1.NetworkPolicyPeer{
				{
					PodSelector: &metav1.LabelSelector{},
				},
			},
		},
	}
	policy.Spec.PodSelector = metav1.LabelSelector{}
	policy.Spec.PolicyTypes = []networkingv1.PolicyType{networkingv1.PolicyTypeIngress}
	return nil
}

// reconcileManagementKASNetworkPolicy selects pods excluding the ones having NeedManagementKASAccessLabel and specific operands.
// It denies egress traffic to the management cluster clusterNetwork and to the KAS endpoints.
func reconcileManagementKASNetworkPolicy(policy *networkingv1.NetworkPolicy, managementClusterNetwork *configv1.Network, kasBlockExceptions []string, isOpenShiftDNS bool) error {
	// Allow traffic to same namespace
	policy.Spec.Egress = []networkingv1.NetworkPolicyEgressRule{
		{
			To: []networkingv1.NetworkPolicyPeer{
				{
					PodSelector: &metav1.LabelSelector{},
				},
			},
		},
	}

	clusterNetworks := make([]string, 0)
	// In vanilla kube management cluster this would be nil.
	if managementClusterNetwork != nil {
		for _, network := range managementClusterNetwork.Spec.ClusterNetwork {
			clusterNetworks = append(clusterNetworks, network.CIDR)
		}
	}

	// Allow to any destination not on the management cluster pod network and
	// not on the KAS block CIDRs (either KAS endpoint /32s or the MC machine
	// network CIDR depending on operator configuration).
	exceptions := make([]string, 0, len(kasBlockExceptions)+len(clusterNetworks))
	exceptions = append(exceptions, kasBlockExceptions...)
	exceptions = append(exceptions, clusterNetworks...)
	policy.Spec.Egress = append(policy.Spec.Egress,
		networkingv1.NetworkPolicyEgressRule{
			To: []networkingv1.NetworkPolicyPeer{
				{
					IPBlock: &networkingv1.IPBlock{
						CIDR:   "0.0.0.0/0",
						Except: exceptions,
					},
				},
			},
		})

	if isOpenShiftDNS {
		// Allow traffic to openshift-dns namespace
		dnsUDPPort := intstr.FromInt(5353)
		dnsUDPProtocol := corev1.ProtocolUDP
		dnsTCPPort := intstr.FromInt(5353)
		dnsTCPProtocol := corev1.ProtocolTCP
		policy.Spec.Egress = append(policy.Spec.Egress, networkingv1.NetworkPolicyEgressRule{
			To: []networkingv1.NetworkPolicyPeer{
				{
					NamespaceSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"kubernetes.io/metadata.name": "openshift-dns",
						},
					},
				},
			},
			Ports: []networkingv1.NetworkPolicyPort{
				{
					Port:     &dnsUDPPort,
					Protocol: &dnsUDPProtocol,
				},
				{
					Port:     &dnsTCPPort,
					Protocol: &dnsTCPProtocol,
				},
			},
		})
	} else {
		// All traffic to any destination on port 53 for both TCP and UDP
		dnsUDPPort := intstr.FromInt(53)
		dnsUDPProtocol := corev1.ProtocolUDP
		dnsTCPPort := intstr.FromInt(53)
		dnsTCPProtocol := corev1.ProtocolTCP
		policy.Spec.Egress = append(policy.Spec.Egress, networkingv1.NetworkPolicyEgressRule{
			Ports: []networkingv1.NetworkPolicyPort{
				{
					Port:     &dnsUDPPort,
					Protocol: &dnsUDPProtocol,
				},
				{
					Port:     &dnsTCPPort,
					Protocol: &dnsTCPProtocol,
				},
			},
		})
	}

	policy.Spec.PolicyTypes = []networkingv1.PolicyType{networkingv1.PolicyTypeEgress}
	policy.Spec.PodSelector = metav1.LabelSelector{
		MatchExpressions: []metav1.LabelSelectorRequirement{
			{
				Key:      NeedManagementKASAccessLabel,
				Operator: "DoesNotExist",
				Values:   nil,
			},
			{
				Key:      "name",
				Operator: "NotIn",
				Values:   []string{"aws-ebs-csi-driver-operator"},
			},
		},
	}

	return nil
}

//nolint:staticcheck // SA1019: corev1.Endpoints is intentionally used for backward compatibility
func kasEndpointsToCIDRs(kubernetesEndpoint *corev1.Endpoints) []string {
	kasCIDRs := make([]string, 0)
	for _, subset := range kubernetesEndpoint.Subsets {
		for _, address := range subset.Addresses {
			// Get the CIDR string representation.
			ip := net.ParseIP(address.IP)
			mask := net.CIDRMask(32, 32)
			// Convert IP and mask to CIDR notation
			ipNet := &net.IPNet{
				IP:   ip,
				Mask: mask,
			}

			kasCIDRs = append(kasCIDRs, ipNet.String())
		}
	}
	return kasCIDRs
}

// reconcileMetricsServerNetworkPolicy selects pods having NeedMetricsServerAccessLabel.
// It allows egress traffic to the HCP metrics server that is available for self-managed HyperShift and ROSA HCP.
// Depending on the monitoring stack:
// - RHOBS (ROSA HCP only): targets OBO Prometheus at port 9090 in openshift-observability-operator namespace
// - CoreOS (self-managed HyperShift): targets Thanos Querier at port 9092 in openshift-monitoring namespace
func reconcileMetricsServerNetworkPolicy(policy *networkingv1.NetworkPolicy, hcp *hyperv1.HostedControlPlane) error {
	protocol := corev1.ProtocolTCP
	var egressRules []networkingv1.NetworkPolicyEgressRule

	if os.Getenv(rhobsmonitoring.EnvironmentVariable) == "1" && awsutil.IsROSAHCP(hcp) {
		// RHOBS Prometheus configuration (ROSA HCP specific)
		port := intstr.FromInt(9090)
		egressRules = []networkingv1.NetworkPolicyEgressRule{
			{
				To: []networkingv1.NetworkPolicyPeer{
					{
						PodSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"app.kubernetes.io/instance": "hypershift-monitoring-stack",
								"app.kubernetes.io/name":     "prometheus",
							}},
						NamespaceSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"network.openshift.io/policy-group": "monitoring",
							},
						},
					},
				},
				Ports: []networkingv1.NetworkPolicyPort{
					{
						Protocol: &protocol,
						Port:     &port,
					},
				},
			},
		}
	} else {
		// CoreOS Thanos configuration
		port := intstr.FromInt(9092)
		egressRules = []networkingv1.NetworkPolicyEgressRule{
			{
				To: []networkingv1.NetworkPolicyPeer{
					{
						PodSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"app.kubernetes.io/instance": "thanos-querier",
								"app.kubernetes.io/name":     "thanos-query",
							}},
						NamespaceSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"network.openshift.io/policy-group": "monitoring",
							},
						},
					},
				},
				Ports: []networkingv1.NetworkPolicyPort{
					{
						Protocol: &protocol,
						Port:     &port,
					},
				},
			},
		}
	}

	policy.Spec.Egress = egressRules

	policy.Spec.PolicyTypes = []networkingv1.PolicyType{networkingv1.PolicyTypeEgress}
	policy.Spec.PodSelector = metav1.LabelSelector{
		MatchExpressions: []metav1.LabelSelectorRequirement{
			{
				Key:      NeedMetricsServerAccessLabel,
				Operator: "Exists",
				Values:   nil,
			},
		},
	}

	return nil
}

// reconcileExternalInfraVirtLauncherPolicy discovers the KubeVirt infra client
// and creates/updates the virt-launcher NetworkPolicy on the infrastructure cluster.
// It handles RBAC errors gracefully by setting status conditions.
func (r *HostedClusterReconciler) reconcileExternalInfraVirtLauncherPolicy(ctx context.Context, log logr.Logger, createOrUpdate upsert.CreateOrUpdateFN, hcluster *hyperv1.HostedCluster) error {
	kvInfraClient, err := r.KubevirtInfraClients.DiscoverKubevirtClusterClient(ctx,
		r.Client,
		hcluster.Spec.InfraID,
		hcluster.Spec.Platform.Kubevirt.Credentials,
		hcluster.Namespace,
		hcluster.Namespace)
	if err != nil {
		return fmt.Errorf("failed to get kubevirt infra client for network policy: %w", err)
	}

	infraClient := kvInfraClient.GetInfraClient()
	infraNamespace := kvInfraClient.GetInfraNamespace()

	// networks.config.openshift.io is cluster-scoped, so the infra
	// kubeconfig needs a ClusterRole with get permission on that
	// resource. When the permission is missing we still create the
	// NetworkPolicy but without CIDR-based egress blocking, and
	// surface the RBAC gap as a condition on the HostedCluster.
	infraClusterNetwork, err := fetchInfraClusterNetwork(ctx, infraClient, infraNamespace, hcluster, log)
	if err != nil {
		return err
	}

	policy := networkpolicy.VirtLauncherNetworkPolicy(infraNamespace)
	if _, err := createOrUpdate(ctx, infraClient, policy, func() error {
		return reconcileVirtLauncherNetworkPolicyExternalInfra(log, policy, hcluster, infraClusterNetwork)
	}); err != nil {
		if apierrors.IsForbidden(err) {
			rbacMsg := fmt.Sprintf(
				"Unable to create/update virt-launcher NetworkPolicy on the infrastructure cluster: "+
					"the external infra kubeconfig lacks networking.k8s.io/networkpolicies permissions. "+
					"Grant create/update/get/list/watch on networkpolicies in the infra namespace. "+
					"Error: %v", err)
			log.Info(rbacMsg)
			meta.SetStatusCondition(&hcluster.Status.Conditions, metav1.Condition{
				Type:               string(hyperv1.ValidKubeVirtInfraNetworkPolicyRBAC),
				Status:             metav1.ConditionFalse,
				Reason:             hyperv1.InfraClusterNetworkPolicyCreateFailedReason,
				ObservedGeneration: hcluster.Generation,
				Message:            rbacMsg,
			})
			emitInfraClusterWarningEvent(ctx, infraClient, infraNamespace, hcluster.Spec.InfraID, rbacMsg, log)
		} else {
			return fmt.Errorf("failed to reconcile virt launcher policy on external infra: %w", err)
		}
	}
	return nil
}

// fetchInfraClusterNetwork reads networks.config.openshift.io/cluster from the
// infrastructure cluster. When the permission is missing we still allow the
// NetworkPolicy to be created but without CIDR-based egress blocking, and
// surface the RBAC gap as a condition on the HostedCluster.
func fetchInfraClusterNetwork(ctx context.Context, infraClient client.Client, infraNamespace string, hcluster *hyperv1.HostedCluster, log logr.Logger) (*configv1.Network, error) {
	networkObj := &configv1.Network{ObjectMeta: metav1.ObjectMeta{Name: "cluster"}}
	if err := infraClient.Get(ctx, client.ObjectKeyFromObject(networkObj), networkObj); err != nil {
		if apierrors.IsForbidden(err) || apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
			rbacMsg := fmt.Sprintf(
				"The external infrastructure kubeconfig lacks permission to read "+
					"networks.config.openshift.io/cluster. The virt-launcher NetworkPolicy "+
					"has been created without CIDR-based egress restrictions, resulting in "+
					"weaker tenant isolation. Grant a ClusterRole with get on "+
					"networks.config.openshift.io to the infra service account for full isolation. "+
					"Error: %v", err)
			log.Info(rbacMsg)
			meta.SetStatusCondition(&hcluster.Status.Conditions, metav1.Condition{
				Type:               string(hyperv1.ValidKubeVirtInfraNetworkPolicyRBAC),
				Status:             metav1.ConditionFalse,
				Reason:             hyperv1.InfraClusterNetworkReadFailedReason,
				ObservedGeneration: hcluster.Generation,
				Message:            rbacMsg,
			})
			emitInfraClusterWarningEvent(ctx, infraClient, infraNamespace, hcluster.Spec.InfraID, rbacMsg, log)
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get infrastructure cluster network config: %w", err)
	}
	meta.SetStatusCondition(&hcluster.Status.Conditions, metav1.Condition{
		Type:               string(hyperv1.ValidKubeVirtInfraNetworkPolicyRBAC),
		Status:             metav1.ConditionTrue,
		Reason:             hyperv1.AsExpectedReason,
		ObservedGeneration: hcluster.Generation,
		Message:            "Infrastructure cluster network configuration is readable; CIDR-based egress restrictions are active.",
	})
	return networkObj, nil
}

// emitInfraClusterWarningEvent creates or updates a warning Event in the
// infrastructure cluster namespace so that operators monitoring the infra
// cluster can see the RBAC gap without access to the management cluster.
func emitInfraClusterWarningEvent(ctx context.Context, infraClient client.Client, namespace, infraID, message string, log logr.Logger) {
	now := metav1.NewTime(time.Now())
	eventName := fmt.Sprintf("virt-launcher-netpol-rbac-%s", infraID)

	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:      eventName,
			Namespace: namespace,
		},
	}

	existing := &corev1.Event{}
	if err := infraClient.Get(ctx, client.ObjectKeyFromObject(event), existing); err == nil {
		existing.Count++
		existing.LastTimestamp = now
		existing.Message = message
		if updateErr := infraClient.Update(ctx, existing); updateErr != nil {
			log.Info("unable to update warning event on infrastructure cluster", "error", updateErr)
		}
		return
	}

	event.InvolvedObject = corev1.ObjectReference{
		APIVersion: "v1",
		Kind:       "Namespace",
		Name:       namespace,
	}
	event.Reason = "InsufficientNetworkPolicyRBAC"
	event.Message = message
	event.Type = corev1.EventTypeWarning
	event.Source = corev1.EventSource{
		Component: "hypershift-operator",
	}
	event.FirstTimestamp = now
	event.LastTimestamp = now
	event.Count = 1

	if createErr := infraClient.Create(ctx, event); createErr != nil {
		log.Info("unable to create warning event on infrastructure cluster", "error", createErr)
	}
}
