package network

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	operatorv1 "github.com/openshift/api/operator/v1"
)

func TestReconcileDefaultIngressController(t *testing.T) {
	vxlanPort := kubevirtDefaultVXLANPort
	genevePort := kubevirtDefaultGenevePort
	v4InternalSubnet := kubevirtDefaultV4InternalSubnet

	fakePort := uint32(11111)
	testsCases := []struct {
		name              string
		inputNetwork      *operatorv1.Network
		inputNetworkType  hyperv1.NetworkType
		inputPlatformType hyperv1.PlatformType
		disableMultus     bool
		expectedNetwork   *operatorv1.Network
	}{
		{
			name:              "KubeVirt with OVNKubernetes uses unique default geneve port",
			inputNetwork:      NetworkOperator(),
			inputNetworkType:  hyperv1.OVNKubernetes,
			inputPlatformType: hyperv1.KubevirtPlatform,
			disableMultus:     false,
			expectedNetwork: &operatorv1.Network{
				ObjectMeta: NetworkOperator().ObjectMeta,
				Spec: operatorv1.NetworkSpec{
					OperatorSpec: operatorv1.OperatorSpec{
						ManagementState: "Managed",
					},
					DefaultNetwork: operatorv1.DefaultNetworkDefinition{
						OVNKubernetesConfig: &operatorv1.OVNKubernetesConfig{
							GenevePort:       &genevePort,
							V4InternalSubnet: v4InternalSubnet,
						},
					},
				},
			},
		},
		{
			name:              "KubeVirt with OpenShiftSDN uses unique default vxlan port",
			inputNetwork:      NetworkOperator(),
			inputNetworkType:  hyperv1.OpenShiftSDN,
			inputPlatformType: hyperv1.KubevirtPlatform,
			disableMultus:     false,
			expectedNetwork: &operatorv1.Network{
				ObjectMeta: NetworkOperator().ObjectMeta,
				Spec: operatorv1.NetworkSpec{
					OperatorSpec: operatorv1.OperatorSpec{
						ManagementState: "Managed",
					},
					DefaultNetwork: operatorv1.DefaultNetworkDefinition{
						OpenShiftSDNConfig: &operatorv1.OpenShiftSDNConfig{
							VXLANPort: &vxlanPort,
						},
					},
				},
			},
		},
		{
			name: "KubeVirt with OpenShiftSDN does not set port when vxlan port already exists",
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
			disableMultus:     false,
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
			name: "KubeVirt with OVNKubernetes when geneve port already exists",
			inputNetwork: &operatorv1.Network{
				ObjectMeta: NetworkOperator().ObjectMeta,
				Spec: operatorv1.NetworkSpec{
					OperatorSpec: operatorv1.OperatorSpec{
						ManagementState: "Managed",
					},
					DefaultNetwork: operatorv1.DefaultNetworkDefinition{
						OVNKubernetesConfig: &operatorv1.OVNKubernetesConfig{
							GenevePort:       &fakePort,
							V4InternalSubnet: kubevirtDefaultV4InternalSubnet,
						},
					},
				},
			},
			inputNetworkType:  hyperv1.OVNKubernetes,
			inputPlatformType: hyperv1.KubevirtPlatform,
			disableMultus:     false,
			expectedNetwork: &operatorv1.Network{
				ObjectMeta: NetworkOperator().ObjectMeta,
				Spec: operatorv1.NetworkSpec{
					OperatorSpec: operatorv1.OperatorSpec{
						ManagementState: "Managed",
					},
					DefaultNetwork: operatorv1.DefaultNetworkDefinition{
						OVNKubernetesConfig: &operatorv1.OVNKubernetesConfig{
							GenevePort:       &fakePort,
							V4InternalSubnet: kubevirtDefaultV4InternalSubnet,
						},
					},
				},
			},
		},
		{
			name: "KubeVirt with OVNKubernetes when v4InternalSubnet already exists",
			inputNetwork: &operatorv1.Network{
				ObjectMeta: NetworkOperator().ObjectMeta,
				Spec: operatorv1.NetworkSpec{
					OperatorSpec: operatorv1.OperatorSpec{
						ManagementState: "Managed",
					},
					DefaultNetwork: operatorv1.DefaultNetworkDefinition{
						OVNKubernetesConfig: &operatorv1.OVNKubernetesConfig{
							V4InternalSubnet: "100.66.0.0/16",
							GenevePort:       &genevePort,
						},
					},
				},
			},
			inputNetworkType:  hyperv1.OVNKubernetes,
			inputPlatformType: hyperv1.KubevirtPlatform,
			disableMultus:     false,
			expectedNetwork: &operatorv1.Network{
				ObjectMeta: NetworkOperator().ObjectMeta,
				Spec: operatorv1.NetworkSpec{
					OperatorSpec: operatorv1.OperatorSpec{
						ManagementState: "Managed",
					},
					DefaultNetwork: operatorv1.DefaultNetworkDefinition{
						OVNKubernetesConfig: &operatorv1.OVNKubernetesConfig{
							V4InternalSubnet: "100.66.0.0/16",
							GenevePort:       &genevePort,
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
			disableMultus:     false,
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
			disableMultus:     false,
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
			name:              "DisableMultus sets disableMultiNetwork to true",
			inputNetwork:      NetworkOperator(),
			inputNetworkType:  hyperv1.Other,
			inputPlatformType: hyperv1.AWSPlatform,
			disableMultus:     true,
			expectedNetwork: &operatorv1.Network{
				ObjectMeta: NetworkOperator().ObjectMeta,
				Spec: operatorv1.NetworkSpec{
					OperatorSpec: operatorv1.OperatorSpec{
						ManagementState: "Managed",
					},
					DisableMultiNetwork: &[]bool{true}[0],
				},
			},
		},
		{
			name:              "None with SDN does not set unique vxlan port",
			inputNetwork:      NetworkOperator(),
			inputNetworkType:  hyperv1.OpenShiftSDN,
			inputPlatformType: hyperv1.NonePlatform,
			disableMultus:     false,
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
			disableMultus:     false,
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
			disableMultus:     false,
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
			disableMultus:     false,
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
			ReconcileNetworkOperator(tc.inputNetwork, tc.inputNetworkType, tc.inputPlatformType, tc.disableMultus)
			g.Expect(tc.inputNetwork).To(BeEquivalentTo(tc.expectedNetwork))
		})
	}
}
