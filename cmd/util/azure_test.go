package util

import (
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/cmd/log"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

func TestSetupAzureCredentials(t *testing.T) {
	tests := map[string]struct {
		testName               string
		credentials            *AzureCreds
		credentialsFile        string
		expectedSubscriptionID string
		expectedAzureCreds     *azidentity.DefaultAzureCredential
		expectedError          bool
	}{
		"When credentials are valid it should return the subscription ID": {
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
		"When credentials file is invalid it should still return the subscription ID": {
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

func TestReadCredentials(t *testing.T) {
	tests := map[string]struct {
		path               string
		expectedAzureCreds *AzureCreds
		expectedError      bool
	}{
		"When file is valid it should return credentials": {
			path: "../../test/setup/fake_credentials",
			expectedAzureCreds: &AzureCreds{
				SubscriptionID: "89a",
				TenantID:       "60e",
				ClientID:       "f70",
				ClientSecret:   "8Q~",
			},
			expectedError: false,
		},
		"When file is invalid it should return an error": {
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

func TestValidateMarketplaceFlags(t *testing.T) {
	tests := map[string]struct {
		marketplaceImageInfo map[string]*string
		expectedError        bool
	}{
		"When marketplace image is valid it should pass validation": {
			marketplaceImageInfo: map[string]*string{
				"marketplace-publisher": newStringPtr("publisher"),
				"marketplace-offer":     newStringPtr("offer"),
				"marketplace-sku":       newStringPtr("sku"),
				"marketplace-version":   newStringPtr("version"),
			},
			expectedError: false,
		},
		"When marketplace image has empty offer it should return an error": {
			marketplaceImageInfo: map[string]*string{
				"marketplace-publisher": newStringPtr("publisher"),
				"marketplace-offer":     newStringPtr(""),
				"marketplace-sku":       newStringPtr("sku"),
				"marketplace-version":   newStringPtr("version"),
			},
			expectedError: true,
		},
		"When marketplace image is empty it should pass validation": {
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
