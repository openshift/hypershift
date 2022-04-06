package network

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	operatorv1 "github.com/openshift/api/operator/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
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

func ReconcileNetworkOperator(network *operatorv1.Network, networkType hyperv1.NetworkType, platformType hyperv1.PlatformType) error {
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
		}

	default:
		// do nothing
	}

	// Setting the management state is required in order to create
	// this object. We need to create this object before the cno starts
	// because mutating many of the values (like vxlanport) is not premitted
	// after the cno reconciles this operator CR
	if network.Spec.ManagementState == "" {
		network.Spec.ManagementState = "Managed"
	}
	return nil
}
