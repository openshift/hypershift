package network

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	operatorv1 "github.com/openshift/api/operator/v1"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

func NetworkOperator() *operatorv1.Network {
	return &operatorv1.Network{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
	}
}

// The default vxlan port registered by IANA is 4789. We need to avoid that for
// kubernetes which runs nested.
// 9879 is a currently unassigned IANA port in the user port range.
const kubevirtDefaultVXLANPort = uint32(9879)

// The default OVN geneve port is 6081. We need to avoid that for kubernetes which runs nested.
// 9880 is a currently unassigned IANA port in the user port range.
const kubevirtDefaultGenevePort = uint32(9880)

// The default OVN gateway router LRP CIDR is 100.64.0.0/16. We need to avoid
// that for kubernetes which runs nested.
// 100.65.0.0/16 is not used internally at OVN kubernetes.
const kubevirtDefaultV4InternalSubnet = "100.65.0.0/16"

func ReconcileNetworkOperator(network *operatorv1.Network, networkType hyperv1.NetworkType, platformType hyperv1.PlatformType, disableMultiNetwork bool) {
	switch platformType {
	case hyperv1.KubevirtPlatform:
		// Modify vxlan port to avoid collisions with management cluster's default vxlan port.
		if networkType == hyperv1.OpenShiftSDN {
			port := kubevirtDefaultVXLANPort
			if network.Spec.DefaultNetwork.OpenShiftSDNConfig == nil {
				network.Spec.DefaultNetwork.OpenShiftSDNConfig = &operatorv1.OpenShiftSDNConfig{}
			}
			if network.Spec.DefaultNetwork.OpenShiftSDNConfig.VXLANPort == nil {
				network.Spec.DefaultNetwork.OpenShiftSDNConfig.VXLANPort = &port
			}
		} else if networkType == hyperv1.OVNKubernetes {
			port := kubevirtDefaultGenevePort
			if network.Spec.DefaultNetwork.OVNKubernetesConfig == nil {
				network.Spec.DefaultNetwork.OVNKubernetesConfig = &operatorv1.OVNKubernetesConfig{}
			}
			if network.Spec.DefaultNetwork.OVNKubernetesConfig.V4InternalSubnet == "" {
				network.Spec.DefaultNetwork.OVNKubernetesConfig.V4InternalSubnet = kubevirtDefaultV4InternalSubnet
			}
			if network.Spec.DefaultNetwork.OVNKubernetesConfig.GenevePort == nil {
				network.Spec.DefaultNetwork.OVNKubernetesConfig.GenevePort = &port
			}
		}
	case hyperv1.PowerVSPlatform:
		if networkType == hyperv1.OVNKubernetes {
			if network.Spec.DefaultNetwork.OVNKubernetesConfig == nil {
				network.Spec.DefaultNetwork.OVNKubernetesConfig = &operatorv1.OVNKubernetesConfig{}
			}
			// Default shared routing causes egress traffic to use OVN routes, to use the routes present in host, need to use host routing
			// BZ: https://bugzilla.redhat.com/show_bug.cgi?id=1996108
			if network.Spec.DefaultNetwork.OVNKubernetesConfig.GatewayConfig == nil {
				network.Spec.DefaultNetwork.OVNKubernetesConfig.GatewayConfig = &operatorv1.GatewayConfig{}
			}
			network.Spec.DefaultNetwork.OVNKubernetesConfig.GatewayConfig.RoutingViaHost = true
		}
	default:
		// do nothing
	}

	// Setting the management state is required in order to create
	// this object. We need to create this object before the cno starts
	// because mutating many of the values (like vxlanport) is not permitted
	// after the cno reconciles this operator CR
	if network.Spec.ManagementState == "" {
		network.Spec.ManagementState = "Managed"
	}

	// Set disableMultiNetwork to disable Multus CNI and related components
	if disableMultiNetwork {
		network.Spec.DisableMultiNetwork = &disableMultiNetwork
	}
}

func DetectSuboptimalMTU(ctx context.Context, mgmtClient client.Client,
	guestNetworkOperator *operatorv1.Network, hcp *hyperv1.HostedControlPlane) error {
	const recommendedMinMTU = uint32(9000)
	const ovnkOverhead = 200

	if hcp.Spec.Platform.Type == hyperv1.KubevirtPlatform &&
		hcp.Spec.Networking.NetworkType == hyperv1.OVNKubernetes &&
		guestNetworkOperator.Spec.DefaultNetwork.OVNKubernetesConfig != nil &&
		guestNetworkOperator.Spec.DefaultNetwork.OVNKubernetesConfig.MTU != nil {

		conditionStatus := metav1.ConditionTrue
		conditionReason := hyperv1.AsExpectedReason
		conditionMessage := hyperv1.AllIsWellMessage

		if *guestNetworkOperator.Spec.DefaultNetwork.OVNKubernetesConfig.MTU < recommendedMinMTU-ovnkOverhead {
			// The detected MTU value is suboptimal
			conditionStatus = metav1.ConditionFalse
			conditionReason = hyperv1.KubeVirtSuboptimalMTUReason
			conditionMessage = fmt.Sprintf("A suboptimal MTU size has been detected. "+
				"Due to performance, we recommend setting an MTU of %d bytes or greater for the "+
				"infra cluster that hosts the KubeVirt VirtualMachines. When smaller MTUs are used, the cluster will "+
				"still operate, but network performance will be degraded due to fragmentation of the double "+
				"encapsulation in OVN-Kubernetes.", recommendedMinMTU)
		}
		originalHCP := hcp.DeepCopy()
		meta.SetStatusCondition(&hcp.Status.Conditions, metav1.Condition{
			Type:               string(hyperv1.ValidKubeVirtInfraNetworkMTU),
			Status:             conditionStatus,
			Reason:             conditionReason,
			ObservedGeneration: hcp.Generation,
			Message:            conditionMessage,
		})
		if !equality.Semantic.DeepEqual(hcp.Status, originalHCP.Status) {
			if err := mgmtClient.Status().Patch(ctx, hcp, client.MergeFromWithOptions(originalHCP, client.MergeFromWithOptimisticLock{})); err != nil {
				return fmt.Errorf("failed to set suboptimal MTU condition on HCP: %w", err)
			}
		}
	}
	return nil
}
