package util

import (
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/cmd/log"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
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

func newStringPtr(s string) *string {
	return &s
}
