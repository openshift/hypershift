package network

import (
	"testing"

	. "github.com/onsi/gomega"
	operatorv1 "github.com/openshift/api/operator/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
)

func TestReconcileDefaultIngressController(t *testing.T) {
	port := kubevirtDefaultVXLANPort
	fakePort := uint32(11111)
	testsCases := []struct {
		name              string
		inputNetwork      *operatorv1.Network
		inputNetworkType  hyperv1.NetworkType
		inputPlatformType hyperv1.PlatformType
		expectedNetwork   *operatorv1.Network
	}{
		{
			name:              "KubeVirt with OpenshiftSDN uses unique default vxlan port",
			inputNetwork:      NetworkOperator(),
			inputNetworkType:  hyperv1.OpenShiftSDN,
			inputPlatformType: hyperv1.KubevirtPlatform,
			expectedNetwork: &operatorv1.Network{
				ObjectMeta: NetworkOperator().ObjectMeta,
				Spec: operatorv1.NetworkSpec{
					OperatorSpec: operatorv1.OperatorSpec{
						ManagementState: "Managed",
					},
					DefaultNetwork: operatorv1.DefaultNetworkDefinition{
						OpenShiftSDNConfig: &operatorv1.OpenShiftSDNConfig{
							VXLANPort: &port,
						},
					},
				},
			},
		},
		{
			name: "KubeVirt with OpenshiftSDN does not set port when vxlan port already exists",
			inputNetwork: &operatorv1.Network{
				ObjectMeta: NetworkOperator().ObjectMeta,
				Spec: operatorv1.NetworkSpec{
					OperatorSpec: operatorv1.OperatorSpec{
						ManagementState: "Managed",
					},
					DefaultNetwork: operatorv1.DefaultNetworkDefinition{
						OpenShiftSDNConfig: &operatorv1.OpenShiftSDNConfig{
							VXLANPort: &fakePort,
						},
					},
				},
			},
			inputNetworkType:  hyperv1.OpenShiftSDN,
			inputPlatformType: hyperv1.KubevirtPlatform,
			expectedNetwork: &operatorv1.Network{
				ObjectMeta: NetworkOperator().ObjectMeta,
				Spec: operatorv1.NetworkSpec{
					OperatorSpec: operatorv1.OperatorSpec{
						ManagementState: "Managed",
					},
					DefaultNetwork: operatorv1.DefaultNetworkDefinition{
						OpenShiftSDNConfig: &operatorv1.OpenShiftSDNConfig{
							VXLANPort: &fakePort,
						},
					},
				},
			},
		},
		{
			name:              "KubeVirt with non SDN network does not set unique vxlan port",
			inputNetwork:      NetworkOperator(),
			inputNetworkType:  "fake",
			inputPlatformType: hyperv1.KubevirtPlatform,
			expectedNetwork: &operatorv1.Network{
				ObjectMeta: NetworkOperator().ObjectMeta,
				Spec: operatorv1.NetworkSpec{
					OperatorSpec: operatorv1.OperatorSpec{
						ManagementState: "Managed",
					},
				},
			},
		},
		{
			name:              "AWS with SDN does not set unique vxlan port",
			inputNetwork:      NetworkOperator(),
			inputNetworkType:  hyperv1.OpenShiftSDN,
			inputPlatformType: hyperv1.AWSPlatform,
			expectedNetwork: &operatorv1.Network{
				ObjectMeta: NetworkOperator().ObjectMeta,
				Spec: operatorv1.NetworkSpec{
					OperatorSpec: operatorv1.OperatorSpec{
						ManagementState: "Managed",
					},
				},
			},
		},
		{
			name:              "None with SDN does not set unique vxlan port",
			inputNetwork:      NetworkOperator(),
			inputNetworkType:  hyperv1.OpenShiftSDN,
			inputPlatformType: hyperv1.NonePlatform,
			expectedNetwork: &operatorv1.Network{
				ObjectMeta: NetworkOperator().ObjectMeta,
				Spec: operatorv1.NetworkSpec{
					OperatorSpec: operatorv1.OperatorSpec{
						ManagementState: "Managed",
					},
				},
			},
		},
		{
			name:              "IBM with SDN does not set unique vxlan port",
			inputNetwork:      NetworkOperator(),
			inputNetworkType:  hyperv1.OpenShiftSDN,
			inputPlatformType: hyperv1.IBMCloudPlatform,
			expectedNetwork: &operatorv1.Network{
				ObjectMeta: NetworkOperator().ObjectMeta,
				Spec: operatorv1.NetworkSpec{
					OperatorSpec: operatorv1.OperatorSpec{
						ManagementState: "Managed",
					},
				},
			},
		},
		{
			name:              "Azure with SDN does not set unique vxlan port",
			inputNetwork:      NetworkOperator(),
			inputNetworkType:  hyperv1.OpenShiftSDN,
			inputPlatformType: hyperv1.AzurePlatform,
			expectedNetwork: &operatorv1.Network{
				ObjectMeta: NetworkOperator().ObjectMeta,
				Spec: operatorv1.NetworkSpec{
					OperatorSpec: operatorv1.OperatorSpec{
						ManagementState: "Managed",
					},
				},
			},
		},
		{
			name:              "Agent with SDN does not set unique vxlan port",
			inputNetwork:      NetworkOperator(),
			inputNetworkType:  hyperv1.OpenShiftSDN,
			inputPlatformType: hyperv1.AgentPlatform,
			expectedNetwork: &operatorv1.Network{
				ObjectMeta: NetworkOperator().ObjectMeta,
				Spec: operatorv1.NetworkSpec{
					OperatorSpec: operatorv1.OperatorSpec{
						ManagementState: "Managed",
					},
				},
			},
		},
	}
	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			err := ReconcileNetworkOperator(tc.inputNetwork, tc.inputNetworkType, tc.inputPlatformType)
			g.Expect(err).To(BeNil())
			g.Expect(tc.inputNetwork).To(BeEquivalentTo(tc.expectedNetwork))
		})
	}
}
