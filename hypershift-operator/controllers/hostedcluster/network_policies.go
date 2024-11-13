package hostedcluster

import (
	"context"
	"fmt"
	"net"
	"net/netip"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/networkpolicy"
	"github.com/openshift/hypershift/hypershift-operator/controllers/sharedingress"
	"github.com/openshift/hypershift/support/capabilities"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/upsert"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	utilsnet "k8s.io/utils/net"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/blang/semver"
)

const (
	NeedManagementKASAccessLabel = "hypershift.openshift.io/need-management-kas-access"
	NeedMetricsServerAccessLabel = "hypershift.openshift.io/need-metrics-server-access"
)

func (r *HostedClusterReconciler) reconcileNetworkPolicies(ctx context.Context, createOrUpdate upsert.CreateOrUpdateFN, hcluster *hyperv1.HostedCluster, hcp *hyperv1.HostedControlPlane, version semver.Version, controlPlaneOperatorAppliesManagementKASNetworkPolicyLabel bool) error {
	controlPlaneNamespaceName := manifests.HostedControlPlaneNamespace(hcluster.Namespace, hcluster.Name)

	// Reconcile openshift-ingress Network Policy
	policy := networkpolicy.OpenshiftIngressNetworkPolicy(controlPlaneNamespaceName)
	if _, err := createOrUpdate(ctx, r.Client, policy, func() error {
		return reconcileOpenshiftIngressNetworkPolicy(policy)
	}); err != nil {
		return fmt.Errorf("failed to reconcile ingress network policy: %w", err)
	}

	// Reconcile same-namespace Network Policy
	policy = networkpolicy.SameNamespaceNetworkPolicy(controlPlaneNamespaceName)
	if _, err := createOrUpdate(ctx, r.Client, policy, func() error {
		return reconcileSameNamespaceNetworkPolicy(policy)
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
		return reconcileKASNetworkPolicy(policy, hcluster, r.ManagementClusterCapabilities.Has(capabilities.CapabilityDNS), managementClusterNetwork)
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
			return reconcileManagementKASNetworkPolicy(policy, managementClusterNetwork, kubernetesEndpoint, r.ManagementClusterCapabilities.Has(capabilities.CapabilityDNS))
		}); err != nil {
			return fmt.Errorf("failed to reconcile kube-apiserver network policy: %w", err)
		}

		// Allow egress communication to the HCP metrics server for pods that have a known annotation.
		if r.EnableCVOManagementClusterMetricsAccess {
			policy = networkpolicy.MetricsServerNetworkPolicy(controlPlaneNamespaceName)
			if _, err := createOrUpdate(ctx, r.Client, policy, func() error {
				return reconcileMetricsServerNetworkPolicy(policy)
			}); err != nil {
				return fmt.Errorf("failed to reconcile metrics server network policy: %w", err)
			}
		}
	}

	if sharedingress.UseSharedIngress() {
		// Reconcile shared-ingress Network Policy.
		// Let all ingress from shared-ingress namespace.
		policy := networkpolicy.SharedIngressNetworkPolicy(controlPlaneNamespaceName)
		if _, err := createOrUpdate(ctx, r.Client, policy, func() error {
			return reconcileSharedIngressNetworkPolicy(policy, hcluster)
		}); err != nil {
			return fmt.Errorf("failed to reconcile sharedingress network policy: %w", err)
		}
	}

	// Reconcile openshift-monitoring Network Policy
	policy = networkpolicy.OpenshiftMonitoringNetworkPolicy(controlPlaneNamespaceName)
	if _, err := createOrUpdate(ctx, r.Client, policy, func() error {
		return reconcileOpenshiftMonitoringNetworkPolicy(policy, hcluster)
	}); err != nil {
		return fmt.Errorf("failed to reconcile monitoring network policy: %w", err)
	}

	// Reconcile private-router Network Policy
	if hcluster.Spec.Platform.Type == hyperv1.AWSPlatform || hcluster.Spec.Platform.Type == hyperv1.AzurePlatform {
		policy = networkpolicy.PrivateRouterNetworkPolicy(controlPlaneNamespaceName)
		// TODO: Network policy code should move to the control plane operator. For now,
		// only setup ingress rules (and not egress rules) when version is < 4.14
		ingressOnly := version.Major == 4 && version.Minor < 14
		if _, err := createOrUpdate(ctx, r.Client, policy, func() error {
			return reconcilePrivateRouterNetworkPolicy(policy, hcluster, kubernetesEndpoint, r.ManagementClusterCapabilities.Has(capabilities.CapabilityDNS), managementClusterNetwork, ingressOnly)
		}); err != nil {
			return fmt.Errorf("failed to reconcile private router network policy: %w", err)
		}
	} else if hcluster.Spec.Platform.Type == hyperv1.KubevirtPlatform {
		if hcluster.Spec.Platform.Kubevirt.Credentials == nil {
			// network policy is being set on centralized infra only, not on external infra
			policy = networkpolicy.VirtLauncherNetworkPolicy(controlPlaneNamespaceName)
			if _, err := createOrUpdate(ctx, r.Client, policy, func() error {
				return reconcileVirtLauncherNetworkPolicy(policy, hcluster, managementClusterNetwork)
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
					return reconcileNodePortOauthNetworkPolicy(policy, hcluster)
				}); err != nil {
					return fmt.Errorf("failed to reconcile oauth server nodeport network policy: %w", err)
				}
			}
		case hyperv1.Ignition:
			if svc.ServicePublishingStrategy.Type == hyperv1.NodePort {
				// Reconcile nodeport-ignition Network Policy
				policy = networkpolicy.NodePortIgnitionNetworkPolicy(controlPlaneNamespaceName)
				if _, err := createOrUpdate(ctx, r.Client, policy, func() error {
					return reconcileNodePortIgnitionNetworkPolicy(policy, hcluster)
				}); err != nil {
					return fmt.Errorf("failed to reconcile ignition nodeport network policy: %w", err)
				}
				// Reconcile nodeport-ignition-proxy Network Policy
				policy = networkpolicy.NodePortIgnitionProxyNetworkPolicy(controlPlaneNamespaceName)
				if _, err := createOrUpdate(ctx, r.Client, policy, func() error {
					return reconcileNodePortIgnitionProxyNetworkPolicy(policy, hcluster)
				}); err != nil {
					return fmt.Errorf("failed to reconcile ignition proxy nodeport network policy: %w", err)
				}
			}
		case hyperv1.Konnectivity:
			if svc.ServicePublishingStrategy.Type == hyperv1.NodePort {
				// Reconcile nodeport-konnectivity Network Policy
				policy = networkpolicy.NodePortKonnectivityNetworkPolicy(controlPlaneNamespaceName)
				if _, err := createOrUpdate(ctx, r.Client, policy, func() error {
					return reconcileNodePortKonnectivityNetworkPolicy(policy, hcluster)
				}); err != nil {
					return fmt.Errorf("failed to reconcile konnectivity nodeport network policy: %w", err)
				}

				// Reconcile nodeport-konnectivity Network Policy when konnectivity is hosted in the kas pod
				policy = networkpolicy.NodePortKonnectivityKASNetworkPolicy(controlPlaneNamespaceName)
				if _, err := createOrUpdate(ctx, r.Client, policy, func() error {
					return reconcileNodePortKonnectivityKASNetworkPolicy(policy, hcluster)
				}); err != nil {
					return fmt.Errorf("failed to reconcile konnectivity nodeport network policy: %w", err)
				}

			}
		}
	}

	return nil
}

func reconcileKASNetworkPolicy(policy *networkingv1.NetworkPolicy, hcluster *hyperv1.HostedCluster, isOpenShiftDNS bool, managementClusterNetwork *configv1.Network) error {
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

func reconcilePrivateRouterNetworkPolicy(policy *networkingv1.NetworkPolicy, hcluster *hyperv1.HostedCluster, kubernetesEndpoint *corev1.Endpoints, isOpenShiftDNS bool, managementClusterNetwork *configv1.Network, ingressOnly bool) error {
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

	// Allow to any destination not on the management cluster service network
	// i.e. block all inter-namespace egress not allowed by other rules.
	// Also do not allow Kubernetes endpoint IPs explicitly
	// i.e. block access to management cluster KAS.
	exceptions := append(kasEndpointsToCIDRs(kubernetesEndpoint), clusterNetworks...)
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

func reconcileNodePortOauthNetworkPolicy(policy *networkingv1.NetworkPolicy, hcluster *hyperv1.HostedCluster) error {
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

func reconcileNodePortIgnitionProxyNetworkPolicy(policy *networkingv1.NetworkPolicy, hcluster *hyperv1.HostedCluster) error {
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

func reconcileNodePortIgnitionNetworkPolicy(policy *networkingv1.NetworkPolicy, hcluster *hyperv1.HostedCluster) error {
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

func reconcileNodePortKonnectivityKASNetworkPolicy(policy *networkingv1.NetworkPolicy, hcluster *hyperv1.HostedCluster) error {
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

func reconcileNodePortKonnectivityNetworkPolicy(policy *networkingv1.NetworkPolicy, hcluster *hyperv1.HostedCluster) error {
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

func reconcileOpenshiftMonitoringNetworkPolicy(policy *networkingv1.NetworkPolicy, hcluster *hyperv1.HostedCluster) error {
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

func reconcileSharedIngressNetworkPolicy(policy *networkingv1.NetworkPolicy, hcluster *hyperv1.HostedCluster) error {
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

func reconcileVirtLauncherNetworkPolicy(policy *networkingv1.NetworkPolicy, hcluster *hyperv1.HostedCluster, managementClusterNetwork *configv1.Network) error {
	protocolTCP := corev1.ProtocolTCP
	protocolUDP := corev1.ProtocolUDP
	protocolSCTP := corev1.ProtocolSCTP

	blockedIPv4Networks := []string{}
	blockedIPv6Networks := []string{}
	for _, network := range managementClusterNetwork.Spec.ClusterNetwork {
		blockedIPv4Networks, blockedIPv6Networks = addToBlockedNetworks(network.CIDR, blockedIPv4Networks, blockedIPv6Networks)
	}

	for _, network := range managementClusterNetwork.Spec.ServiceNetwork {
		blockedIPv4Networks, blockedIPv6Networks = addToBlockedNetworks(network, blockedIPv4Networks, blockedIPv6Networks)
	}

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
	policy.Spec.Egress = []networkingv1.NetworkPolicyEgressRule{
		{
			To: []networkingv1.NetworkPolicyPeer{
				{
					// Allow access towards outside the cluster for the virtual machines (guest nodes),
					// But deny access to other cluster's pods and services with the exceptions below
					// IPv4
					IPBlock: &networkingv1.IPBlock{
						CIDR:   netip.PrefixFrom(netip.IPv4Unspecified(), 0).String(), // 0.0.0.0/0
						Except: blockedIPv4Networks,
					},
				},
				{
					// IPv6
					IPBlock: &networkingv1.IPBlock{
						CIDR:   netip.PrefixFrom(netip.IPv6Unspecified(), 0).String(), // ::/0
						Except: blockedIPv6Networks,
					},
				},
				{
					// Allow the guest nodes to communicate between each other
					PodSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							hyperv1.InfraIDLabel: hcluster.Spec.InfraID,
							"kubevirt.io":        "virt-launcher",
						},
					},
				},
				{
					// Allow access to the cluster's DNS server for name resolution
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
					// Allow access to the guest cluster API server
					PodSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"hypershift.openshift.io/control-plane-component": "kube-apiserver",
						},
					},
				},
				{
					// Allow access to the oauth server (from the console pod on the virtual machine)
					PodSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"hypershift.openshift.io/control-plane-component": "oauth-openshift",
						},
					},
				},
				{
					// Allow access to the ignition server
					PodSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "ignition-server-proxy",
						},
					},
				},
				{
					// Allow access to the management cluster ingress (for ignition server)
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
			},
		},
	}
	nodeAddressesMap := make(map[string]bool)
	for _, hcService := range hcluster.Spec.Services {
		if hcService.Type != hyperv1.NodePort {
			continue
		}
		nodeAddress := hcService.NodePort.Address
		_, exists := nodeAddressesMap[nodeAddress]
		if exists {
			continue
		}
		nodeAddressesMap[nodeAddress] = true
		var prefixLength int
		if utilsnet.IsIPv4String(nodeAddress) {
			prefixLength = 32
		} else if utilsnet.IsIPv6String(nodeAddress) {
			prefixLength = 128
		} else {
			return fmt.Errorf("could not determine if %s is an IPv4 or IPv6 address", nodeAddress)
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
func reconcileManagementKASNetworkPolicy(policy *networkingv1.NetworkPolicy, managementClusterNetwork *configv1.Network, kubernetesEndpoint *corev1.Endpoints, isOpenShiftDNS bool) error {
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

	// Allow to any destination not on the management cluster service network
	// i.e. block all inter-namespace egress not allowed by other rules.
	// Also do not allow Kubernetes endpoint IPs explicitly
	// i.e. block access to management cluster KAS.
	exceptions := append(kasEndpointsToCIDRs(kubernetesEndpoint), clusterNetworks...)
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
// It allows egress traffic to the HCP metrics server that is available for self-managed
// HyperShift running on an OpenShift management cluster.
func reconcileMetricsServerNetworkPolicy(policy *networkingv1.NetworkPolicy) error {
	protocol := corev1.ProtocolTCP
	port := intstr.FromInt(9092)
	policy.Spec.Egress = []networkingv1.NetworkPolicyEgressRule{
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
