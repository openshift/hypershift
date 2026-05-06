package install

import (
	"context"
	"io"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	crdassets "github.com/openshift/hypershift/cmd/install/assets/crds"
	"github.com/openshift/hypershift/hypershift-operator/controllers/sharedingress"
	hyperapi "github.com/openshift/hypershift/support/api"

	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/set"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
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
			crds, err := setupCRDs(t.Context(), nil, tc.inputOptions, &corev1.Namespace{}, nil, nil)
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
	clusterAPIGVK := schema.GroupVersionKind{
		Group:   operatorv1alpha1.GroupVersion.Group,
		Version: operatorv1alpha1.GroupVersion.Version,
		Kind:    "ClusterAPI",
	}

	makeConfig := func(t *testing.T, generation, observedRevisionGeneration int64, currentRevision, desiredRevision string) *unstructured.Unstructured {
		t.Helper()
		g := NewGomegaWithT(t)
		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(clusterAPIGVK)
		obj.SetName("cluster")
		obj.SetGeneration(generation)
		status := map[string]any{
			"observedRevisionGeneration": observedRevisionGeneration,
			"desiredRevision":            desiredRevision,
			"revisions": []any{
				map[string]any{"name": desiredRevision, "revision": observedRevisionGeneration, "contentID": "content"},
			},
		}
		if currentRevision != "" {
			status["currentRevision"] = currentRevision
		}
		g.Expect(unstructured.SetNestedField(obj.Object, status, "status")).To(Succeed())
		return obj
	}

	tests := []struct {
		name            string
		config          *unstructured.Unstructured
		patchGeneration int64
		expectSuccess   bool
	}{
		{
			name:            "When revision controller has observed the patch generation and installer has applied it should succeed",
			config:          makeConfig(t, 2, 2, "rev-2", "rev-2"),
			patchGeneration: 2,
			expectSuccess:   true,
		},
		{
			name:            "When observedRevisionGeneration is ahead of patch generation it should succeed",
			config:          makeConfig(t, 3, 3, "rev-3", "rev-3"),
			patchGeneration: 2,
			expectSuccess:   true,
		},
		{
			name:            "When revision controller has not observed the patch generation it should time out",
			config:          makeConfig(t, 3, 2, "rev-2", "rev-2"),
			patchGeneration: 3,
			expectSuccess:   false,
		},
		{
			name:            "When reading a stale object from before the patch it should time out",
			config:          makeConfig(t, 1, 1, "rev-1", "rev-1"),
			patchGeneration: 2,
			expectSuccess:   false,
		},
		{
			name:            "When currentRevision does not match desiredRevision it should time out",
			config:          makeConfig(t, 2, 2, "rev-1", "rev-2"),
			patchGeneration: 2,
			expectSuccess:   false,
		},
		{
			name:            "When currentRevision is empty it should time out",
			config:          makeConfig(t, 1, 1, "", "rev-1"),
			patchGeneration: 1,
			expectSuccess:   false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			// Use an interceptor to return the unstructured object directly,
			// bypassing the fake client's typed round-trip which would drop
			// the observedRevisionGeneration field not present in the vendored type.
			config := tc.config
			client := fake.NewClientBuilder().
				WithScheme(hyperapi.Scheme).
				WithInterceptorFuncs(interceptor.Funcs{
					Get: func(ctx context.Context, c crclient.WithWatch, key crclient.ObjectKey, obj crclient.Object, opts ...crclient.GetOption) error {
						u, ok := obj.(*unstructured.Unstructured)
						if !ok {
							return c.Get(ctx, key, obj, opts...)
						}
						u.Object = config.DeepCopy().Object
						return nil
					},
				}).
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
