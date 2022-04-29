package install

import (
	"testing"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
)

func TestOptions_Validate(t *testing.T) {
	tests := map[string]struct {
		inputOptions Options
		expectError  bool
	}{
		"when aws private platform without private creds or secret reference and region it errors": {
			inputOptions: Options{
				PrivatePlatform: string(hyperv1.AWSPlatform),
			},
			expectError: true,
		},
		"when aws private platform with private creds and region there is no error": {
			inputOptions: Options{
				PrivatePlatform:  string(hyperv1.AWSPlatform),
				AWSPrivateCreds:  "/path/to/credentials",
				AWSPrivateRegion: "us-east-1",
			},
			expectError: false,
		},
		"when aws private platform with secret and region there is no error": {
			inputOptions: Options{
				PrivatePlatform:             string(hyperv1.AWSPlatform),
				AWSPrivateCredentialsSecret: "my-secret",
				AWSPrivateRegion:            "us-east-1",
			},
			expectError: false,
		},
		"when empty private platform is specified it errors": {
			inputOptions: Options{},
			expectError:  true,
		},
		"when partially specified oauth creds used (OIDCStorageProviderS3Credentials) it errors": {
			inputOptions: Options{
				OIDCStorageProviderS3Credentials: "mycreds",
			},
			expectError: true,
		},
		"when partially specified oauth creds used (OIDCStorageProviderS3CredentialsSecret) it errors": {
			inputOptions: Options{
				OIDCStorageProviderS3CredentialsSecret: "mysecret",
			},
			expectError: true,
		},
		"when external-dns provider is set without creds it errors": {
			inputOptions: Options{
				ExternalDNSProvider:     "aws",
				ExternalDNSDomainFilter: "test.com",
			},
			expectError: true,
		},
		"when external-dns provider is set with both creds methods it errors": {
			inputOptions: Options{
				ExternalDNSProvider:          "aws",
				ExternalDNSCredentials:       "/path/to/credentials",
				ExternalDNSCredentialsSecret: "creds-secret",
				ExternalDNSDomainFilter:      "test.com",
			},
			expectError: true,
		},
		"when external-dns provider is set without domain filter it errors": {
			inputOptions: Options{
				ExternalDNSProvider:    "aws",
				ExternalDNSCredentials: "/path/to/credentials",
			},
			expectError: true,
		},
		"when all data specified there is no error": {
			inputOptions: Options{
				PrivatePlatform:                           string(hyperv1.NonePlatform),
				OIDCStorageProviderS3CredentialsSecret:    "mysecret",
				OIDCStorageProviderS3Region:               "us-east-1",
				OIDCStorageProviderS3CredentialsSecretKey: "mykey",
				OIDCStorageProviderS3BucketName:           "mybucket",
			},
			expectError: false,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			err := test.inputOptions.Validate()
			if test.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).To(BeNil())
			}
		})
	}
}
