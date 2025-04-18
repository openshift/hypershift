package azureutil

import (
	"reflect"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/config"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
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

func TestIsAroHCP(t *testing.T) {
	testCases := []struct {
		name          string
		envVarValue   string
		expectedValue bool
	}{
		{
			name:          "Sets the managed service env var to hyperv1.AroHCP so the function should return true",
			envVarValue:   hyperv1.AroHCP,
			expectedValue: true,
		},
		{
			name:          "Sets the managed service env var to nothing so the function should return false",
			envVarValue:   "",
			expectedValue: false,
		},
		{
			name:          "Sets the managed service env var to 'asdf' so the function should return false",
			envVarValue:   "asdf",
			expectedValue: false,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			t.Setenv("MANAGED_SERVICE", tc.envVarValue)
			isAroHcp := IsAroHCP()
			g.Expect(isAroHcp).To(Equal(tc.expectedValue))
		})
	}
}

func TestCreateEnvVarsForAzureManagedIdentity(t *testing.T) {
	type args struct {
		azureCredentialsFilepath string
	}
	tests := []struct {
		name string
		args args
		want []corev1.EnvVar
	}{
		{
			name: "returns a slice of environment variables with the azure creds",
			args: args{
				azureCredentialsFilepath: "my-credentials-file",
			},
			want: []corev1.EnvVar{
				{
					Name:  config.ManagedAzureCredentialsFilePath,
					Value: config.ManagedAzureCertificatePath + "my-credentials-file",
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CreateEnvVarsForAzureManagedIdentity(tt.args.azureCredentialsFilepath); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CreateEnvVarsForAzureManagedIdentity() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCreateVolumeMountForAzureSecretStoreProviderClass(t *testing.T) {
	tests := []struct {
		name                  string
		secretStoreVolumeName string
		want                  corev1.VolumeMount
	}{
		{
			name:                  "return a volume mount for a secret store provider",
			secretStoreVolumeName: "my-secret-store",
			want: corev1.VolumeMount{
				Name:      "my-secret-store",
				MountPath: config.ManagedAzureCertificateMountPath,
				ReadOnly:  true,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CreateVolumeMountForAzureSecretStoreProviderClass(tt.secretStoreVolumeName); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CreateVolumeMountForAzureSecretStoreProviderClass() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCreateVolumeForAzureSecretStoreProviderClass(t *testing.T) {
	tests := []struct {
		name                    string
		secretStoreVolumeName   string
		secretProviderClassName string
		want                    corev1.Volume
	}{
		{
			name:                    "return a volume for a secret store provider",
			secretStoreVolumeName:   "my-secret-store",
			secretProviderClassName: "my-secret-provider-class",
			want: corev1.Volume{
				Name: "my-secret-store",
				VolumeSource: corev1.VolumeSource{
					CSI: &corev1.CSIVolumeSource{
						Driver:   config.ManagedAzureSecretsStoreCSIDriver,
						ReadOnly: ptr.To(true),
						VolumeAttributes: map[string]string{
							config.ManagedAzureSecretProviderClass: "my-secret-provider-class",
						},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CreateVolumeForAzureSecretStoreProviderClass(tt.secretStoreVolumeName, tt.secretProviderClassName); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CreateVolumeForAzureSecretStoreProviderClass() = %v, want %v", got, tt.want)
			}
		})
	}
}
