package util

import (
	"testing"

	. "github.com/onsi/gomega"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/log"
	"github.com/openshift/hypershift/support/api"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func Test_SetupAzureCredentials(t *testing.T) {
	tests := map[string]struct {
		testName               string
		credentials            *AzureCreds
		credentialsFile        string
		expectedSubscriptionID string
		expectedAzureCreds     *azidentity.DefaultAzureCredential
		expectedError          bool
	}{
		"valid credentials": {
			credentialsFile: "../../test/setup/fake_credentials",
			credentials: &AzureCreds{
				SubscriptionID: "89a",
				TenantID:       "60e",
				ClientID:       "f70",
				ClientSecret:   "8Q~",
			},
			expectedSubscriptionID: "89a",
			expectedError:          false,
		},
		"invalid credentials": {
			credentialsFile: "../../test/setup/fake_credential",
			credentials: &AzureCreds{
				SubscriptionID: "89a",
				TenantID:       "60e",
				ClientID:       "f70",
				ClientSecret:   "8Q~",
			},
			expectedSubscriptionID: "89a",
			expectedError:          false,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			subscriptionID, _, err := SetupAzureCredentials(log.Log, test.credentials, test.credentialsFile)
			if test.expectedError {
				g.Expect(err).To(MatchError(test.expectedError))
			} else {
				g.Expect(err).To(BeNil())
				g.Expect(subscriptionID).To(Equal(test.expectedSubscriptionID))
			}
		})
	}
}

func Test_ReadCredentials(t *testing.T) {
	tests := map[string]struct {
		path               string
		expectedAzureCreds *AzureCreds
		expectedError      bool
	}{
		"valid file": {
			path: "../../test/setup/fake_credentials",
			expectedAzureCreds: &AzureCreds{
				SubscriptionID: "89a",
				TenantID:       "60e",
				ClientID:       "f70",
				ClientSecret:   "8Q~",
			},
			expectedError: false,
		},
		"invalid file": {
			path:          "../../test/setup/fake_credential",
			expectedError: true,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			azureCreds, err := ReadCredentials(test.path)
			if test.expectedError {
				g.Expect(err).To(Not(BeNil()))
			} else {
				g.Expect(err).To(BeNil())
				g.Expect(azureCreds).To(Equal(test.expectedAzureCreds))
			}
		})
	}
}

func Test_ValidateMarketplaceFlags(t *testing.T) {
	tests := map[string]struct {
		marketplaceImageInfo map[string]*string
		expectedError        bool
	}{
		"valid marketplace image": {
			marketplaceImageInfo: map[string]*string{
				"marketplace-publisher": newStringPtr("publisher"),
				"marketplace-offer":     newStringPtr("offer"),
				"marketplace-sku":       newStringPtr("sku"),
				"marketplace-version":   newStringPtr("version"),
			},
			expectedError: false,
		},
		"invalid marketplace image": {
			marketplaceImageInfo: map[string]*string{
				"marketplace-publisher": newStringPtr("publisher"),
				"marketplace-offer":     newStringPtr(""),
				"marketplace-sku":       newStringPtr("sku"),
				"marketplace-version":   newStringPtr("version"),
			},
			expectedError: true,
		},
		"empty marketplace image": {
			marketplaceImageInfo: map[string]*string{
				"marketplace-publisher": newStringPtr(""),
				"marketplace-offer":     newStringPtr(""),
				"marketplace-sku":       nil,
				"marketplace-version":   nil,
			},
			expectedError: false,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			err := ValidateMarketplaceFlags(test.marketplaceImageInfo)
			if test.expectedError {
				g.Expect(err).To(Not(BeNil()))
			} else {
				g.Expect(err).To(BeNil())
			}
		})
	}
}

func TestReadManagedIdentityConfiguration(t *testing.T) {
	tests := map[string]struct {
		path                      string
		expectedManagedIdentities []ManagedIdentityInfo
		expectedError             bool
	}{
		"valid file, expect no error": {
			path: "../../test/setup/fake-config.json",
			expectedManagedIdentities: []ManagedIdentityInfo{
				{
					Name:     "azure-disk",
					ClientID: "123-123-123-123",
					CertName: "azure-disk",
				},
				{
					Name:     "azure-file",
					ClientID: "123-123-123-1234",
					CertName: "azure-file",
				},
				{
					Name:     "capz",
					ClientID: "123-123-123-1235",
					CertName: "capz",
				},
				{
					Name:     "cloud-provider",
					ClientID: "123-123-123-1236",
					CertName: "cloud-provider",
				},
				{
					Name:     "cncc",
					ClientID: "123-123-123-1237",
					CertName: "cncc",
				},
				{
					Name:     "control-plane",
					ClientID: "123-123-123-1238",
					CertName: "control-plane",
				},
				{
					Name:     "ingress",
					ClientID: "123-123-123-1239",
					CertName: "ingress",
				},
			},
			expectedError: false,
		},
		"json file is badly formed, expect error": {
			path:          "../../test/setup/fake-incorrect-config.json",
			expectedError: true,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			managedIdentities, err := readManagedIdentityConfiguration(test.path)
			if test.expectedError {
				g.Expect(err).To(Not(BeNil()))
			} else {
				g.Expect(err).To(BeNil())
				g.Expect(managedIdentities).To(Equal(test.expectedManagedIdentities))
			}
		})
	}
}

func TestSetupManagedIdentityCredentials(t *testing.T) {
	tests := map[string]struct {
		configFilePath string
		hc             *hyperv1.HostedCluster

		expectedHC    *hyperv1.HostedCluster
		expectedError bool
	}{
		"valid config file, expect no error": {
			configFilePath: "../../test/setup/fake-config.json",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hc",
					Namespace: "clusters",
				},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type:  hyperv1.AzurePlatform,
						Azure: &hyperv1.AzurePlatformSpec{},
					},
				},
			},
			expectedHC: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hc",
					Namespace: "clusters",
				},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzurePlatformSpec{
							ManagedIdentities: hyperv1.AzureResourceManagedIdentities{
								ControlPlane: hyperv1.ControlPlaneManagedIdentities{
									CloudProvider: hyperv1.ManagedIdentity{
										ClientID:        "123-123-123-1236",
										CertificateName: "cloud-provider",
									},
									ClusterAPIAzure: hyperv1.ManagedIdentity{
										ClientID:        "123-123-123-1235",
										CertificateName: "capz",
									},
									ControlPlane: hyperv1.ManagedIdentity{
										ClientID:        "123-123-123-1238",
										CertificateName: "control-plane",
									},
									ImageRegistry: hyperv1.ManagedIdentity{
										ClientID:        "123-123-123-12310",
										CertificateName: "image-registry",
									},
									Ingress: hyperv1.ManagedIdentity{
										ClientID:        "123-123-123-1239",
										CertificateName: "ingress",
									},
									Network: hyperv1.ManagedIdentity{
										ClientID:        "123-123-123-1237",
										CertificateName: "cncc",
									},
									Disk: hyperv1.ManagedIdentity{
										ClientID:        "123-123-123-123",
										CertificateName: "azure-disk",
									},
									File: hyperv1.ManagedIdentity{
										ClientID:        "123-123-123-1234",
										CertificateName: "azure-file",
									},
								},
							},
						},
					},
				},
			},
			expectedError: false,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			objs := []crclient.Object{test.hc}

			_ = fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(objs...).Build()

			err := SetupManagedIdentityCredentials(test.configFilePath, test.hc)
			if test.expectedError {
				g.Expect(err).To(Not(BeNil()))
			} else {
				g.Expect(err).To(BeNil())
				g.Expect(test.hc.Spec.Platform.Azure.ManagedIdentities).To(Equal(test.expectedHC.Spec.Platform.Azure.ManagedIdentities))
			}
		})
	}
}

func newStringPtr(s string) *string {
	return &s
}
