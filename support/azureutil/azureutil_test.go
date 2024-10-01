package azureutil

import (
	"context"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	. "github.com/onsi/gomega"
	"github.com/openshift/hypershift/support/api"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestGetSubnetNameFromSubnetID(t *testing.T) {
	tests := []struct {
		testCaseName       string
		subnetID           string
		expectedSubnetName string
		expectedErr        bool
	}{
		{
			testCaseName:       "empty subnet ID",
			subnetID:           "",
			expectedSubnetName: "",
			expectedErr:        true,
		},
		{
			testCaseName:       "improperly formed subnet ID",
			subnetID:           "/subscriptions/mySubscriptionID/resourceGroups/myResourceGroupName/providers/Microsoft.Network/virtualNetworks/myVnetName/subnets",
			expectedSubnetName: "",
			expectedErr:        true,
		},
		{
			testCaseName:       "properly formed subnet ID",
			subnetID:           "/subscriptions/mySubscriptionID/resourceGroups/myResourceGroupName/providers/Microsoft.Network/virtualNetworks/myVnetName/subnets/mySubnetName",
			expectedSubnetName: "mySubnetName",
			expectedErr:        false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.testCaseName, func(t *testing.T) {
			g := NewGomegaWithT(t)
			subnetID, err := GetSubnetNameFromSubnetID(tc.subnetID)
			if tc.expectedErr {
				g.Expect(err).To(Not(BeNil()))
				g.Expect(err).To(HaveOccurred(), "invalid subnet ID format: "+tc.subnetID)
			} else {
				g.Expect(err).To(BeNil())
				g.Expect(subnetID).To(Equal(tc.expectedSubnetName))
			}
		})
	}
}

func TestGetNetworkSecurityGroupNameFromNetworkSecurityGroupID(t *testing.T) {
	tests := []struct {
		testCaseName    string
		nsgID           string
		expectedNSGName string
		expectedNSGRG   string
		expectedErr     bool
	}{
		{
			testCaseName:    "empty NSG ID",
			nsgID:           "",
			expectedNSGName: "",
			expectedNSGRG:   "",
			expectedErr:     true,
		},
		{
			testCaseName:    "improperly formed nsg ID",
			nsgID:           "/subscriptions/mySubscriptionID/resourceGroups/myResourceGroupName/providers/Microsoft.Network/networkSecurityGroups",
			expectedNSGName: "",
			expectedNSGRG:   "",
			expectedErr:     true,
		},
		{
			testCaseName:    "properly formed nsg ID",
			nsgID:           "/subscriptions/mySubscriptionID/resourceGroups/myResourceGroupName/providers/Microsoft.Network/networkSecurityGroups/myNSGName",
			expectedNSGName: "myNSGName",
			expectedNSGRG:   "myResourceGroupName",
			expectedErr:     false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.testCaseName, func(t *testing.T) {
			g := NewGomegaWithT(t)
			nsgName, nsgRG, err := GetNameAndResourceGroupFromNetworkSecurityGroupID(tc.nsgID)
			if tc.expectedErr {
				g.Expect(err).To(Not(BeNil()))
				g.Expect(err).To(HaveOccurred(), "invalid nsg ID format: "+tc.nsgID)
			} else {
				g.Expect(err).To(BeNil())
				g.Expect(nsgName).To(Equal(tc.expectedNSGName))
				g.Expect(nsgRG).To(Equal(tc.expectedNSGRG))
			}
		})
	}
}

func TestGetVnetNameAndResourceGroupFromVnetID(t *testing.T) {
	tests := []struct {
		testCaseName     string
		vnetID           string
		expectedVnetName string
		expectedVnetRG   string
		expectedErr      bool
	}{
		{
			testCaseName:     "empty VNET ID",
			vnetID:           "",
			expectedVnetName: "",
			expectedVnetRG:   "",
			expectedErr:      true,
		},
		{
			testCaseName:     "improperly formed VNET ID",
			vnetID:           "/subscriptions/mySubscriptionID/resourceGroups/myResourceGroupName/providers/Microsoft.Network/virtualNetworks/",
			expectedVnetName: "",
			expectedVnetRG:   "",
			expectedErr:      true,
		},
		{
			testCaseName:     "properly formed VNET ID",
			vnetID:           "/subscriptions/mySubscriptionID/resourceGroups/myResourceGroupName/providers/Microsoft.Network/virtualNetworks/myVnetName",
			expectedVnetName: "myVnetName",
			expectedVnetRG:   "myResourceGroupName",
			expectedErr:      false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.testCaseName, func(t *testing.T) {
			g := NewGomegaWithT(t)
			vnetName, vnetRG, err := GetVnetNameAndResourceGroupFromVnetID(tc.vnetID)
			if tc.expectedErr {
				g.Expect(err).To(Not(BeNil()))
				g.Expect(err).To(HaveOccurred(), "invalid VNET ID format: "+tc.vnetID)
			} else {
				g.Expect(err).To(BeNil())
				g.Expect(vnetName).To(Equal(tc.expectedVnetName))
				g.Expect(vnetRG).To(Equal(tc.expectedVnetRG))
			}
		})
	}
}

func TestGetAzureCredentialsFromSecret(t *testing.T) {
	tests := []struct {
		testCaseName string
		hc           *hyperv1.HostedCluster
		secret       *corev1.Secret
		expectedErr  bool
	}{
		{
			testCaseName: "nominal test case",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "clusters",
				},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzurePlatformSpec{
							Credentials: corev1.LocalObjectReference{Name: "cloud-credentials"},
						},
					},
				},
			},
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cloud-credentials",
					Namespace: "clusters",
				},
				Data: map[string][]byte{
					"AZURE_CLIENT_ID":     []byte("46fb37b5"),
					"AZURE_CLIENT_SECRET": []byte("46fb37b5"),
					"AZURE_TENANT_ID":     []byte("46fb37b5"),
				},
			},
			expectedErr: false,
		},
		{
			testCaseName: "wrong secret name, err",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "clusters",
				},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzurePlatformSpec{
							Credentials: corev1.LocalObjectReference{Name: "cloud-credentialss"},
						},
					},
				},
			},
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cloud-credentials",
					Namespace: "clusters",
				},
				Data: map[string][]byte{
					"AZURE_CLIENT_ID":     []byte("46fb37b5"),
					"AZURE_CLIENT_SECRET": []byte("46fb37b5"),
					"AZURE_TENANT_ID":     []byte("46fb37b5"),
				},
			},
			expectedErr: true,
		},
		{
			testCaseName: "missing date from secret, err",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "clusters",
				},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzurePlatformSpec{
							Credentials: corev1.LocalObjectReference{Name: "cloud-credentialss"},
						},
					},
				},
			},
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cloud-credentials",
					Namespace: "clusters",
				},
				Data: map[string][]byte{
					"AZURE_CLIENT_ID": []byte("46fb37b5"),
					"AZURE_TENANT_ID": []byte("46fb37b5"),
				},
			},
			expectedErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.testCaseName, func(t *testing.T) {
			g := NewGomegaWithT(t)

			objs := []crclient.Object{tc.hc, tc.secret}

			client := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(objs...).Build()

			creds, err := GetAzureCredentialsFromSecret(context.TODO(), client, tc.hc.Namespace, tc.hc.Spec.Platform.Azure.Credentials.Name)
			if !tc.expectedErr {
				g.Expect(err).To(BeNil())
				g.Expect(creds.Name).To(Equal(tc.hc.Spec.Platform.Azure.Credentials.Name))
			} else {
				g.Expect(err).To(Not(BeNil()))
			}
		})
	}
}
