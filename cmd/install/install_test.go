package install

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	aws "github.com/openshift/hypershift/cmd/infra/aws"
	"github.com/openshift/hypershift/cmd/install/assets"
	crdassets "github.com/openshift/hypershift/cmd/install/assets/crds"
	"github.com/openshift/hypershift/hypershift-operator/controllers/sharedingress"
	hyperapi "github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/metrics"

	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/set"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
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
		"When AWS private platform with role ARN and region it should succeed": {
			inputOptions: Options{
				PrivatePlatform:         string(hyperv1.AWSPlatform),
				AWSPrivateRoleARN:       "arn:aws:iam::123456789012:role/op-ec2",
				AWSPrivateRegion:        "us-east-1",
				AWSRoleCredentialSource: "web-identity",
			},
			expectError: false,
		},
		"When AWS private platform with role ARN and creds file it should error": {
			inputOptions: Options{
				PrivatePlatform:         string(hyperv1.AWSPlatform),
				AWSPrivateRoleARN:       "arn:aws:iam::123456789012:role/op-ec2",
				AWSPrivateCreds:         "/path/to/credentials",
				AWSPrivateRegion:        "us-east-1",
				AWSRoleCredentialSource: "web-identity",
			},
			expectError: true,
		},
		"When AWS private platform with both creds file and secret it should error": {
			inputOptions: Options{
				PrivatePlatform:             string(hyperv1.AWSPlatform),
				AWSPrivateCreds:             "/path/to/credentials",
				AWSPrivateCredentialsSecret: "my-secret",
				AWSPrivateRegion:            "us-east-1",
			},
			expectError: true,
		},
		"When AWS private platform with role ARN and no region it should error": {
			inputOptions: Options{
				PrivatePlatform:         string(hyperv1.AWSPlatform),
				AWSPrivateRoleARN:       "arn:aws:iam::123456789012:role/op-ec2",
				AWSRoleCredentialSource: "web-identity",
			},
			expectError: true,
		},
		"When role ARN is set with invalid credential source it should error": {
			inputOptions: Options{
				PrivatePlatform:         string(hyperv1.AWSPlatform),
				AWSPrivateRoleARN:       "arn:aws:iam::123456789012:role/op-ec2",
				AWSPrivateRegion:        "us-east-1",
				AWSRoleCredentialSource: "invalid-source",
			},
			expectError: true,
		},
		"When role ARN is set with web-identity credential source it should succeed": {
			inputOptions: Options{
				PrivatePlatform:         string(hyperv1.AWSPlatform),
				AWSPrivateRoleARN:       "arn:aws:iam::123456789012:role/op-ec2",
				AWSPrivateRegion:        "us-east-1",
				AWSRoleCredentialSource: "web-identity",
			},
			expectError: false,
		},
		"When role ARN is set with ec2-instance-metadata credential source it should succeed": {
			inputOptions: Options{
				PrivatePlatform:         string(hyperv1.AWSPlatform),
				AWSPrivateRoleARN:       "arn:aws:iam::123456789012:role/op-ec2",
				AWSPrivateRegion:        "us-east-1",
				AWSRoleCredentialSource: "ec2-instance-metadata",
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
		"when GCP private platform with only gcp-project it errors": {
			inputOptions: Options{
				PrivatePlatform: string(hyperv1.GCPPlatform),
				GCPProject:      "my-project",
			},
			expectError: true,
		},
		"when GCP private platform with only gcp-region it errors": {
			inputOptions: Options{
				PrivatePlatform: string(hyperv1.GCPPlatform),
				GCPRegion:       "us-central1",
			},
			expectError: true,
		},
		"when GCP private platform with both gcp-project and gcp-region it succeeds": {
			inputOptions: Options{
				PrivatePlatform: string(hyperv1.GCPPlatform),
				GCPProject:      "my-project",
				GCPRegion:       "us-central1",
			},
			expectError: false,
		},
		"when GCP private platform without gcp-project and gcp-region it succeeds": {
			inputOptions: Options{
				PrivatePlatform: string(hyperv1.GCPPlatform),
			},
			expectError: false,
		},
		"when external-dns GCP provider is set without credentials it succeeds (Workload Identity)": {
			inputOptions: Options{
				PrivatePlatform:          string(hyperv1.GCPPlatform),
				ExternalDNSProvider:      "google",
				ExternalDNSDomainFilter:  "test.com",
				ExternalDNSGoogleProject: "my-project",
			},
			expectError: false,
		},
		"when external-dns GCP provider is set with credentials it succeeds": {
			inputOptions: Options{
				PrivatePlatform:          string(hyperv1.GCPPlatform),
				ExternalDNSProvider:      "google",
				ExternalDNSDomainFilter:  "test.com",
				ExternalDNSGoogleProject: "my-project",
				ExternalDNSCredentials:   "/path/to/credentials",
			},
			expectError: false,
		},
		"when external-dns GCP provider is set without google-project it succeeds": {
			inputOptions: Options{
				PrivatePlatform:         string(hyperv1.GCPPlatform),
				ExternalDNSProvider:     "google",
				ExternalDNSDomainFilter: "test.com",
			},
			expectError: false,
		},
		"When Azure private platform is specified without credentials, it should error": {
			inputOptions: Options{
				PrivatePlatform: string(hyperv1.AzurePlatform),
			},
			expectError: true,
		},
		"When Azure private platform is specified with creds file, it should succeed": {
			inputOptions: Options{
				PrivatePlatform:       string(hyperv1.AzurePlatform),
				AzurePrivateCreds:     "/dev/null",
				AzurePLSResourceGroup: "rg-mgmt",
			},
			expectError: false,
		},
		"When Azure private platform is specified with secret reference, it should succeed": {
			inputOptions: Options{
				PrivatePlatform:               string(hyperv1.AzurePlatform),
				AzurePrivateCredentialsSecret: "my-azure-secret",
				AzurePLSResourceGroup:         "rg-mgmt",
			},
			expectError: false,
		},
		"When Azure private platform is specified with both creds and secret, it should error": {
			inputOptions: Options{
				PrivatePlatform:               string(hyperv1.AzurePlatform),
				AzurePrivateCreds:             "/dev/null",
				AzurePrivateCredentialsSecret: "my-azure-secret",
			},
			expectError: true,
		},
		"When Azure private platform is specified for ARO HCP without credentials, it should succeed": {
			inputOptions: Options{
				PrivatePlatform: string(hyperv1.AzurePlatform),
				ManagedService:  hyperv1.AroHCP,
			},
			expectError: false,
		},
		"When Azure private platform is specified with managed identity and subscription ID, it should succeed": {
			inputOptions: Options{
				PrivatePlatform:                 string(hyperv1.AzurePlatform),
				AzurePLSManagedIdentityClientID: "00000000-0000-0000-0000-000000000001",
				AzurePLSSubscriptionID:          "00000000-0000-0000-0000-000000000002",
				AzurePLSResourceGroup:           "rg-mgmt",
			},
			expectError: false,
		},
		"When Azure private platform is specified without resource group, it should error": {
			inputOptions: Options{
				PrivatePlatform:   string(hyperv1.AzurePlatform),
				AzurePrivateCreds: "/dev/null",
			},
			expectError: true,
		},
		"When Azure private platform is specified with managed identity but no subscription ID, it should error": {
			inputOptions: Options{
				PrivatePlatform:                 string(hyperv1.AzurePlatform),
				AzurePLSManagedIdentityClientID: "00000000-0000-0000-0000-000000000001",
			},
			expectError: true,
		},
		"When Azure private platform is specified with managed identity and creds file, it should error": {
			inputOptions: Options{
				PrivatePlatform:                 string(hyperv1.AzurePlatform),
				AzurePLSManagedIdentityClientID: "00000000-0000-0000-0000-000000000001",
				AzurePLSSubscriptionID:          "00000000-0000-0000-0000-000000000002",
				AzurePrivateCreds:               "/dev/null",
			},
			expectError: true,
		},
		"When Azure private platform is specified with managed identity and secret, it should error": {
			inputOptions: Options{
				PrivatePlatform:                 string(hyperv1.AzurePlatform),
				AzurePLSManagedIdentityClientID: "00000000-0000-0000-0000-000000000001",
				AzurePLSSubscriptionID:          "00000000-0000-0000-0000-000000000002",
				AzurePrivateCredentialsSecret:   "my-azure-secret",
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
		"when image pull policy is not set there is no error": {
			inputOptions: Options{
				PrivatePlatform: string(hyperv1.NonePlatform),
			},
			expectError: false,
		},
		"when valid image pull policy is set there is no error": {
			inputOptions: Options{
				PrivatePlatform: string(hyperv1.NonePlatform),
				ImagePullPolicy: "Always",
			},
			expectError: false,
		},
		"when invalid image pull policy is set it errors": {
			inputOptions: Options{
				PrivatePlatform: string(hyperv1.NonePlatform),
				ImagePullPolicy: "InvalidPolicy",
			},
			expectError: true,
		},
		"When Azure private platform with managed identity and creds file it should error": {
			inputOptions: Options{
				PrivatePlatform:                 string(hyperv1.AzurePlatform),
				AzurePrivateCreds:               "/path/to/credentials",
				AzurePLSManagedIdentityClientID: "client-id",
				AzurePLSSubscriptionID:          "sub-id",
			},
			expectError: true,
		},
		"When Azure private platform with managed identity and secret it should error": {
			inputOptions: Options{
				PrivatePlatform:                 string(hyperv1.AzurePlatform),
				AzurePrivateCredentialsSecret:   "my-secret",
				AzurePLSManagedIdentityClientID: "client-id",
				AzurePLSSubscriptionID:          "sub-id",
			},
			expectError: true,
		},
		"When Azure private platform with managed identity but no subscription ID it should error": {
			inputOptions: Options{
				PrivatePlatform:                 string(hyperv1.AzurePlatform),
				AzurePLSManagedIdentityClientID: "client-id",
			},
			expectError: true,
		},
		"When Azure private platform with managed identity and subscription ID it should succeed": {
			inputOptions: Options{
				PrivatePlatform:                 string(hyperv1.AzurePlatform),
				AzurePLSManagedIdentityClientID: "client-id",
				AzurePLSSubscriptionID:          "sub-id",
				AzurePLSResourceGroup:           "rg-mgmt",
			},
			expectError: false,
		},
		"When Azure private platform with creds file it should succeed": {
			inputOptions: Options{
				PrivatePlatform:       string(hyperv1.AzurePlatform),
				AzurePrivateCreds:     "/path/to/credentials",
				AzurePLSResourceGroup: "rg-mgmt",
			},
			expectError: false,
		},
		"when scale-from-zero provider is missing but creds provided it errors": {
			inputOptions: Options{
				PrivatePlatform:    string(hyperv1.AWSPlatform),
				AWSPrivateCreds:    "/dev/null",
				AWSPrivateRegion:   "us-east-1",
				ScaleFromZeroCreds: "/path/to/creds",
			},
			expectError: true,
		},
		"when scale-from-zero provider is invalid it errors": {
			inputOptions: Options{
				PrivatePlatform:       string(hyperv1.AWSPlatform),
				AWSPrivateCreds:       "/dev/null",
				AWSPrivateRegion:      "us-east-1",
				ScaleFromZeroProvider: "gcp",
				ScaleFromZeroCreds:    "/path/to/creds",
			},
			expectError: true,
		},
		"when scale-from-zero both creds and secret provided it errors": {
			inputOptions: Options{
				PrivatePlatform:                string(hyperv1.AWSPlatform),
				AWSPrivateCreds:                "/dev/null",
				AWSPrivateRegion:               "us-east-1",
				ScaleFromZeroProvider:          "aws",
				ScaleFromZeroCreds:             "/path/to/creds",
				ScaleFromZeroCredentialsSecret: "my-secret",
			},
			expectError: true,
		},
		"when scale-from-zero provider is aws with creds file there is no error": {
			inputOptions: Options{
				PrivatePlatform:       string(hyperv1.AWSPlatform),
				AWSPrivateCreds:       "/dev/null",
				AWSPrivateRegion:      "us-east-1",
				ScaleFromZeroProvider: "aws",
				ScaleFromZeroCreds:    "/dev/null", // Use /dev/null as it always exists
			},
			expectError: false,
		},
		"when scale-from-zero provider is aws with secret reference there is no error": {
			inputOptions: Options{
				PrivatePlatform:                   string(hyperv1.AWSPlatform),
				AWSPrivateCreds:                   "/dev/null",
				AWSPrivateRegion:                  "us-east-1",
				ScaleFromZeroProvider:             "aws",
				ScaleFromZeroCredentialsSecret:    "my-secret",
				ScaleFromZeroCredentialsSecretKey: "credentials",
			},
			expectError: false,
		},
		"when install-scope is all it should not error": {
			inputOptions: Options{
				PrivatePlatform: string(hyperv1.NonePlatform),
				InstallScope:    string(OutputAll),
			},
			expectError: false,
		},
		"when install-scope is crds it should not error": {
			inputOptions: Options{
				PrivatePlatform: string(hyperv1.NonePlatform),
				InstallScope:    string(OutputCRDs),
			},
			expectError: false,
		},
		"when install-scope is resources it should not error": {
			inputOptions: Options{
				PrivatePlatform: string(hyperv1.NonePlatform),
				InstallScope:    string(OutputResources),
			},
			expectError: false,
		},
		"when install-scope is invalid it should error": {
			inputOptions: Options{
				PrivatePlatform: string(hyperv1.NonePlatform),
				InstallScope:    "bogus",
			},
			expectError: true,
		},
		"when install-scope is crds with wait-until-available it should error": {
			inputOptions: Options{
				PrivatePlatform:    string(hyperv1.NonePlatform),
				InstallScope:       string(OutputCRDs),
				WaitUntilAvailable: true,
			},
			expectError: true,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			err := test.inputOptions.Complete()
			if err == nil {
				err = test.inputOptions.Validate()
			}
			if test.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).To(BeNil())
			}
		})
	}
}

func TestCRDIncludeFilter(t *testing.T) {
	defaultCRD := func() *apiextensionsv1.CustomResourceDefinition {
		return &apiextensionsv1.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					"release.openshift.io/feature-set": "Default",
				},
			},
		}
	}

	tests := []struct {
		name   string
		opts   Options
		path   string
		crd    *apiextensionsv1.CustomResourceDefinition
		ipam   set.Set[string]
		expect bool
	}{
		{
			name:   "When path contains payload-manifests, it should be excluded",
			path:   "hypershift-operator/payload-manifests/something.yaml",
			crd:    defaultCRD(),
			expect: false,
		},
		{
			name:   "When path contains tests/, it should be excluded",
			path:   "hypershift-operator/tests/something.yaml",
			crd:    defaultCRD(),
			expect: false,
		},
		{
			name:   "When path contains etcd and ExcludeEtcdManifests is true, it should be excluded",
			opts:   Options{ExcludeEtcdManifests: true},
			path:   "cluster-api/etcd-something.yaml",
			crd:    defaultCRD(),
			expect: false,
		},
		{
			name:   "When path contains etcd and ExcludeEtcdManifests is false, it should be included",
			opts:   Options{ExcludeEtcdManifests: false},
			path:   "cluster-api/etcd-something.yaml",
			crd:    defaultCRD(),
			expect: true,
		},
		{
			name: "When path is a generated CRD with TechPreviewNoUpgrade annotation and TechPreviewNoUpgrade is true, it should be included",
			opts: Options{TechPreviewNoUpgrade: true},
			path: "hypershift-operator/zz_generated.crd-manifests/nodepools-TechPreviewNoUpgrade.yaml",
			crd: &apiextensionsv1.CustomResourceDefinition{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"release.openshift.io/feature-set": "TechPreviewNoUpgrade",
					},
				},
			},
			expect: true,
		},
		{
			name:   "When path is a generated CRD with Default annotation and TechPreviewNoUpgrade is true, it should be excluded",
			opts:   Options{TechPreviewNoUpgrade: true},
			path:   "hypershift-operator/zz_generated.crd-manifests/nodepools-Default.yaml",
			crd:    defaultCRD(),
			expect: false,
		},
		{
			name:   "When path is a generated CRD with Default annotation and TechPreviewNoUpgrade is false, it should be included",
			path:   "hypershift-operator/zz_generated.crd-manifests/nodepools-Default.yaml",
			crd:    defaultCRD(),
			expect: true,
		},
		{
			name:   "When path is under hypershift-operator/, it should be included",
			path:   "hypershift-operator/something.yaml",
			crd:    defaultCRD(),
			expect: true,
		},
		{
			name:   "When path is under cluster-api/ and CRD is not an existing IPAM CRD, it should be included",
			path:   "cluster-api/clusters.cluster.x-k8s.io.yaml",
			crd:    &apiextensionsv1.CustomResourceDefinition{ObjectMeta: metav1.ObjectMeta{Name: "clusters.cluster.x-k8s.io"}},
			expect: true,
		},
		{
			name:   "When path is under cluster-api/ and CRD is an existing IPAM CRD, it should be excluded",
			path:   "cluster-api/ipaddressclaims.ipam.cluster.x-k8s.io.yaml",
			crd:    &apiextensionsv1.CustomResourceDefinition{ObjectMeta: metav1.ObjectMeta{Name: "ipaddressclaims.ipam.cluster.x-k8s.io"}},
			ipam:   set.New("ipaddressclaims.ipam.cluster.x-k8s.io"),
			expect: false,
		},
		{
			name:   "When path contains awsendpointservices and AWS platform is installed, it should be included",
			opts:   Options{PlatformsToInstall: []string{"aws"}},
			path:   "hypershift-operator/zz_generated.crd-manifests/awsendpointservices-Default.yaml",
			crd:    defaultCRD(),
			expect: true,
		},
		{
			name:   "When path contains awsendpointservices and only Azure platform is installed, it should be excluded",
			opts:   Options{PlatformsToInstall: []string{"azure"}},
			path:   "hypershift-operator/zz_generated.crd-manifests/awsendpointservices-Default.yaml",
			crd:    defaultCRD(),
			expect: false,
		},
		{
			name:   "When PlatformsToInstall includes aws, AWS provider CRDs should be included",
			opts:   Options{PlatformsToInstall: []string{"aws"}},
			path:   "cluster-api-provider-aws/infrastructure.cluster.x-k8s.io_awsclusters.yaml",
			crd:    defaultCRD(),
			expect: true,
		},
		{
			name:   "When PlatformsToInstall includes only azure, AWS provider CRDs should be excluded",
			opts:   Options{PlatformsToInstall: []string{"azure"}},
			path:   "cluster-api-provider-aws/infrastructure.cluster.x-k8s.io_awsclusters.yaml",
			crd:    defaultCRD(),
			expect: false,
		},
		{
			name:   "When PlatformsToInstall is empty, all platform CRDs should be included",
			path:   "cluster-api-provider-aws/infrastructure.cluster.x-k8s.io_awsclusters.yaml",
			crd:    defaultCRD(),
			expect: true,
		},
		{
			name:   "When path contains auditlogpersistence and EnableAuditLogPersistence is true, it should be included",
			opts:   Options{EnableAuditLogPersistence: true},
			path:   "auditlogpersistence/something.yaml",
			crd:    defaultCRD(),
			expect: true,
		},
		{
			name:   "When path contains auditlogpersistence and EnableAuditLogPersistence is false, it should be excluded",
			path:   "auditlogpersistence/something.yaml",
			crd:    defaultCRD(),
			expect: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			ipam := tc.ipam
			if ipam == nil {
				ipam = set.New[string]()
			}
			filter := crdIncludeFilter(tc.opts, ipam)
			g.Expect(filter(tc.path, tc.crd)).To(Equal(tc.expect))
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
		{
			name: "When PlatformOptions is set to Azure only Azure CAPI CRDs should be present",
			inputOptions: Options{
				PlatformsToInstall: []string{"azure"},
			},
		},
		{
			name: "When PlatformOptions is set to AWS only AWS CAPI CRDs should be present",
			inputOptions: Options{
				PlatformsToInstall: []string{"aws"},
			},
		},
		{
			name: "When PlatformOptions is set to AWS,Azure only AWS & Azure CAPI CRDs should be present",
			inputOptions: Options{
				PlatformsToInstall: []string{"aws", "azure"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			crds, err := setupCRDs(t.Context(), nil, tc.inputOptions, &corev1.Namespace{}, &corev1.Service{})
			g.Expect(err).ToNot(HaveOccurred())
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

			// Validate the feature set specific CRDs are applied.
			g.Expect(nodePoolCRDS).To(HaveLen(1))
			if tc.inputOptions.TechPreviewNoUpgrade {
				g.Expect(nodePoolCRDS[0].GetAnnotations()["release.openshift.io/feature-set"]).To(Equal("TechPreviewNoUpgrade"))
				return
			}

			// Compute wanted and unwanted platforms based on the test input.
			wantedPlatforms := ValidPlatforms
			unwantedPlatforms := set.New[string]()
			if tc.inputOptions.PlatformsToInstall != nil {
				wantedPlatforms = set.New[string](tc.inputOptions.PlatformsToInstall...)
				unwantedPlatforms = ValidPlatforms.Difference(wantedPlatforms)
			}

			// Validate that no unwanted platform CRDs are present.
			for _, crd := range crds {
				crdName := crd.GetName()

				for unwantedPlatform := range unwantedPlatforms {
					g.Expect(strings.ToLower(crdName)).NotTo(ContainSubstring(strings.ToLower(unwantedPlatform)), "Found unwanted platform CRD")
				}

				if strings.Contains(crdName, "awsendpointservices.hypershift.openshift.io") {
					g.Expect(unwantedPlatforms.Has("AWS")).To(BeFalse())
				}
			}

			// Validate that all wanted platform CRDs are present.
			for platform := range wantedPlatforms {
				wantedCAPICRDsPerPlatform, err := fs.ReadDir(crdassets.CRDS, "cluster-api-provider-"+strings.ToLower(platform))
				if err == nil {
					var yamlFiles []fs.DirEntry
					for _, file := range wantedCAPICRDsPerPlatform {
						if filepath.Ext(file.Name()) == ".yaml" {
							yamlFiles = append(yamlFiles, file)
						}
					}
					wantedCAPICRDsPerPlatform = yamlFiles
				}
				g.Expect(err).ToNot(HaveOccurred())

				gotCRDsPerPlatform := make([]string, 0)
				if platform == "ibmcloud" {
					platform = "ibm"
				}
				for _, crd := range crds {
					if strings.Contains(strings.ToLower(crd.GetName()), strings.ToLower(platform)) {
						gotCRDsPerPlatform = append(gotCRDsPerPlatform, crd.GetName())
					}
				}

				g.Expect(len(wantedCAPICRDsPerPlatform)).To(BeNumerically("<=", len(gotCRDsPerPlatform)), "Missing CRDs for platform %s", platform)
			}

			if wantedPlatforms.Has("AWS") {
				g.Expect(awsEndpointServicesCRD).ToNot(BeNil())
			}

			g.Expect(nodePoolCRDS[0].GetAnnotations()["release.openshift.io/feature-set"]).To(Equal("Default"))
		})
	}
}

func TestRenderHyperShiftOperator_RenderSensitive(t *testing.T) {
	g := NewGomegaWithT(t)
	pullSecretFile := filepath.Join(t.TempDir(), "pull-secret.json")
	g.Expect(os.WriteFile(pullSecretFile, []byte(`{"auths":{}}`), 0o600)).To(Succeed())

	tests := []struct {
		name            string
		renderSensitive bool
		expectSecrets   bool
	}{
		{
			name:            "When render-sensitive is false it should exclude secrets from output",
			renderSensitive: false,
			expectSecrets:   false,
		},
		{
			name:            "When render-sensitive is true it should include secrets in output",
			renderSensitive: true,
			expectSecrets:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			var buf bytes.Buffer
			opts := &Options{
				PrivatePlatform:         string(hyperv1.NonePlatform),
				EnableDefaultingWebhook: true,
				EnableValidatingWebhook: true,
				EnableConversionWebhook: true,
				PullSecretFile:          pullSecretFile,
				RenderSensitive:         tc.renderSensitive,
				Format:                  RenderFormatYaml,
				OutputTypes:             string(OutputAll),
			}

			err := RenderHyperShiftOperator(t.Context(), &buf, opts)
			g.Expect(err).ToNot(HaveOccurred())

			var secretNames []string
			decodedCount := 0
			for doc := range strings.SplitSeq(buf.String(), "\n---\n") {
				if strings.TrimSpace(doc) == "" {
					continue
				}
				obj, _, err := hyperapi.YamlSerializer.Decode([]byte(doc), nil, nil)
				g.Expect(err).ToNot(HaveOccurred(), "failed to decode rendered manifest")
				decodedCount++
				if secret, ok := obj.(*corev1.Secret); ok {
					secretNames = append(secretNames, secret.Name)
				}
			}
			g.Expect(decodedCount).To(BeNumerically(">", 0), "expected rendered manifests to be decodable")
			if tc.expectSecrets {
				g.Expect(secretNames).ToNot(BeEmpty(), "expected secrets in rendered output")
			} else {
				g.Expect(secretNames).To(BeEmpty(), "expected no secrets in rendered output")
			}
		})
	}

	t.Run("When webhooks are enabled it should not render webhook cert secrets", func(t *testing.T) {
		g := NewGomegaWithT(t)

		var buf bytes.Buffer
		opts := &Options{
			PrivatePlatform:         string(hyperv1.NonePlatform),
			PullSecretFile:          pullSecretFile,
			EnableDefaultingWebhook: true,
			EnableValidatingWebhook: true,
			EnableConversionWebhook: true,
			RenderSensitive:         true,
			Format:                  RenderFormatYaml,
			OutputTypes:             string(OutputAll),
		}
		err := RenderHyperShiftOperator(t.Context(), &buf, opts)
		g.Expect(err).ToNot(HaveOccurred())

		var nonWebhookSecretCount int
		for doc := range strings.SplitSeq(buf.String(), "\n---\n") {
			if strings.TrimSpace(doc) == "" {
				continue
			}
			obj, _, err := hyperapi.YamlSerializer.Decode([]byte(doc), nil, nil)
			g.Expect(err).ToNot(HaveOccurred(), "failed to decode rendered manifest")
			if secret, ok := obj.(*corev1.Secret); ok {
				g.Expect(secret.Name).ToNot(Equal("webhook-serving-ca"), "webhook CA secret should not be rendered")
				g.Expect(secret.Name).ToNot(Equal("manager-serving-cert"), "webhook serving cert should not be rendered")
				nonWebhookSecretCount++
			}
		}
		g.Expect(nonWebhookSecretCount).To(BeNumerically(">", 0), "expected at least one non-webhook secret to be rendered")
	})
}

func TestRenderOutputsScope(t *testing.T) {
	baseOpts := Options{
		PrivatePlatform: string(hyperv1.NonePlatform),
		Format:          RenderFormatYaml,
		RenderSensitive: true,
	}

	tests := []struct {
		name            string
		outputs         string
		expectCRDs      bool
		expectResources bool
		expectError     bool
	}{
		{"when outputs is all it should include CRDs and resources", string(OutputAll), true, true, false},
		{"when outputs is crds it should include only CRDs", string(OutputCRDs), true, false, false},
		{"when outputs is resources it should include only resources", string(OutputResources), false, true, false},
		{"when outputs is invalid it should error", "bogus", false, false, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewGomegaWithT(t)

			opts := baseOpts
			opts.OutputTypes = tc.outputs
			var buf bytes.Buffer
			err := RenderHyperShiftOperator(t.Context(), &buf, &opts)
			if tc.expectError {
				g.Expect(err).To(HaveOccurred(), "expected error for --outputs=%s", tc.outputs)
				return
			}
			g.Expect(err).NotTo(HaveOccurred(), "RenderHyperShiftOperator failed for --outputs=%s", tc.outputs)

			var crdCount, resourceCount int
			for doc := range strings.SplitSeq(buf.String(), "\n---\n") {
				if strings.TrimSpace(doc) == "" {
					continue
				}
				obj, _, err := hyperapi.YamlSerializer.Decode([]byte(doc), nil, nil)
				g.Expect(err).NotTo(HaveOccurred(), "failed to decode rendered manifest")
				if _, isCRD := obj.(*apiextensionsv1.CustomResourceDefinition); isCRD {
					crdCount++
				} else {
					resourceCount++
				}
			}

			if tc.expectCRDs {
				g.Expect(crdCount).To(BeNumerically(">", 0), "expected CRDs in output for --outputs=%s", tc.outputs)
			} else {
				g.Expect(crdCount).To(Equal(0), "expected no CRDs in output for --outputs=%s, got %d", tc.outputs, crdCount)
			}
			if tc.expectResources {
				g.Expect(resourceCount).To(BeNumerically(">", 0), "expected resources in output for --outputs=%s", tc.outputs)
			} else {
				g.Expect(resourceCount).To(Equal(0), "expected no resources in output for --outputs=%s, got %d", tc.outputs, resourceCount)
			}
		})
	}
}

func TestOutputsHelpers(t *testing.T) {
	tests := []struct {
		name              string
		output            Outputs
		isValid           bool
		includesCRDs      bool
		includesResources bool
	}{
		{"when output is all it should be valid and include both", OutputAll, true, true, true},
		{"when output is crds it should be valid and include only CRDs", OutputCRDs, true, true, false},
		{"when output is resources it should be valid and include only resources", OutputResources, true, false, true},
		{"when output is empty it should be invalid", Outputs(""), false, false, false},
		{"when output is bogus it should be invalid", Outputs("bogus"), false, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewGomegaWithT(t)
			g.Expect(tc.output.IsValid()).To(Equal(tc.isValid))
			g.Expect(tc.output.IncludesCRDs()).To(Equal(tc.includesCRDs))
			g.Expect(tc.output.IncludesResources()).To(Equal(tc.includesResources))
		})
	}
}

func TestHyperShiftOperatorManifests_SharedIngress(t *testing.T) {
	tests := []struct {
		name                       string
		managedService             string
		expectSharedIngressObjects bool
	}{
		{
			name:                       "When ManagedService is ARO-HCP it should include shared ingress resources",
			managedService:             hyperv1.AroHCP,
			expectSharedIngressObjects: true,
		},
		{
			name:                       "When ManagedService is empty it should not include shared ingress resources",
			managedService:             "",
			expectSharedIngressObjects: false,
		},
		{
			name:                       "When ManagedService is ROSA it should not include shared ingress resources",
			managedService:             "ROSA",
			expectSharedIngressObjects: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			_, objects, err := hyperShiftOperatorManifests(t.Context(), nil, Options{
				PrivatePlatform: string(hyperv1.NonePlatform),
				ManagedService:  tc.managedService,
			})
			g.Expect(err).ToNot(HaveOccurred())

			var hasSharedIngressNamespace bool
			var namespaceLabels map[string]string
			var hasSharedIngressClusterRole bool
			var hasSharedIngressClusterRoleBinding bool
			var clusterRoleBinding *rbacv1.ClusterRoleBinding
			for _, obj := range objects {
				switch {
				case obj.GetName() == sharedingress.RouterNamespace && obj.GetObjectKind().GroupVersionKind().Kind == "Namespace":
					hasSharedIngressNamespace = true
					namespaceLabels = obj.GetLabels()
				case obj.GetName() == sharedingress.ConfigGeneratorName && obj.GetObjectKind().GroupVersionKind().Kind == "ClusterRole":
					hasSharedIngressClusterRole = true
				case obj.GetName() == sharedingress.ConfigGeneratorName && obj.GetObjectKind().GroupVersionKind().Kind == "ClusterRoleBinding":
					hasSharedIngressClusterRoleBinding = true
					clusterRoleBinding = obj.(*rbacv1.ClusterRoleBinding)
				}
			}

			if tc.expectSharedIngressObjects {
				g.Expect(hasSharedIngressNamespace).To(BeTrue(), "expected shared ingress namespace to be present")
				g.Expect(namespaceLabels).To(HaveKeyWithValue("hypershift.openshift.io/component", "shared-ingress"), "expected shared ingress namespace to have component label")
				g.Expect(hasSharedIngressClusterRole).To(BeTrue(), "expected shared ingress ClusterRole to be present")
				g.Expect(hasSharedIngressClusterRoleBinding).To(BeTrue(), "expected shared ingress ClusterRoleBinding to be present")
				g.Expect(clusterRoleBinding.RoleRef).To(Equal(rbacv1.RoleRef{
					APIGroup: "rbac.authorization.k8s.io",
					Kind:     "ClusterRole",
					Name:     sharedingress.ConfigGeneratorName,
				}), "expected ClusterRoleBinding to reference the correct ClusterRole")
				g.Expect(clusterRoleBinding.Subjects).To(ConsistOf(rbacv1.Subject{
					Kind:      "ServiceAccount",
					Name:      "router",
					Namespace: sharedingress.RouterNamespace,
				}), "expected ClusterRoleBinding to have the router ServiceAccount as subject")
			} else {
				g.Expect(hasSharedIngressNamespace).To(BeFalse(), "expected shared ingress namespace to not be present")
				g.Expect(hasSharedIngressClusterRole).To(BeFalse(), "expected shared ingress ClusterRole to not be present")
				g.Expect(hasSharedIngressClusterRoleBinding).To(BeFalse(), "expected shared ingress ClusterRoleBinding to not be present")
			}
		})
	}
}

// fakeDiscovery implements discovery.ServerResourcesInterface for testing.
type fakeDiscovery struct {
	resources []*metav1.APIResourceList
}

func (f *fakeDiscovery) ServerResourcesForGroupVersion(groupVersion string) (*metav1.APIResourceList, error) {
	for _, rl := range f.resources {
		if rl.GroupVersion == groupVersion {
			return rl, nil
		}
	}
	return nil, &apierrors.StatusError{
		ErrStatus: metav1.Status{
			Status: metav1.StatusFailure,
			Reason: metav1.StatusReasonNotFound,
		},
	}
}

func TestIsClusterAPIRegistered(t *testing.T) {
	tests := []struct {
		name           string
		resources      []*metav1.APIResourceList
		expectedResult bool
	}{
		{
			name: "When ClusterAPI API is registered it should detect CCAPIO presence",
			resources: []*metav1.APIResourceList{
				{
					GroupVersion: "operator.openshift.io/v1alpha1",
					APIResources: []metav1.APIResource{
						{Name: "imagecontentsourcepolicies", Kind: "ImageContentSourcePolicy"},
						{Name: "clusterapis", Kind: "ClusterAPI"},
					},
				},
			},
			expectedResult: true,
		},
		{
			name:           "When ClusterAPI API is not registered it should skip CCAPIO coordination",
			resources:      nil,
			expectedResult: false,
		},
		{
			name: "When operator.openshift.io/v1alpha1 exists but ClusterAPI kind is not present it should return false",
			resources: []*metav1.APIResourceList{
				{
					GroupVersion: "operator.openshift.io/v1alpha1",
					APIResources: []metav1.APIResource{
						{Name: "imagecontentsourcepolicies", Kind: "ImageContentSourcePolicy"},
					},
				},
			},
			expectedResult: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			disco := &fakeDiscovery{resources: tc.resources}

			registered, err := isClusterAPIRegistered(disco)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(registered).To(Equal(tc.expectedResult))
		})
	}
}

func TestEnsureUnmanagedCRDs(t *testing.T) {
	capiCRDs := []crclient.Object{
		&apiextensionsv1.CustomResourceDefinition{ObjectMeta: metav1.ObjectMeta{Name: "clusters.cluster.x-k8s.io"}},
		&apiextensionsv1.CustomResourceDefinition{ObjectMeta: metav1.ObjectMeta{Name: "machines.cluster.x-k8s.io"}},
		&apiextensionsv1.CustomResourceDefinition{ObjectMeta: metav1.ObjectMeta{Name: "nodepools.hypershift.openshift.io"}},
	}

	// ssaInterceptor converts SSA Patch calls into Create/Update for the fake client,
	// which does not support ApplyPatchType natively.
	ssaInterceptor := interceptor.Funcs{
		Patch: func(ctx context.Context, c crclient.WithWatch, obj crclient.Object, patch crclient.Patch, opts ...crclient.PatchOption) error {
			if patch.Type() != types.ApplyPatchType {
				return c.Patch(ctx, obj, patch, opts...)
			}
			existing := obj.DeepCopyObject().(crclient.Object)
			err := c.Get(ctx, crclient.ObjectKeyFromObject(obj), existing)
			if apierrors.IsNotFound(err) {
				return c.Create(ctx, obj)
			}
			if err != nil {
				return err
			}
			obj.SetResourceVersion(existing.GetResourceVersion())
			return c.Update(ctx, obj)
		},
	}

	tests := []struct {
		name             string
		existingConfig   *operatorv1alpha1.ClusterAPI
		crds             []crclient.Object
		expectedCRDNames []string
		expectNoChange   bool
	}{
		{
			name:           "When ClusterAPI config does not exist it should create it with unmanaged CRDs",
			existingConfig: nil,
			crds:           capiCRDs,
			expectedCRDNames: []string{
				"clusters.cluster.x-k8s.io",
				"machines.cluster.x-k8s.io",
			},
		},
		{
			name: "When ClusterAPI config exists it should apply unmanaged CRDs",
			existingConfig: &operatorv1alpha1.ClusterAPI{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: &operatorv1alpha1.ClusterAPISpec{
					UnmanagedCustomResourceDefinitions: []string{
						"clusters.cluster.x-k8s.io",
						"machinesets.cluster.x-k8s.io",
					},
				},
			},
			crds: capiCRDs,
			// SSA with listType=set would merge entries from different field owners on a real
			// API server. Since the fake client cannot simulate set-based merge semantics,
			// this test verifies that HyperShift's apply succeeds and contains its own entries.
			expectedCRDNames: []string{
				"clusters.cluster.x-k8s.io",
				"machines.cluster.x-k8s.io",
			},
		},
		{
			name: "When all CRDs already listed it should apply idempotently",
			existingConfig: &operatorv1alpha1.ClusterAPI{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: &operatorv1alpha1.ClusterAPISpec{
					UnmanagedCustomResourceDefinitions: []string{
						"clusters.cluster.x-k8s.io",
						"machines.cluster.x-k8s.io",
					},
				},
			},
			crds: capiCRDs,
			expectedCRDNames: []string{
				"clusters.cluster.x-k8s.io",
				"machines.cluster.x-k8s.io",
			},
		},
		{
			name:           "When populating unmanaged CRDs it should only include CAPI CRDs",
			existingConfig: nil,
			crds: []crclient.Object{
				&apiextensionsv1.CustomResourceDefinition{ObjectMeta: metav1.ObjectMeta{Name: "nodepools.hypershift.openshift.io"}},
				&apiextensionsv1.CustomResourceDefinition{ObjectMeta: metav1.ObjectMeta{Name: "hostedclusters.hypershift.openshift.io"}},
			},
			expectNoChange: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			builder := fake.NewClientBuilder().
				WithScheme(hyperapi.Scheme).
				WithInterceptorFuncs(ssaInterceptor)
			if tc.existingConfig != nil {
				builder = builder.WithObjects(tc.existingConfig)
			}
			client := builder.Build()

			_, err := ensureUnmanagedCRDs(t.Context(), io.Discard, client, tc.crds)
			g.Expect(err).ToNot(HaveOccurred())

			if tc.expectNoChange {
				config := &operatorv1alpha1.ClusterAPI{}
				err := client.Get(t.Context(), crclient.ObjectKey{Name: "cluster"}, config)
				if tc.existingConfig == nil {
					g.Expect(err).To(HaveOccurred(), "expected no ClusterAPI config to be created")
					return
				}
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(config.Spec).ToNot(BeNil())
				g.Expect(config.Spec.UnmanagedCustomResourceDefinitions).
					To(ConsistOf(tc.existingConfig.Spec.UnmanagedCustomResourceDefinitions))
				return
			}

			config := &operatorv1alpha1.ClusterAPI{}
			err = client.Get(t.Context(), crclient.ObjectKey{Name: "cluster"}, config)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(config.Spec).ToNot(BeNil())
			g.Expect(config.Spec.UnmanagedCustomResourceDefinitions).To(ConsistOf(tc.expectedCRDNames))
		})
	}
}

func TestWaitForCAPIOperatorSync(t *testing.T) {
	tests := []struct {
		name            string
		config          *operatorv1alpha1.ClusterAPI
		patchGeneration int64
		expectSuccess   bool
	}{
		{
			name: "When revision controller has observed the patch generation and installer has applied it should succeed",
			config: &operatorv1alpha1.ClusterAPI{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "cluster",
					Generation: 2,
				},
				Status: operatorv1alpha1.ClusterAPIStatus{
					ObservedRevisionGeneration: 2,
					DesiredRevision:            "rev-2",
					CurrentRevision:            "rev-2",
					Revisions: []operatorv1alpha1.ClusterAPIInstallerRevision{
						{Name: "rev-2", Revision: 2, ContentID: "content-2"},
					},
				},
			},
			patchGeneration: 2,
			expectSuccess:   true,
		},
		{
			name: "When observedRevisionGeneration is ahead of patch generation it should succeed",
			config: &operatorv1alpha1.ClusterAPI{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "cluster",
					Generation: 3,
				},
				Status: operatorv1alpha1.ClusterAPIStatus{
					ObservedRevisionGeneration: 3,
					DesiredRevision:            "rev-3",
					CurrentRevision:            "rev-3",
					Revisions: []operatorv1alpha1.ClusterAPIInstallerRevision{
						{Name: "rev-3", Revision: 3, ContentID: "content-3"},
					},
				},
			},
			patchGeneration: 2,
			expectSuccess:   true,
		},
		{
			name: "When revision controller has not observed the patch generation it should time out",
			config: &operatorv1alpha1.ClusterAPI{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "cluster",
					Generation: 3,
				},
				Status: operatorv1alpha1.ClusterAPIStatus{
					ObservedRevisionGeneration: 2,
					DesiredRevision:            "rev-2",
					CurrentRevision:            "rev-2",
					Revisions: []operatorv1alpha1.ClusterAPIInstallerRevision{
						{Name: "rev-2", Revision: 2, ContentID: "content-2"},
					},
				},
			},
			patchGeneration: 3,
			expectSuccess:   false,
		},
		{
			name: "When reading a stale object from before the patch it should time out",
			config: &operatorv1alpha1.ClusterAPI{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "cluster",
					Generation: 1,
				},
				Status: operatorv1alpha1.ClusterAPIStatus{
					ObservedRevisionGeneration: 1,
					DesiredRevision:            "rev-1",
					CurrentRevision:            "rev-1",
					Revisions: []operatorv1alpha1.ClusterAPIInstallerRevision{
						{Name: "rev-1", Revision: 1, ContentID: "content-1"},
					},
				},
			},
			patchGeneration: 2,
			expectSuccess:   false,
		},
		{
			name: "When currentRevision does not match desiredRevision it should time out",
			config: &operatorv1alpha1.ClusterAPI{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "cluster",
					Generation: 2,
				},
				Status: operatorv1alpha1.ClusterAPIStatus{
					ObservedRevisionGeneration: 2,
					DesiredRevision:            "rev-2",
					CurrentRevision:            "rev-1",
					Revisions: []operatorv1alpha1.ClusterAPIInstallerRevision{
						{Name: "rev-1", Revision: 1, ContentID: "content-1"},
						{Name: "rev-2", Revision: 2, ContentID: "content-2"},
					},
				},
			},
			patchGeneration: 2,
			expectSuccess:   false,
		},
		{
			name: "When currentRevision is empty it should time out",
			config: &operatorv1alpha1.ClusterAPI{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "cluster",
					Generation: 1,
				},
				Status: operatorv1alpha1.ClusterAPIStatus{
					ObservedRevisionGeneration: 1,
					DesiredRevision:            "rev-1",
					Revisions: []operatorv1alpha1.ClusterAPIInstallerRevision{
						{Name: "rev-1", Revision: 1, ContentID: "content-1"},
					},
				},
			},
			patchGeneration: 1,
			expectSuccess:   false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			client := fake.NewClientBuilder().
				WithScheme(hyperapi.Scheme).
				WithObjects(tc.config).
				WithStatusSubresource(&operatorv1alpha1.ClusterAPI{}).
				Build()

			ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
			defer cancel()

			err := waitForCAPIOperatorSync(ctx, io.Discard, client, tc.patchGeneration)
			if tc.expectSuccess {
				g.Expect(err).ToNot(HaveOccurred())
			} else {
				g.Expect(err).To(HaveOccurred())
			}
		})
	}
}

func TestApplyDefaults(t *testing.T) {
	tests := []struct {
		name             string
		opts             Options
		expectedReplicas int32
	}{
		{
			name:             "When Development mode is enabled, it should set replicas to 0",
			opts:             Options{Development: true},
			expectedReplicas: 0,
		},
		{
			name:             "When defaulting webhook is enabled, it should set replicas to 2",
			opts:             Options{EnableDefaultingWebhook: true},
			expectedReplicas: 2,
		},
		{
			name:             "When conversion webhook is enabled, it should set replicas to 2",
			opts:             Options{EnableConversionWebhook: true},
			expectedReplicas: 2,
		},
		{
			name:             "When validating webhook is enabled, it should set replicas to 2",
			opts:             Options{EnableValidatingWebhook: true},
			expectedReplicas: 2,
		},
		{
			name:             "When CAPI conversion webhook is enabled (default), it should set replicas to 2",
			opts:             Options{},
			expectedReplicas: 2,
		},
		{
			name:             "When all webhooks are disabled, it should default replicas to 1",
			opts:             Options{DisableCAPIConversionWebhook: true},
			expectedReplicas: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			tc.opts.ApplyDefaults()
			g.Expect(tc.opts.HyperShiftOperatorReplicas).To(Equal(tc.expectedReplicas))
			g.Expect(tc.opts.RenderNamespace).To(BeTrue())
		})
	}
}

func TestFilterManifestsByScope(t *testing.T) {
	t.Parallel()
	fakeCRD := &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: "test-crd"},
	}
	fakeDeployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "test-deployment"},
	}
	crds := []crclient.Object{fakeCRD}
	objects := []crclient.Object{fakeDeployment}

	tests := []struct {
		name            string
		scope           Outputs
		expectCRDs      int
		expectResources int
	}{
		{"when scope is all it should return both CRDs and resources", OutputAll, 1, 1},
		{"when scope is crds it should return only CRDs", OutputCRDs, 1, 0},
		{"when scope is resources it should return only resources", OutputResources, 0, 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewGomegaWithT(t)
			gotCRDs, gotObjects := filterManifestsByScope(crds, objects, tc.scope)
			g.Expect(gotCRDs).To(HaveLen(tc.expectCRDs))
			g.Expect(gotObjects).To(HaveLen(tc.expectResources))
		})
	}
}

func TestNewInstallOptionsWithDefaults_InstallScope(t *testing.T) {
	g := NewGomegaWithT(t)
	opts := NewInstallOptionsWithDefaults()
	g.Expect(opts.InstallScope).To(Equal(string(OutputAll)), "InstallScope should default to 'all'")
}

func TestIsAWSPlatformEnabled(t *testing.T) {
	tests := []struct {
		name               string
		platformsToInstall []string
		expected           bool
	}{
		{
			name:               "When no platforms are specified, it should return true (all platforms enabled)",
			platformsToInstall: nil,
			expected:           true,
		},
		{
			name:               "When empty platforms list is specified, it should return true",
			platformsToInstall: []string{},
			expected:           true,
		},
		{
			name:               "When AWS is in the list, it should return true",
			platformsToInstall: []string{"AWS", "Azure"},
			expected:           true,
		},
		{
			name:               "When aws (lowercase) is in the list, it should return true",
			platformsToInstall: []string{"aws"},
			expected:           true,
		},
		{
			name:               "When AWS is not in the list, it should return false",
			platformsToInstall: []string{"Azure", "GCP"},
			expected:           false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			result := isAWSPlatformEnabled(tc.platformsToInstall)
			g.Expect(result).To(Equal(tc.expected))
		})
	}
}

func TestIsAzurePlatformEnabled(t *testing.T) {
	tests := []struct {
		name               string
		platformsToInstall []string
		expected           bool
	}{
		{
			name:               "When no platforms are specified, it should return true (all platforms enabled)",
			platformsToInstall: nil,
			expected:           true,
		},
		{
			name:               "When Azure is in the list, it should return true",
			platformsToInstall: []string{"Azure", "AWS"},
			expected:           true,
		},
		{
			name:               "When azure (lowercase) is in the list, it should return true",
			platformsToInstall: []string{"azure"},
			expected:           true,
		},
		{
			name:               "When Azure is not in the list, it should return false",
			platformsToInstall: []string{"AWS", "GCP"},
			expected:           false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			result := isAzurePlatformEnabled(tc.platformsToInstall)
			g.Expect(result).To(Equal(tc.expected))
		})
	}
}

func TestValidatePlatformConfig(t *testing.T) {
	tests := []struct {
		name        string
		opts        Options
		expectError bool
	}{
		{
			name:        "When private platform is None, it should pass",
			opts:        Options{PrivatePlatform: string(hyperv1.NonePlatform)},
			expectError: false,
		},
		{
			name:        "When private platform is an unsupported value, it should fail",
			opts:        Options{PrivatePlatform: "Unsupported"},
			expectError: true,
		},
		{
			name: "When private platform is AWS with creds and region, it should pass",
			opts: Options{
				PrivatePlatform:  string(hyperv1.AWSPlatform),
				AWSPrivateCreds:  "/path/to/creds",
				AWSPrivateRegion: "us-east-1",
			},
			expectError: false,
		},
		{
			name: "When private platform is AWS without creds or region, it should fail",
			opts: Options{
				PrivatePlatform: string(hyperv1.AWSPlatform),
			},
			expectError: true,
		},
		{
			name: "When private platform is GCP with only project set, it should fail",
			opts: Options{
				PrivatePlatform: string(hyperv1.GCPPlatform),
				GCPProject:      "my-project",
			},
			expectError: true,
		},
		{
			name: "When private platform is GCP with both project and region, it should pass",
			opts: Options{
				PrivatePlatform: string(hyperv1.GCPPlatform),
				GCPProject:      "my-project",
				GCPRegion:       "us-central1",
			},
			expectError: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			errs := tc.opts.validatePlatformConfig()
			if tc.expectError {
				g.Expect(errs).NotTo(BeEmpty())
			} else {
				g.Expect(errs).To(BeEmpty())
			}
		})
	}
}

func TestValidateOIDCConfig(t *testing.T) {
	tests := []struct {
		name        string
		opts        Options
		expectError bool
	}{
		{
			name:        "When no OIDC options are set, it should pass",
			opts:        Options{},
			expectError: false,
		},
		{
			name: "When both OIDC secret and credentials are set, it should fail",
			opts: Options{
				OIDCStorageProviderS3CredentialsSecret: "my-secret",
				OIDCStorageProviderS3Credentials:       "my-creds",
			},
			expectError: true,
		},
		{
			name: "When OIDC credentials are set without bucket name, it should fail",
			opts: Options{
				OIDCStorageProviderS3Credentials: "my-creds",
			},
			expectError: true,
		},
		{
			name: "When OIDC bucket name contains dots, it should fail",
			opts: Options{
				OIDCStorageProviderS3BucketName: "my.bucket.name",
			},
			expectError: true,
		},
		{
			name: "When all OIDC parameters are provided correctly, it should pass",
			opts: Options{
				OIDCStorageProviderS3Credentials:          "my-creds",
				OIDCStorageProviderS3BucketName:           "mybucket",
				OIDCStorageProviderS3Region:               "us-east-1",
				OIDCStorageProviderS3CredentialsSecretKey: "mykey",
			},
			expectError: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			errs := tc.opts.validateOIDCConfig()
			if tc.expectError {
				g.Expect(errs).NotTo(BeEmpty())
			} else {
				g.Expect(errs).To(BeEmpty())
			}
		})
	}
}

func TestValidateExternalDNSConfig(t *testing.T) {
	tests := []struct {
		name        string
		opts        Options
		expectError bool
	}{
		{
			name:        "When no external DNS provider is set, it should pass",
			opts:        Options{},
			expectError: false,
		},
		{
			name: "When external DNS provider is set without credentials or domain filter, it should fail",
			opts: Options{
				ExternalDNSProvider: "aws",
			},
			expectError: true,
		},
		{
			name: "When external DNS provider is set with credentials and domain filter, it should pass",
			opts: Options{
				ExternalDNSProvider:     "aws",
				ExternalDNSCredentials:  "/path/to/creds",
				ExternalDNSDomainFilter: "example.com",
			},
			expectError: false,
		},
		{
			name: "When external DNS interval is an invalid duration, it should fail",
			opts: Options{
				ExternalDNSProvider:     "aws",
				ExternalDNSCredentials:  "/path/to/creds",
				ExternalDNSDomainFilter: "example.com",
				ExternalDNSInterval:     "not-a-duration",
			},
			expectError: true,
		},
		{
			name: "When AWS zones cache duration is set with non-AWS provider, it should fail",
			opts: Options{
				ExternalDNSProvider:              "azure",
				ExternalDNSCredentials:           "/path/to/creds",
				ExternalDNSDomainFilter:          "example.com",
				ExternalDNSAWSZonesCacheDuration: "1h",
			},
			expectError: true,
		},
		{
			name: "When google provider is set without credentials, it should pass (Workload Identity)",
			opts: Options{
				ExternalDNSProvider:     "google",
				ExternalDNSDomainFilter: "example.com",
			},
			expectError: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			errs := tc.opts.validateExternalDNSConfig()
			if tc.expectError {
				g.Expect(errs).NotTo(BeEmpty())
			} else {
				g.Expect(errs).To(BeEmpty())
			}
		})
	}
}

func TestValidateMonitoringConfig(t *testing.T) {
	tests := []struct {
		name        string
		opts        Options
		expectError bool
	}{
		{
			name:        "When no monitoring options are set, it should pass",
			opts:        Options{},
			expectError: false,
		},
		{
			name: "When both RHOBS monitoring and CVO management cluster metrics access are set, it should fail",
			opts: Options{
				RHOBSMonitoring:                         true,
				EnableCVOManagementClusterMetricsAccess: true,
			},
			expectError: true,
		},
		{
			name: "When CVO prometheus URL is set without RHOBS or CVO metrics, it should fail",
			opts: Options{
				CVOPrometheusURL: "https://prometheus.example.com",
			},
			expectError: true,
		},
		{
			name: "When CVO prometheus URL is set with RHOBS monitoring, it should pass",
			opts: Options{
				CVOPrometheusURL: "https://prometheus.example.com",
				RHOBSMonitoring:  true,
			},
			expectError: false,
		},
		{
			name: "When both AZ monitoring and CVO management cluster metrics access are set, it should fail",
			opts: Options{
				AZMonitoring:                            true,
				EnableCVOManagementClusterMetricsAccess: true,
			},
			expectError: true,
		},
		{
			name: "When both AZ monitoring and RHOBS monitoring are set, it should fail",
			opts: Options{
				AZMonitoring:    true,
				RHOBSMonitoring: true,
			},
			expectError: true,
		},
		{
			name: "When CVO prometheus URL is set with AZ monitoring, it should pass",
			opts: Options{
				CVOPrometheusURL: "https://prometheus.example.com",
				AZMonitoring:     true,
			},
			expectError: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			errs := tc.opts.validateMonitoringConfig()
			if tc.expectError {
				g.Expect(errs).NotTo(BeEmpty())
			} else {
				g.Expect(errs).To(BeEmpty())
			}
		})
	}
}

func TestValidateMiscConfig(t *testing.T) {
	tests := []struct {
		name        string
		opts        Options
		expectError bool
	}{
		{
			name:        "When no misc options are set, it should pass",
			opts:        Options{},
			expectError: false,
		},
		{
			name: "When managed service is an invalid value, it should fail",
			opts: Options{
				ManagedService: "INVALID",
			},
			expectError: true,
		},
		{
			name: "When managed service is ARO-HCP, it should pass",
			opts: Options{
				ManagedService: hyperv1.AroHCP,
			},
			expectError: false,
		},
		{
			name: "When an invalid platform is specified, it should fail",
			opts: Options{
				PlatformsToInstall: []string{"invalid-platform"},
			},
			expectError: true,
		},
		{
			name: "When valid platforms are specified, it should pass",
			opts: Options{
				PlatformsToInstall: []string{"aws", "Azure"},
			},
			expectError: false,
		},
		{
			name: "When invalid image pull policy is specified, it should fail",
			opts: Options{
				ImagePullPolicy: "WheneverYouFeel",
			},
			expectError: true,
		},
		{
			name: "When Always image pull policy is specified, it should pass",
			opts: Options{
				ImagePullPolicy: "Always",
			},
			expectError: false,
		},
		{
			name: "When Never image pull policy is specified, it should pass",
			opts: Options{
				ImagePullPolicy: "Never",
			},
			expectError: false,
		},
		{
			name: "When IfNotPresent image pull policy is specified, it should pass",
			opts: Options{
				ImagePullPolicy: "IfNotPresent",
			},
			expectError: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			errs := tc.opts.validateMiscConfig()
			if tc.expectError {
				g.Expect(errs).NotTo(BeEmpty())
			} else {
				g.Expect(errs).To(BeEmpty())
			}
		})
	}
}

func TestSetupMonitoring(t *testing.T) {
	tests := []struct {
		name             string
		opts             Options
		expectSLOAlerts  bool
		expectDashboards bool
		minResourceCount int
	}{
		{
			name: "When SLOs alerts and monitoring dashboards are disabled, it should return base monitoring resources",
			opts: Options{
				PlatformMonitoring: metrics.PlatformMonitoringAll,
			},
			expectSLOAlerts:  false,
			expectDashboards: false,
			minResourceCount: 4,
		},
		{
			name: "When SLOs alerts are enabled, it should include alerting rule",
			opts: Options{
				SLOsAlerts: true,
			},
			expectSLOAlerts:  true,
			minResourceCount: 5,
		},
		{
			name: "When monitoring dashboards are enabled, it should include dashboard template",
			opts: Options{
				Namespace:            "hypershift",
				MonitoringDashboards: true,
			},
			expectDashboards: true,
			minResourceCount: 5,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "hypershift"}}
			objects := setupMonitoring(tc.opts, ns)

			g.Expect(len(objects)).To(BeNumerically(">=", tc.minResourceCount))

			// Check SLO alerts
			foundAlertingRule := false
			for _, obj := range objects {
				if rule, ok := obj.(*prometheusoperatorv1.PrometheusRule); ok && rule.Namespace == "openshift-monitoring" {
					foundAlertingRule = true
					break
				}
			}
			if tc.expectSLOAlerts {
				g.Expect(foundAlertingRule).To(BeTrue(), "expected SLO alerting rule to be present when SLOsAlerts is enabled")
			} else {
				g.Expect(foundAlertingRule).To(BeFalse(), "expected no SLO alerting rule when SLOsAlerts is disabled")
			}

			// Check dashboards
			foundDashboard := false
			for _, obj := range objects {
				if cm, ok := obj.(*corev1.ConfigMap); ok && cm.Name == "monitoring-dashboard-template" {
					foundDashboard = true
					break
				}
			}
			if tc.expectDashboards {
				g.Expect(foundDashboard).To(BeTrue(), "expected monitoring dashboard to be present when MonitoringDashboards is enabled")
			} else {
				g.Expect(foundDashboard).To(BeFalse(), "expected no monitoring dashboard when MonitoringDashboards is disabled")
			}
		})
	}
}

func TestSetupRBAC(t *testing.T) {
	tests := []struct {
		name                         string
		enableAdminRBAC              bool
		azureManagedIdentityClientID string
		expectSAAnnotation           bool
		minObjectCount               int
	}{
		{
			name:           "When admin RBAC is disabled, it should return base RBAC resources",
			minObjectCount: 6,
		},
		{
			name:            "When admin RBAC is enabled, it should include client and reader RBAC resources",
			enableAdminRBAC: true,
			minObjectCount:  11,
		},
		{
			name:                         "When Azure managed identity is set, it should annotate the service account",
			azureManagedIdentityClientID: "test-client-id",
			expectSAAnnotation:           true,
			minObjectCount:               6,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "hypershift"}}
			opts := Options{
				EnableAdminRBACGeneration:       tc.enableAdminRBAC,
				AzurePLSManagedIdentityClientID: tc.azureManagedIdentityClientID,
			}

			sa, objects := setupRBAC(opts, ns)
			g.Expect(len(objects)).To(BeNumerically(">=", tc.minObjectCount))
			g.Expect(sa).NotTo(BeNil())
			if tc.expectSAAnnotation {
				g.Expect(sa.Annotations).To(HaveKeyWithValue("azure.workload.identity/client-id", tc.azureManagedIdentityClientID))
			}

			// When admin RBAC is enabled, verify specific admin RBAC objects exist
			if tc.enableAdminRBAC {
				foundClientClusterRole := false
				foundReaderClusterRole := false
				for _, obj := range objects {
					if obj.GetObjectKind().GroupVersionKind().Kind == "ClusterRole" {
						if obj.GetName() == "hypershift-client" {
							foundClientClusterRole = true
						}
						if obj.GetName() == "hypershift-readers" {
							foundReaderClusterRole = true
						}
					}
				}
				g.Expect(foundClientClusterRole).To(BeTrue(), "expected hypershift-client ClusterRole to be present when admin RBAC is enabled")
				g.Expect(foundReaderClusterRole).To(BeTrue(), "expected hypershift-readers ClusterRole to be present when admin RBAC is enabled")
			}
		})
	}
}

func TestSetupCA(t *testing.T) {
	t.Run("When no additional trust bundle is provided, it should return only the managed trust bundle", func(t *testing.T) {
		g := NewGomegaWithT(t)
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "hypershift"}}
		userCA, trustedCA, objects, err := setupCA(Options{}, ns)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(userCA).To(BeNil())
		g.Expect(trustedCA).NotTo(BeNil())
		g.Expect(trustedCA.Name).To(Equal("openshift-config-managed-trusted-ca-bundle"))
		g.Expect(objects).To(HaveLen(1))
	})
}

func TestSetupExternalDNS(t *testing.T) {
	tests := []struct {
		name             string
		opts             Options
		minResourceCount int
		expectGCPRules   bool
	}{
		{
			name: "When AWS provider is set with role ARN, it should return base resources with credentials secret",
			opts: Options{
				ExternalDNSProvider:     "aws",
				ExternalDNSDomainFilter: "example.com",
				ExternalDNSRoleARN:      "arn:aws:iam::123456789012:role/external-dns",
			},
			minResourceCount: 6,
		},
		{
			name: "When google provider is set, it should include additional ClusterRole rules",
			opts: Options{
				ExternalDNSProvider:     "google",
				ExternalDNSDomainFilter: "example.com",
			},
			minResourceCount: 5,
			expectGCPRules:   true,
		},
		{
			name: "When credentials secret name is set, it should return base resources without creating a new secret",
			opts: Options{
				ExternalDNSProvider:          "aws",
				ExternalDNSDomainFilter:      "example.com",
				ExternalDNSCredentialsSecret: "my-dns-secret",
			},
			minResourceCount: 5,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "hypershift"}}
			objects, err := setupExternalDNS(context.Background(), tc.opts, ns)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(len(objects)).To(BeNumerically(">=", tc.minResourceCount))

			if tc.expectGCPRules {
				for _, obj := range objects {
					if cr, ok := obj.(*rbacv1.ClusterRole); ok {
						foundDNSEndpoints := false
						for _, rule := range cr.Rules {
							for _, res := range rule.Resources {
								if res == "dnsendpoints" {
									foundDNSEndpoints = true
								}
							}
						}
						g.Expect(foundDNSEndpoints).To(BeTrue(), "expected dnsendpoints rule for google provider")
					}
				}
			}
		})
	}
}

func TestValidateImageConfig(t *testing.T) {
	tests := []struct {
		name        string
		opts        Options
		expectError bool
	}{
		{
			name:        "When default image and no refs file are set, it should pass",
			opts:        Options{HyperShiftImage: HyperShiftImage},
			expectError: false,
		},
		{
			name: "When both custom image and refs file are set, it should fail",
			opts: Options{
				HyperShiftImage: "custom-image:latest",
				ImageRefsFile:   "/path/to/refs",
			},
			expectError: true,
		},
		{
			name: "When cert rotation scale is longer than 24h, it should fail",
			opts: Options{
				HyperShiftImage:   HyperShiftImage,
				CertRotationScale: 48 * time.Hour,
			},
			expectError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			errs := tc.opts.validateImageConfig()
			if tc.expectError {
				g.Expect(errs).NotTo(BeEmpty())
			} else {
				g.Expect(errs).To(BeEmpty())
			}
		})
	}
}

func TestSetupSharedIngress(t *testing.T) {
	t.Run("When setupSharedIngress is called, it should return namespace, ClusterRole, and ClusterRoleBinding", func(t *testing.T) {
		g := NewGomegaWithT(t)
		objects := setupSharedIngress()
		g.Expect(objects).To(HaveLen(3))

		g.Expect(objects[0].GetName()).To(Equal(sharedingress.RouterNamespace))
		g.Expect(objects[0].GetLabels()).To(HaveKeyWithValue("hypershift.openshift.io/component", "shared-ingress"))

		g.Expect(objects[1].GetName()).To(Equal(sharedingress.ConfigGeneratorName))
		g.Expect(objects[2].GetName()).To(Equal(sharedingress.ConfigGeneratorName))

		crb := objects[2].(*rbacv1.ClusterRoleBinding)
		g.Expect(crb.RoleRef.Name).To(Equal(sharedingress.ConfigGeneratorName))
		g.Expect(crb.Subjects).To(HaveLen(1))
		g.Expect(crb.Subjects[0].Name).To(Equal("router"))
		g.Expect(crb.Subjects[0].Namespace).To(Equal(sharedingress.RouterNamespace))
	})
}

func TestSetupAdminRBAC(t *testing.T) {
	t.Run("When setupAdminRBAC is called, it should return client and reader RBAC resources", func(t *testing.T) {
		g := NewGomegaWithT(t)
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "hypershift"}}
		objects := setupAdminRBAC(ns)

		g.Expect(objects).To(HaveLen(5))

		objectNames := make([]string, len(objects))
		for i, obj := range objects {
			objectNames[i] = obj.GetName()
		}
		g.Expect(objectNames).To(ContainElement("hypershift-client"))
		g.Expect(objectNames).To(ContainElement("hypershift-readers"))
	})
}

func TestGetDeploymentCondition(t *testing.T) {
	tests := []struct {
		name             string
		deployConditions []appsv1.DeploymentCondition
		condType         string
		expectFound      bool
	}{
		{
			name: "When the condition exists, it should return it",
			deployConditions: []appsv1.DeploymentCondition{
				{Type: "Progressing", Reason: "NewReplicaSetAvailable"},
				{Type: "Available", Reason: "MinimumReplicasAvailable"},
			},
			condType:    "Available",
			expectFound: true,
		},
		{
			name: "When the condition does not exist, it should return nil",
			deployConditions: []appsv1.DeploymentCondition{
				{Type: "Progressing", Reason: "NewReplicaSetAvailable"},
			},
			condType:    "Available",
			expectFound: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			status := appsv1.DeploymentStatus{}
			for _, c := range tc.deployConditions {
				status.Conditions = append(status.Conditions, appsv1.DeploymentCondition{
					Type:   appsv1.DeploymentConditionType(c.Type),
					Reason: c.Reason,
				})
			}
			cond := GetDeploymentCondition(status, appsv1.DeploymentConditionType(tc.condType))
			if tc.expectFound {
				g.Expect(cond).NotTo(BeNil())
			} else {
				g.Expect(cond).To(BeNil())
			}
		})
	}
}

func TestNewInstallOptionsWithDefaults(t *testing.T) {
	t.Run("When NewInstallOptionsWithDefaults is called, it should set all expected defaults", func(t *testing.T) {
		g := NewGomegaWithT(t)
		opts := NewInstallOptionsWithDefaults()

		g.Expect(opts.Namespace).To(Equal("hypershift"))
		g.Expect(opts.PrivatePlatform).To(Equal(string(hyperv1.NonePlatform)))
		g.Expect(opts.HyperShiftImage).To(Equal(HyperShiftImage))
		g.Expect(opts.ExternalDNSImage).To(Equal(ExternalDNSImage))
		g.Expect(opts.CertRotationScale).To(Equal(24 * time.Hour))
		g.Expect(opts.ImagePullPolicy).To(Equal("IfNotPresent"))
		g.Expect(opts.EnableConversionWebhook).To(BeTrue())
		g.Expect(opts.EnableDedicatedRequestServingIsolation).To(BeTrue())
		g.Expect(opts.EnableEtcdRecovery).To(BeTrue())
		g.Expect(opts.MetricsSet).To(Equal(metrics.DefaultMetricsSet))
		g.Expect(opts.AWSPrivateCredentialsSecretKey).To(Equal("credentials"))
		g.Expect(opts.ScaleFromZeroCredentialsSecretKey).To(Equal("credentials"))
		g.Expect(opts.OIDCStorageProviderS3CredentialsSecretKey).To(Equal("credentials"))
		g.Expect(opts.Development).To(BeFalse())
		g.Expect(opts.EnableAdminRBACGeneration).To(BeFalse())
		g.Expect(opts.AdditionalOperatorEnvVars).NotTo(BeNil())
	})
}

func TestHyperShiftNamespaceBuild(t *testing.T) {
	tests := []struct {
		name                         string
		nsConfig                     assets.HyperShiftNamespace
		expectClusterMonitoringLabel bool
	}{
		{
			name: "When OCP cluster monitoring is enabled, it should include the monitoring label",
			nsConfig: assets.HyperShiftNamespace{
				Name:                       "hypershift",
				EnableOCPClusterMonitoring: true,
			},
			expectClusterMonitoringLabel: true,
		},
		{
			name: "When OCP cluster monitoring is disabled, it should not include the monitoring label",
			nsConfig: assets.HyperShiftNamespace{
				Name:                       "hypershift",
				EnableOCPClusterMonitoring: false,
			},
			expectClusterMonitoringLabel: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			ns := tc.nsConfig.Build()
			g.Expect(ns.Name).To(Equal(tc.nsConfig.Name))
			g.Expect(ns.Labels).To(HaveKeyWithValue("hypershift.openshift.io/component", "operator"))
			if tc.expectClusterMonitoringLabel {
				g.Expect(ns.Labels).To(HaveKeyWithValue("openshift.io/cluster-monitoring", "true"))
			} else {
				g.Expect(ns.Labels).NotTo(HaveKey("openshift.io/cluster-monitoring"))
			}
		})
	}
}

func TestHyperShiftOperatorManifests_WebhookFlags(t *testing.T) {
	tests := []struct {
		name                    string
		opts                    Options
		expectWebhookVolume     bool
		expectCertDirArg        bool
		expectValidatingArg     bool
		expectCAPICRDConversion bool
	}{
		{
			name: "When no webhook flags are set but CAPI conversion is enabled by default, it should add webhook resources and CRD conversion",
			opts: Options{
				PrivatePlatform: string(hyperv1.NonePlatform),
			},
			expectWebhookVolume:     true,
			expectCertDirArg:        true,
			expectValidatingArg:     false,
			expectCAPICRDConversion: true,
		},
		{
			name: "When DisableCAPIConversionWebhook is true and no other webhooks, it should not add webhook resources or CRD conversion",
			opts: Options{
				PrivatePlatform:              string(hyperv1.NonePlatform),
				DisableCAPIConversionWebhook: true,
			},
			expectWebhookVolume:     false,
			expectCertDirArg:        false,
			expectValidatingArg:     false,
			expectCAPICRDConversion: false,
		},
		{
			name: "When EnableConversionWebhook is true, it should add webhook resources",
			opts: Options{
				PrivatePlatform:              string(hyperv1.NonePlatform),
				DisableCAPIConversionWebhook: true,
				EnableConversionWebhook:      true,
			},
			expectWebhookVolume:     true,
			expectCertDirArg:        true,
			expectValidatingArg:     false,
			expectCAPICRDConversion: false,
		},
		{
			name: "When EnableValidatingWebhook is true, it should add webhook resources and validating arg",
			opts: Options{
				PrivatePlatform:              string(hyperv1.NonePlatform),
				DisableCAPIConversionWebhook: true,
				EnableValidatingWebhook:      true,
			},
			expectWebhookVolume:     true,
			expectCertDirArg:        true,
			expectValidatingArg:     true,
			expectCAPICRDConversion: false,
		},
		{
			name: "When EnableDefaultingWebhook is true, it should add webhook resources",
			opts: Options{
				PrivatePlatform:              string(hyperv1.NonePlatform),
				DisableCAPIConversionWebhook: true,
				EnableDefaultingWebhook:      true,
			},
			expectWebhookVolume:     true,
			expectCertDirArg:        true,
			expectValidatingArg:     false,
			expectCAPICRDConversion: false,
		},
		{
			name: "When EnableAuditLogPersistence is true, it should add webhook resources",
			opts: Options{
				PrivatePlatform:              string(hyperv1.NonePlatform),
				DisableCAPIConversionWebhook: true,
				EnableAuditLogPersistence:    true,
			},
			expectWebhookVolume:     true,
			expectCertDirArg:        true,
			expectValidatingArg:     false,
			expectCAPICRDConversion: false,
		},
		{
			name: "When all webhook flags are enabled, it should add webhook resources with validating arg and CRD conversion",
			opts: Options{
				PrivatePlatform:         string(hyperv1.NonePlatform),
				EnableConversionWebhook: true,
				EnableDefaultingWebhook: true,
				EnableValidatingWebhook: true,
			},
			expectWebhookVolume:     true,
			expectCertDirArg:        true,
			expectValidatingArg:     true,
			expectCAPICRDConversion: true,
		},
		{
			name: "When all enable flags are false and DisableCAPIConversionWebhook is default (false), it should add webhook resources and CRD conversion",
			opts: Options{
				PrivatePlatform:           string(hyperv1.NonePlatform),
				EnableConversionWebhook:   false,
				EnableDefaultingWebhook:   false,
				EnableValidatingWebhook:   false,
				EnableAuditLogPersistence: false,
			},
			expectWebhookVolume:     true,
			expectCertDirArg:        true,
			expectValidatingArg:     false,
			expectCAPICRDConversion: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			tc.opts.HyperShiftImage = "test-image"
			tc.opts.Namespace = "hypershift"
			tc.opts.RenderNamespace = true

			ctx := context.Background()
			crds, objects, err := hyperShiftOperatorManifests(ctx, nil, tc.opts)
			g.Expect(err).NotTo(HaveOccurred())

			// Find the operator deployment
			var deployment *appsv1.Deployment
			for _, obj := range objects {
				if d, ok := obj.(*appsv1.Deployment); ok {
					deployment = d
					break
				}
			}
			g.Expect(deployment).NotTo(BeNil(), "should find operator deployment")

			container := deployment.Spec.Template.Spec.Containers[0]

			hasServingCertVolume := false
			for _, v := range deployment.Spec.Template.Spec.Volumes {
				if v.Name == "serving-cert" {
					hasServingCertVolume = true
					break
				}
			}

			hasServingCertMount := false
			for _, vm := range container.VolumeMounts {
				if vm.Name == "serving-cert" {
					hasServingCertMount = true
					break
				}
			}

			hasCertDirArg := false
			hasValidatingArg := false
			for _, arg := range container.Args {
				if arg == "--cert-dir=/var/run/secrets/serving-cert" {
					hasCertDirArg = true
				}
				if arg == "--enable-validating-webhook=true" {
					hasValidatingArg = true
				}
			}

			g.Expect(hasServingCertVolume).To(Equal(tc.expectWebhookVolume), "serving-cert volume")
			g.Expect(hasServingCertMount).To(Equal(tc.expectWebhookVolume), "serving-cert volume mount")
			g.Expect(hasCertDirArg).To(Equal(tc.expectCertDirArg), "--cert-dir arg")
			g.Expect(hasValidatingArg).To(Equal(tc.expectValidatingArg), "--enable-validating-webhook arg")

			// Validate CAPI CRD conversion webhook configuration
			for _, obj := range crds {
				crd, ok := obj.(*apiextensionsv1.CustomResourceDefinition)
				if !ok {
					continue
				}
				override, isCAPICRD := crdassets.CAPICRDOverrides[crd.Name]
				if !isCAPICRD || !override.NeedsConversion {
					continue
				}

				if tc.expectCAPICRDConversion {
					g.Expect(crd.Spec.Conversion).NotTo(BeNil(), "CRD %s should have conversion", crd.Name)
					g.Expect(crd.Spec.Conversion.Strategy).To(Equal(apiextensionsv1.WebhookConverter), "CRD %s conversion strategy", crd.Name)
					g.Expect(crd.Spec.Conversion.Webhook).NotTo(BeNil(), "CRD %s should have webhook config", crd.Name)
					g.Expect(crd.Spec.Conversion.Webhook.ConversionReviewVersions).To(ConsistOf("v1beta1", "v1beta2"), "CRD %s review versions", crd.Name)
				} else {
					g.Expect(crd.Spec.Conversion).To(BeNil(), "CRD %s should not have conversion when CAPI conversion is disabled", crd.Name)
				}
			}
		})
	}
}

func TestAwsRoleCredentialFileContent(t *testing.T) {
	tests := map[string]struct {
		roleARN          string
		credentialSource string
		expectContains   []string
	}{
		"When credential source is web-identity it should include token file path": {
			roleARN:          "arn:aws:iam::123456789012:role/test-role",
			credentialSource: aws.CredentialSourceWebIdentity,
			expectContains: []string{
				"role_arn = arn:aws:iam::123456789012:role/test-role",
				"web_identity_token_file = /var/run/secrets/openshift/serviceaccount/token",
			},
		},
		"When credential source is ec2-instance-metadata it should include Ec2InstanceMetadata": {
			roleARN:          "arn:aws:iam::123456789012:role/test-role",
			credentialSource: aws.CredentialSourceEC2InstanceMetadata,
			expectContains: []string{
				"role_arn = arn:aws:iam::123456789012:role/test-role",
				"credential_source = Ec2InstanceMetadata",
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			content := awsRoleCredentialFileContent(test.roleARN, test.credentialSource)
			for _, s := range test.expectContains {
				g.Expect(content).To(ContainSubstring(s))
			}
			g.Expect(content).To(HavePrefix("[default]"))
		})
	}
}

func TestLoadOperatorRolesFile(t *testing.T) {
	tests := map[string]struct {
		setup       func(t *testing.T) Options
		expectError bool
		validate    func(*GomegaWithT, Options)
	}{
		"When no roles file is specified it should be a no-op": {
			setup: func(t *testing.T) Options {
				return Options{}
			},
			validate: func(g *GomegaWithT, o Options) {
				g.Expect(o.AWSPrivateRoleARN).To(BeEmpty())
				g.Expect(o.OIDCStorageProviderS3RoleARN).To(BeEmpty())
				g.Expect(o.ExternalDNSRoleARN).To(BeEmpty())
			},
		},
		"When a valid roles file is specified it should populate role ARN fields": {
			setup: func(t *testing.T) Options {
				roles := aws.CreateOperatorRolesOutput{
					OperatorEC2RoleARN:    "arn:aws:iam::123456789012:role/op-ec2",
					OperatorOIDCS3RoleARN: "arn:aws:iam::123456789012:role/op-s3",
					ExternalDNSRoleARN:    "arn:aws:iam::123456789012:role/ext-dns",
				}
				data, err := json.Marshal(roles)
				if err != nil {
					t.Fatal(err)
				}
				f := filepath.Join(t.TempDir(), "roles.json")
				if err := os.WriteFile(f, data, 0644); err != nil {
					t.Fatal(err)
				}
				return Options{AWSOperatorRolesFile: f}
			},
			validate: func(g *GomegaWithT, o Options) {
				g.Expect(o.AWSPrivateRoleARN).To(Equal("arn:aws:iam::123456789012:role/op-ec2"))
				g.Expect(o.OIDCStorageProviderS3RoleARN).To(Equal("arn:aws:iam::123456789012:role/op-s3"))
				g.Expect(o.ExternalDNSRoleARN).To(Equal("arn:aws:iam::123456789012:role/ext-dns"))
			},
		},
		"When roles file conflicts with --aws-private-role-arn it should error": {
			setup: func(t *testing.T) Options {
				f := filepath.Join(t.TempDir(), "roles.json")
				if err := os.WriteFile(f, []byte(`{}`), 0644); err != nil {
					t.Fatal(err)
				}
				return Options{
					AWSOperatorRolesFile: f,
					AWSPrivateRoleARN:    "arn:aws:iam::123456789012:role/existing",
				}
			},
			expectError: true,
		},
		"When roles file conflicts with --oidc-storage-provider-s3-role-arn it should error": {
			setup: func(t *testing.T) Options {
				f := filepath.Join(t.TempDir(), "roles.json")
				if err := os.WriteFile(f, []byte(`{}`), 0644); err != nil {
					t.Fatal(err)
				}
				return Options{
					AWSOperatorRolesFile:         f,
					OIDCStorageProviderS3RoleARN: "arn:aws:iam::123456789012:role/existing",
				}
			},
			expectError: true,
		},
		"When roles file conflicts with --external-dns-role-arn it should error": {
			setup: func(t *testing.T) Options {
				f := filepath.Join(t.TempDir(), "roles.json")
				if err := os.WriteFile(f, []byte(`{}`), 0644); err != nil {
					t.Fatal(err)
				}
				return Options{
					AWSOperatorRolesFile: f,
					ExternalDNSRoleARN:   "arn:aws:iam::123456789012:role/existing",
				}
			},
			expectError: true,
		},
		"When roles file does not exist it should error": {
			setup: func(t *testing.T) Options {
				return Options{AWSOperatorRolesFile: "/nonexistent/path/roles.json"}
			},
			expectError: true,
		},
		"When roles file contains invalid JSON it should error": {
			setup: func(t *testing.T) Options {
				f := filepath.Join(t.TempDir(), "roles.json")
				if err := os.WriteFile(f, []byte("not json"), 0644); err != nil {
					t.Fatal(err)
				}
				return Options{AWSOperatorRolesFile: f}
			},
			expectError: true,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			opts := test.setup(t)
			err := opts.loadOperatorRolesFile()
			if test.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				if test.validate != nil {
					test.validate(g, opts)
				}
			}
		})
	}
}

func TestComplete(t *testing.T) {
	tests := map[string]struct {
		setup       func(t *testing.T) Options
		expectError bool
		validate    func(*GomegaWithT, Options)
	}{
		"When no operator roles file it should complete successfully": {
			setup: func(t *testing.T) Options {
				return Options{}
			},
		},
		"When ScaleFromZeroProvider has whitespace and uppercase it should normalize": {
			setup: func(t *testing.T) Options {
				return Options{ScaleFromZeroProvider: "  AWS  "}
			},
			validate: func(g *GomegaWithT, o Options) {
				g.Expect(o.ScaleFromZeroProvider).To(Equal("aws"))
			},
		},
		"When a valid operator roles file is specified it should load ARNs": {
			setup: func(t *testing.T) Options {
				roles := aws.CreateOperatorRolesOutput{
					OperatorEC2RoleARN:    "arn:aws:iam::123456789012:role/op-ec2",
					OperatorOIDCS3RoleARN: "arn:aws:iam::123456789012:role/op-s3",
					ExternalDNSRoleARN:    "arn:aws:iam::123456789012:role/ext-dns",
				}
				data, err := json.Marshal(roles)
				if err != nil {
					t.Fatal(err)
				}
				f := filepath.Join(t.TempDir(), "roles.json")
				if err := os.WriteFile(f, data, 0644); err != nil {
					t.Fatal(err)
				}
				return Options{AWSOperatorRolesFile: f}
			},
			validate: func(g *GomegaWithT, o Options) {
				g.Expect(o.AWSPrivateRoleARN).To(Equal("arn:aws:iam::123456789012:role/op-ec2"))
			},
		},
		"When operator roles file does not exist it should return error": {
			setup: func(t *testing.T) Options {
				return Options{AWSOperatorRolesFile: "/nonexistent/path/roles.json"}
			},
			expectError: true,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			opts := test.setup(t)
			err := opts.Complete()
			if test.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				if test.validate != nil {
					test.validate(g, opts)
				}
			}
		})
	}
}
