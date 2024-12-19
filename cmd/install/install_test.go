package install

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	corev1 "k8s.io/api/core/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
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

func TestSetupCRDs(t *testing.T) {
	tests := []struct {
		name         string
		inputOptions Options
		expectError  bool
	}{
		{
			name: "When is TechPreviewNoUpgrade it should have a single nodepool CRD with the TechPreviewNoUpgrade annotation",
			inputOptions: Options{
				TechPreviewNoUpgrade: true,
			},
		},
		{
			name:         "When is NOT TechPreviewNoUpgrade it should have a single nodepool CRD with the default annotation",
			inputOptions: Options{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			crds := setupCRDs(tc.inputOptions, &corev1.Namespace{}, nil)
			nodePoolCRDS := make([]crclient.Object, 0)
			var machineDeploymentCRD crclient.Object
			var awsEndpointServicesCRD crclient.Object
			for _, crd := range crds {
				if crd.GetName() == "nodepools.hypershift.openshift.io" {
					nodePoolCRDS = append(nodePoolCRDS, crd)
				}
				if crd.GetName() == "machinedeployments.cluster.x-k8s.io" {
					machineDeploymentCRD = crd
				}
				if crd.GetName() == "awsendpointservices.hypershift.openshift.io" {
					awsEndpointServicesCRD = crd
				}
			}

			// Smoke test to ensure that CRDs that should apply for any feature gate are present.
			g.Expect(machineDeploymentCRD).ToNot(BeNil())
			g.Expect(awsEndpointServicesCRD).ToNot(BeNil())

			// Validate the feature set specific CRDs are applied.
			g.Expect(nodePoolCRDS).To(HaveLen(1))
			if tc.inputOptions.TechPreviewNoUpgrade {
				g.Expect(nodePoolCRDS[0].GetAnnotations()["release.openshift.io/feature-set"]).To(Equal("TechPreviewNoUpgrade"))
				return
			}
			g.Expect(nodePoolCRDS[0].GetAnnotations()["release.openshift.io/feature-set"]).To(Equal("Default"))
		})
	}
}
