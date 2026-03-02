package gcp

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"

	capigcp "sigs.k8s.io/cluster-api-provider-gcp/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/blang/semver"
)

const (
	// Test service account emails used across GCP tests
	testNodePoolGSA        = "test-capg-sa@test-project.iam.gserviceaccount.com"
	testControlPlaneGSA    = "test-control-plane-sa@test-project.iam.gserviceaccount.com"
	testCloudControllerGSA = "test-cloud-controller@test-project.iam.gserviceaccount.com"
	testStorageGSA         = "test-storage@test-project.iam.gserviceaccount.com"
	testImageRegistryGSA   = "test-image-registry@test-project.iam.gserviceaccount.com"
)

// testCreateOrUpdate is a test helper that implements createOrUpdate functionality
// for testing without requiring the actual upsert package dependencies.
func testCreateOrUpdate(ctx context.Context, c client.Client, obj client.Object, f controllerutil.MutateFn) (controllerutil.OperationResult, error) {
	// Check if object exists
	key := client.ObjectKeyFromObject(obj)
	existing := obj.DeepCopyObject().(client.Object)
	err := c.Get(ctx, key, existing)
	if client.IgnoreNotFound(err) != nil {
		return controllerutil.OperationResultNone, err
	}

	if err != nil {
		// Object doesn't exist, create it
		if err := f(); err != nil {
			return controllerutil.OperationResultNone, err
		}
		if err := c.Create(ctx, obj); err != nil {
			return controllerutil.OperationResultNone, err
		}
		return controllerutil.OperationResultCreated, nil
	} else {
		// Object exists, update it
		if err := f(); err != nil {
			return controllerutil.OperationResultNone, err
		}
		obj.SetResourceVersion(existing.GetResourceVersion())
		if err := c.Update(ctx, obj); err != nil {
			return controllerutil.OperationResultNone, err
		}
		return controllerutil.OperationResultUpdated, nil
	}
}

// testSimpleCreateOrUpdate is a simplified test helper for tests that don't need
// the complex createOrUpdate logic (just applies the mutation).
func testSimpleCreateOrUpdate(ctx context.Context, c client.Client, obj client.Object, f controllerutil.MutateFn) (controllerutil.OperationResult, error) {
	return controllerutil.OperationResultCreated, f()
}

// validHostedCluster returns a baseline HostedCluster with a valid GCP WIF config.
// Callers can modify individual fields to test specific scenarios.
func validHostedCluster() *hyperv1.HostedCluster {
	return &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-namespace",
		},
		Spec: hyperv1.HostedClusterSpec{
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.GCPPlatform,
				GCP: &hyperv1.GCPPlatformSpec{
					Project: "test-project",
					Region:  "us-central1",
					WorkloadIdentity: hyperv1.GCPWorkloadIdentityConfig{
						ProjectNumber: "123456789012",
						PoolID:        "test-pool",
						ProviderID:    "test-provider",
						ServiceAccountsEmails: hyperv1.GCPServiceAccountsEmails{
							NodePool:        testNodePoolGSA,
							ControlPlane:    testControlPlaneGSA,
							CloudController: testCloudControllerGSA,
							Storage:         testStorageGSA,
							ImageRegistry:   testImageRegistryGSA,
						},
					},
				},
			},
		},
	}
}

func TestGCPPlatformInterface(t *testing.T) {
	g := NewWithT(t)

	// Test that GCP implements the Platform interface
	platform := New("test-utilities-image", "test-capg-image", &semver.Version{Major: 4, Minor: 17, Patch: 0})
	g.Expect(platform).ToNot(BeNil())
}

func TestReconcileCAPIInfraCR(t *testing.T) {
	g := NewWithT(t)

	platform := New("test-utilities-image", "test-capg-image", &semver.Version{Major: 4, Minor: 17, Patch: 0})

	// Create a scheme with both HyperShift and CAPG types
	scheme := runtime.NewScheme()
	g.Expect(clientgoscheme.AddToScheme(scheme)).To(Succeed())
	g.Expect(hyperv1.AddToScheme(scheme)).To(Succeed())
	g.Expect(capigcp.AddToScheme(scheme)).To(Succeed())

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&capigcp.GCPCluster{}).WithObjects().Build()

	// Test CAPI infrastructure reconciliation
	obj, err := platform.ReconcileCAPIInfraCR(
		context.Background(),
		fakeClient,
		testCreateOrUpdate,
		validHostedCluster(),
		"test-control-plane-namespace",
		hyperv1.APIEndpoint{Host: "example.com", Port: 443},
	)

	g.Expect(err).To(BeNil())
	g.Expect(obj).ToNot(BeNil()) // Should create GCPCluster object

	// Verify that the object has the Ready status set
	gcpCluster, ok := obj.(*capigcp.GCPCluster)
	g.Expect(ok).To(BeTrue())
	g.Expect(gcpCluster.Status.Ready).To(BeTrue()) // Critical: Ready status must be set
}

func TestCAPIProviderDeploymentSpec(t *testing.T) {
	g := NewWithT(t)

	platform := New("test-utilities-image", "test-capg-image", &semver.Version{Major: 4, Minor: 17, Patch: 0})

	// Test minimal implementation returns nil (no CAPI provider)
	spec, err := platform.CAPIProviderDeploymentSpec(
		validHostedCluster(),
		nil, // HostedControlPlane
	)

	g.Expect(err).To(BeNil())
	g.Expect(spec).ToNot(BeNil()) // Should return deployment spec
}

func TestCAPIProviderDeploymentSpecNilGCPPlatform(t *testing.T) {
	g := NewWithT(t)

	platform := New("test-utilities-image", "test-capg-image", &semver.Version{Major: 4, Minor: 17, Patch: 0})

	// Test that nil GCP platform configuration returns appropriate error
	spec, err := platform.CAPIProviderDeploymentSpec(
		&hyperv1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "test-namespace",
			},
			Spec: hyperv1.HostedClusterSpec{
				Platform: hyperv1.PlatformSpec{
					Type: hyperv1.GCPPlatform,
					GCP:  nil, // This should trigger the nil check error
				},
			},
		},
		nil, // HostedControlPlane
	)

	g.Expect(err).ToNot(BeNil())
	g.Expect(err.Error()).To(ContainSubstring("GCP platform configuration is missing"))
	g.Expect(spec).To(BeNil())
}

func TestReconcileCredentials(t *testing.T) {
	g := NewWithT(t)

	platform := New("test-utilities-image", "test-capg-image", &semver.Version{Major: 4, Minor: 17, Patch: 0})

	// Create a scheme with both HyperShift and CAPG types
	scheme := runtime.NewScheme()
	g.Expect(clientgoscheme.AddToScheme(scheme)).To(Succeed())
	g.Expect(hyperv1.AddToScheme(scheme)).To(Succeed())
	g.Expect(capigcp.AddToScheme(scheme)).To(Succeed())

	hcluster := validHostedCluster()
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&hyperv1.HostedCluster{}).WithObjects(hcluster).Build()

	// Test minimal implementation returns no error
	err := platform.ReconcileCredentials(
		context.Background(),
		fakeClient,
		testSimpleCreateOrUpdate,
		hcluster,
		"test-control-plane-namespace",
	)

	g.Expect(err).To(BeNil()) // Minimal implementation returns nil
}

func TestReconcileCredentialsNilGCPPlatform(t *testing.T) {
	g := NewWithT(t)

	platform := New("test-utilities-image", "test-capg-image", &semver.Version{Major: 4, Minor: 17, Patch: 0})

	// Create a scheme with HyperShift types
	scheme := runtime.NewScheme()
	g.Expect(clientgoscheme.AddToScheme(scheme)).To(Succeed())
	g.Expect(hyperv1.AddToScheme(scheme)).To(Succeed())

	// Create test HostedCluster object WITHOUT GCP platform configuration
	hcluster := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-namespace",
		},
		Spec: hyperv1.HostedClusterSpec{
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.GCPPlatform,
				GCP:  nil, // This should trigger the nil check error
			},
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&hyperv1.HostedCluster{}).WithObjects(hcluster).Build()

	// Test that nil GCP platform configuration returns appropriate error
	err := platform.ReconcileCredentials(
		context.Background(),
		fakeClient,
		testSimpleCreateOrUpdate,
		hcluster,
		"test-control-plane-namespace",
	)

	g.Expect(err).ToNot(BeNil())
	g.Expect(err.Error()).To(ContainSubstring("GCP platform configuration is missing"))
}

func TestReconcileSecretEncryption(t *testing.T) {
	g := NewWithT(t)

	platform := New("test-utilities-image", "test-capg-image", &semver.Version{Major: 4, Minor: 17, Patch: 0})
	fakeClient := fake.NewClientBuilder().Build()

	// Test minimal implementation returns no error
	err := platform.ReconcileSecretEncryption(
		context.Background(),
		fakeClient,
		testSimpleCreateOrUpdate,
		validHostedCluster(),
		"test-control-plane-namespace",
	)

	g.Expect(err).To(BeNil()) // Minimal implementation returns nil
}

func TestCAPIProviderPolicyRules(t *testing.T) {
	g := NewWithT(t)

	platform := New("test-utilities-image", "test-capg-image", &semver.Version{Major: 4, Minor: 17, Patch: 0})

	// Test implementation follows AWS/Azure pattern - returns nil for standard CAPI RBAC
	rules := platform.CAPIProviderPolicyRules()
	g.Expect(rules).To(BeNil()) // Should return nil like AWS/Azure platforms
}

func TestDeleteCredentials(t *testing.T) {
	g := NewWithT(t)

	platform := New("test-utilities-image", "test-capg-image", &semver.Version{Major: 4, Minor: 17, Patch: 0})
	fakeClient := fake.NewClientBuilder().Build()

	// Test minimal implementation returns no error
	err := platform.DeleteCredentials(
		context.Background(),
		fakeClient,
		validHostedCluster(),
		"test-control-plane-namespace",
	)

	g.Expect(err).To(BeNil()) // Minimal implementation returns nil
}

func TestBuildGCPWorkloadIdentityCredentials(t *testing.T) {
	g := NewWithT(t)

	wif := hyperv1.GCPWorkloadIdentityConfig{
		ProjectNumber: "123456789012",
		PoolID:        "test-pool",
		ProviderID:    "test-provider",
		ServiceAccountsEmails: hyperv1.GCPServiceAccountsEmails{
			NodePool:        testNodePoolGSA,
			ControlPlane:    testControlPlaneGSA,
			CloudController: testCloudControllerGSA,
			Storage:         testStorageGSA,
			ImageRegistry:   testImageRegistryGSA,
		},
	}

	// Using NodePool GSA as an example - the function is generic and works the same
	// for any service account email (NodePool, ControlPlane, CloudController, etc.)
	credentials, err := buildGCPWorkloadIdentityCredentials(wif, wif.ServiceAccountsEmails.NodePool)
	g.Expect(err).To(BeNil())
	g.Expect(credentials).To(ContainSubstring(`"type":"external_account"`))
	g.Expect(credentials).To(ContainSubstring("123456789012"))
	g.Expect(credentials).To(ContainSubstring("test-pool"))
	g.Expect(credentials).To(ContainSubstring("test-provider"))
	g.Expect(credentials).To(ContainSubstring("/var/run/secrets/openshift/serviceaccount/token"))
}

func TestBuildGCPWorkloadIdentityCredentialsValidation(t *testing.T) {
	g := NewWithT(t)

	// validWIF returns a baseline valid GCPWorkloadIdentityConfig.
	// Callers mutate individual fields to test specific validation errors.
	validWIF := func() hyperv1.GCPWorkloadIdentityConfig {
		return hyperv1.GCPWorkloadIdentityConfig{
			ProjectNumber: "123456789012",
			PoolID:        "test-pool",
			ProviderID:    "test-provider",
			ServiceAccountsEmails: hyperv1.GCPServiceAccountsEmails{
				NodePool:        testNodePoolGSA,
				ControlPlane:    testControlPlaneGSA,
				CloudController: testCloudControllerGSA,
				Storage:         testStorageGSA,
				ImageRegistry:   testImageRegistryGSA,
			},
		}
	}

	tests := []struct {
		name     string
		mutate   func(*hyperv1.GCPWorkloadIdentityConfig)
		errorMsg string
	}{
		{
			name:   "valid configuration",
			mutate: nil,
		},
		{
			name:     "missing project number",
			mutate:   func(wif *hyperv1.GCPWorkloadIdentityConfig) { wif.ProjectNumber = "" },
			errorMsg: "project number cannot be empty",
		},
		{
			name:     "missing pool ID",
			mutate:   func(wif *hyperv1.GCPWorkloadIdentityConfig) { wif.PoolID = "" },
			errorMsg: "pool ID cannot be empty",
		},
		{
			name:     "missing provider ID",
			mutate:   func(wif *hyperv1.GCPWorkloadIdentityConfig) { wif.ProviderID = "" },
			errorMsg: "provider ID cannot be empty",
		},
		{
			name:     "missing service account email",
			mutate:   func(wif *hyperv1.GCPWorkloadIdentityConfig) { wif.ServiceAccountsEmails.NodePool = "" },
			errorMsg: "service account email cannot be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wif := validWIF()
			if tt.mutate != nil {
				tt.mutate(&wif)
			}
			// Using NodePool GSA as the serviceAccountEmail parameter - the function
			// is generic and works the same for any service account email
			_, err := buildGCPWorkloadIdentityCredentials(wif, wif.ServiceAccountsEmails.NodePool)
			if tt.errorMsg != "" {
				g.Expect(err).ToNot(BeNil())
				g.Expect(err.Error()).To(ContainSubstring(tt.errorMsg))
			} else {
				g.Expect(err).To(BeNil())
			}
		})
	}
}

func TestValidateWorkloadIdentityConfiguration(t *testing.T) {
	g := NewWithT(t)

	platform := New("test-utilities-image", "test-capg-image", &semver.Version{Major: 4, Minor: 17, Patch: 0})

	tests := []struct {
		name     string
		mutate   func(*hyperv1.HostedCluster)
		errorMsg string
	}{
		{
			name:   "valid configuration",
			mutate: nil,
		},
		{
			name: "missing node pool service account email",
			mutate: func(hc *hyperv1.HostedCluster) {
				hc.Spec.Platform.GCP.WorkloadIdentity.ServiceAccountsEmails.NodePool = ""
			},
			errorMsg: "node pool service account email is required",
		},
		{
			name: "missing control plane service account email",
			mutate: func(hc *hyperv1.HostedCluster) {
				hc.Spec.Platform.GCP.WorkloadIdentity.ServiceAccountsEmails.ControlPlane = ""
			},
			errorMsg: "control plane service account email is required",
		},
		{
			name: "missing storage service account email",
			mutate: func(hc *hyperv1.HostedCluster) {
				hc.Spec.Platform.GCP.WorkloadIdentity.ServiceAccountsEmails.Storage = ""
			},
			errorMsg: "storage service account email is required",
		},
		{
			name: "missing cloud controller service account email",
			mutate: func(hc *hyperv1.HostedCluster) {
				hc.Spec.Platform.GCP.WorkloadIdentity.ServiceAccountsEmails.CloudController = ""
			},
			errorMsg: "cloud controller service account email is required",
		},
		{
			name: "missing image registry service account email",
			mutate: func(hc *hyperv1.HostedCluster) {
				hc.Spec.Platform.GCP.WorkloadIdentity.ServiceAccountsEmails.ImageRegistry = ""
			},
			errorMsg: "image registry service account email is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hc := validHostedCluster()
			if tt.mutate != nil {
				tt.mutate(hc)
			}
			err := platform.validateWorkloadIdentityConfiguration(hc)
			if tt.errorMsg != "" {
				g.Expect(err).ToNot(BeNil())
				g.Expect(err.Error()).To(ContainSubstring(tt.errorMsg))
			} else {
				g.Expect(err).To(BeNil())
			}
		})
	}
}
