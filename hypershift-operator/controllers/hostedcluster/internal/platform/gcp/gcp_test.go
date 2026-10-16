package gcp

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"

	capigcp "sigs.k8s.io/cluster-api-provider-gcp/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/blang/semver"
	"github.com/google/go-cmp/cmp"
)

const (
	// Test service account emails used across GCP tests
	testNodePoolGSA        hyperv1.GCPServiceAccountEmail = "test-capg-sa@test-project.iam.gserviceaccount.com"
	testControlPlaneGSA    hyperv1.GCPServiceAccountEmail = "test-control-plane-sa@test-project.iam.gserviceaccount.com"
	testCloudControllerGSA hyperv1.GCPServiceAccountEmail = "test-cloud-controller@test-project.iam.gserviceaccount.com"
	testStorageGSA         hyperv1.GCPServiceAccountEmail = "test-storage@test-project.iam.gserviceaccount.com"
	testImageRegistryGSA   hyperv1.GCPServiceAccountEmail = "test-image-registry@test-project.iam.gserviceaccount.com"
	testNetworkGSA         hyperv1.GCPServiceAccountEmail = "test-network-sa@test-project.iam.gserviceaccount.com"
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

func objectsFromSecrets(secrets []*corev1.Secret) []client.Object {
	objects := make([]client.Object, 0, len(secrets))
	for _, secret := range secrets {
		objects = append(objects, secret.DeepCopy())
	}
	return objects
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
							Network:         testNetworkGSA,
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

func TestReconcileCredentialsCreatesAllRoleSecrets(t *testing.T) {
	tests := []struct {
		name        string
		sharedEmail bool
	}{
		{
			name: "distinct service account emails",
		},
		{
			name:        "shared service account email",
			sharedEmail: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			hcluster := validHostedCluster()
			if tt.sharedEmail {
				emails := &hcluster.Spec.Platform.GCP.WorkloadIdentity.ServiceAccountsEmails
				emails.ControlPlane = emails.NodePool
				emails.CloudController = emails.NodePool
				emails.Storage = emails.NodePool
				emails.ImageRegistry = emails.NodePool
				emails.Network = emails.NodePool
			}

			scheme := runtime.NewScheme()
			g.Expect(clientgoscheme.AddToScheme(scheme)).To(Succeed())
			g.Expect(hyperv1.AddToScheme(scheme)).To(Succeed())
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
			platform := New("test-utilities-image", "test-capg-image", &semver.Version{Major: 4, Minor: 17, Patch: 0})

			err := platform.ReconcileCredentials(
				context.Background(),
				fakeClient,
				testCreateOrUpdate,
				hcluster,
				"test-control-plane-namespace",
			)
			g.Expect(err).ToNot(HaveOccurred())

			expectedSecrets := []struct {
				secret *corev1.Secret
				email  hyperv1.GCPServiceAccountEmail
			}{
				{NodePoolManagementCredsSecret("test-control-plane-namespace"), hcluster.Spec.Platform.GCP.WorkloadIdentity.ServiceAccountsEmails.NodePool},
				{ControlPlaneOperatorCredsSecret("test-control-plane-namespace"), hcluster.Spec.Platform.GCP.WorkloadIdentity.ServiceAccountsEmails.ControlPlane},
				{CloudControllerCredsSecret("test-control-plane-namespace"), hcluster.Spec.Platform.GCP.WorkloadIdentity.ServiceAccountsEmails.CloudController},
				{GCPPDCloudCredentialsSecret("test-control-plane-namespace"), hcluster.Spec.Platform.GCP.WorkloadIdentity.ServiceAccountsEmails.Storage},
				{ImageRegistryCredsSecret("test-control-plane-namespace"), hcluster.Spec.Platform.GCP.WorkloadIdentity.ServiceAccountsEmails.ImageRegistry},
				{CNCCCredsSecret("test-control-plane-namespace"), hcluster.Spec.Platform.GCP.WorkloadIdentity.ServiceAccountsEmails.Network},
			}
			for _, expected := range expectedSecrets {
				secret := &corev1.Secret{}
				g.Expect(fakeClient.Get(context.Background(), client.ObjectKeyFromObject(expected.secret), secret)).ToNot(HaveOccurred(),
					"role Secret %q should be created", expected.secret.Name)
				g.Expect(secret.Data).To(HaveKey("application_default_credentials.json"))
				g.Expect(string(secret.Data["application_default_credentials.json"])).To(ContainSubstring(string(expected.email)))
			}
		})
	}
}

func TestReconcileCredentialsContinuesAfterUpsertError(t *testing.T) {
	g := NewWithT(t)
	hcluster := validHostedCluster()
	const controlPlaneNamespace = "test-control-plane-namespace"

	scheme := runtime.NewScheme()
	g.Expect(clientgoscheme.AddToScheme(scheme)).To(Succeed())
	baseClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	var attempts []string
	createOrUpdate := func(ctx context.Context, c client.Client, obj client.Object, mutate controllerutil.MutateFn) (controllerutil.OperationResult, error) {
		attempts = append(attempts, obj.GetName())
		if obj.GetName() == ControlPlaneOperatorCredsSecret(controlPlaneNamespace).Name {
			return controllerutil.OperationResultNone, apierrors.NewForbidden(corev1.Resource("secrets"), obj.GetName(), fmt.Errorf("rbac denied"))
		}
		return testCreateOrUpdate(ctx, c, obj, mutate)
	}

	platform := New("test-utilities-image", "test-capg-image", &semver.Version{Major: 4, Minor: 17, Patch: 0})
	err := platform.ReconcileCredentials(context.Background(), baseClient, createOrUpdate, hcluster, controlPlaneNamespace)

	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("failed to reconcile GCP cloud credential secret"))
	g.Expect(attempts).To(HaveLen(6))
	for _, secret := range []*corev1.Secret{
		NodePoolManagementCredsSecret(controlPlaneNamespace),
		CloudControllerCredsSecret(controlPlaneNamespace),
		GCPPDCloudCredentialsSecret(controlPlaneNamespace),
		ImageRegistryCredsSecret(controlPlaneNamespace),
		CNCCCredsSecret(controlPlaneNamespace),
	} {
		g.Expect(baseClient.Get(context.Background(), client.ObjectKeyFromObject(secret), &corev1.Secret{})).ToNot(HaveOccurred(),
			"role Secret %q should be reconciled after another role failed", secret.Name)
	}
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
	const controlPlaneNamespace = "test-control-plane-namespace"

	credentialSecrets := []*corev1.Secret{
		NodePoolManagementCredsSecret(controlPlaneNamespace),
		ControlPlaneOperatorCredsSecret(controlPlaneNamespace),
		CloudControllerCredsSecret(controlPlaneNamespace),
		GCPPDCloudCredentialsSecret(controlPlaneNamespace),
		ImageRegistryCredsSecret(controlPlaneNamespace),
		CNCCCredsSecret(controlPlaneNamespace),
	}
	credentialSecretNames := make([]string, 0, len(credentialSecrets))
	for _, secret := range credentialSecrets {
		credentialSecretNames = append(credentialSecretNames, secret.Name)
	}

	tests := []struct {
		name                   string
		existingNames          []string
		expectedRemainingNames []string
		unrelatedNames         []string
		otherNamespaceNames    []string
		terminatingName        string
		failDeleteName         string
	}{
		{
			name:          "When all GCP credential Secrets exist, it should delete every generated Secret",
			existingNames: credentialSecretNames,
		},
		{
			name:           "When an unrelated Secret exists, it should preserve it",
			existingNames:  append(append([]string{}, credentialSecretNames...), "unrelated-secret"),
			unrelatedNames: []string{"unrelated-secret"},
		},
		{
			name: "When no GCP credential Secrets exist, it should be idempotent",
		},
		{
			name:                "When the same credential name exists in another namespace, it should preserve it",
			existingNames:       []string{"node-management-creds"},
			otherNamespaceNames: []string{"node-management-creds"},
		},
		{
			name:                   "When a credential Secret is already terminating, it should leave it untouched",
			existingNames:          []string{"node-management-creds"},
			expectedRemainingNames: []string{"node-management-creds"},
			terminatingName:        "node-management-creds",
		},
		{
			name:                   "When deleting a credential Secret fails, it should return the error",
			existingNames:          []string{"node-management-creds"},
			expectedRemainingNames: []string{"node-management-creds"},
			failDeleteName:         "node-management-creds",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			scheme := runtime.NewScheme()
			g.Expect(clientgoscheme.AddToScheme(scheme)).To(Succeed())

			objects := make([]client.Object, 0, len(tt.existingNames)+len(tt.otherNamespaceNames))
			for _, name := range tt.existingNames {
				secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: controlPlaneNamespace,
				}}
				if name == tt.terminatingName {
					now := metav1.Now()
					secret.DeletionTimestamp = &now
					secret.Finalizers = []string{"test.finalizer"}
				}
				objects = append(objects, secret)
			}
			for _, name := range tt.otherNamespaceNames {
				objects = append(objects, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: "other-control-plane-namespace",
				}})
			}

			baseClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			var testClient client.Client = baseClient
			if tt.failDeleteName != "" {
				testClient = interceptor.NewClient(baseClient, interceptor.Funcs{
					Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
						if obj.GetName() == tt.failDeleteName {
							return apierrors.NewForbidden(corev1.Resource("secrets"), obj.GetName(), fmt.Errorf("rbac denied"))
						}
						return baseClient.Delete(ctx, obj, opts...)
					},
				})
			}

			platform := New("test-utilities-image", "test-capg-image", &semver.Version{Major: 4, Minor: 17, Patch: 0})
			err := platform.DeleteCredentials(
				context.Background(),
				testClient,
				nil,
				controlPlaneNamespace,
			)

			if tt.failDeleteName != "" {
				g.Expect(err).To(MatchError(ContainSubstring("forbidden")))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}

			expectedRemainingNames := make(map[string]struct{}, len(tt.expectedRemainingNames))
			for _, name := range tt.expectedRemainingNames {
				expectedRemainingNames[name] = struct{}{}
			}
			for _, name := range credentialSecretNames {
				secret := &corev1.Secret{}
				err := testClient.Get(context.Background(), client.ObjectKey{
					Name:      name,
					Namespace: controlPlaneNamespace,
				}, secret)
				if _, ok := expectedRemainingNames[name]; ok {
					g.Expect(err).ToNot(HaveOccurred(), "credential Secret %q should remain", name)
				} else {
					g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "credential Secret %q should be deleted, got %v", name, err)
				}
			}
			for _, name := range tt.otherNamespaceNames {
				secret := &corev1.Secret{}
				err := testClient.Get(context.Background(), client.ObjectKey{
					Name:      name,
					Namespace: "other-control-plane-namespace",
				}, secret)
				g.Expect(err).ToNot(HaveOccurred(), "credential Secret %q in another namespace should remain", name)
			}
			for _, name := range tt.unrelatedNames {
				secret := &corev1.Secret{}
				err := testClient.Get(context.Background(), client.ObjectKey{
					Name:      name,
					Namespace: controlPlaneNamespace,
				}, secret)
				g.Expect(err).ToNot(HaveOccurred(), "unrelated Secret %q should remain", name)
			}
			if tt.terminatingName != "" {
				secret := &corev1.Secret{}
				err := testClient.Get(context.Background(), client.ObjectKey{
					Name:      tt.terminatingName,
					Namespace: controlPlaneNamespace,
				}, secret)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(secret.Finalizers).To(Equal([]string{"test.finalizer"}))
				g.Expect(secret.DeletionTimestamp).ToNot(BeNil())
			}
		})
	}
}

func TestDeleteCredentialsAttemptsAllSecretsAfterError(t *testing.T) {
	g := NewWithT(t)
	const controlPlaneNamespace = "test-control-plane-namespace"

	credentialSecrets := []*corev1.Secret{
		NodePoolManagementCredsSecret(controlPlaneNamespace),
		ControlPlaneOperatorCredsSecret(controlPlaneNamespace),
		CloudControllerCredsSecret(controlPlaneNamespace),
		GCPPDCloudCredentialsSecret(controlPlaneNamespace),
		ImageRegistryCredsSecret(controlPlaneNamespace),
		CNCCCredsSecret(controlPlaneNamespace),
	}

	scheme := runtime.NewScheme()
	g.Expect(clientgoscheme.AddToScheme(scheme)).To(Succeed())
	baseClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objectsFromSecrets(credentialSecrets)...).Build()
	var deleteAttempts []string
	testClient := interceptor.NewClient(baseClient, interceptor.Funcs{
		Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			deleteAttempts = append(deleteAttempts, obj.GetName())
			if obj.GetName() == credentialSecrets[0].Name {
				return apierrors.NewForbidden(corev1.Resource("secrets"), obj.GetName(), fmt.Errorf("rbac denied"))
			}
			return baseClient.Delete(ctx, obj, opts...)
		},
	})

	platform := New("test-utilities-image", "test-capg-image", &semver.Version{Major: 4, Minor: 17, Patch: 0})
	err := platform.DeleteCredentials(context.Background(), testClient, nil, controlPlaneNamespace)

	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring(fmt.Sprintf("failed to clean up GCP cloud credential secret %s/%s", controlPlaneNamespace, credentialSecrets[0].Name)))
	g.Expect(deleteAttempts).To(HaveLen(len(credentialSecrets)), "cleanup should attempt every generated Secret")
	for _, secret := range credentialSecrets[1:] {
		getErr := baseClient.Get(context.Background(), client.ObjectKeyFromObject(secret), &corev1.Secret{})
		g.Expect(apierrors.IsNotFound(getErr)).To(BeTrue(), "credential Secret %q should be deleted, got %v", secret.Name, getErr)
	}
}

func TestDeleteCredentialsTreatsDeleteNotFoundAsIdempotent(t *testing.T) {
	g := NewWithT(t)
	const controlPlaneNamespace = "test-control-plane-namespace"

	credentialSecrets := []*corev1.Secret{
		NodePoolManagementCredsSecret(controlPlaneNamespace),
		ControlPlaneOperatorCredsSecret(controlPlaneNamespace),
		CloudControllerCredsSecret(controlPlaneNamespace),
		GCPPDCloudCredentialsSecret(controlPlaneNamespace),
		ImageRegistryCredsSecret(controlPlaneNamespace),
		CNCCCredsSecret(controlPlaneNamespace),
	}

	scheme := runtime.NewScheme()
	g.Expect(clientgoscheme.AddToScheme(scheme)).To(Succeed())
	baseClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objectsFromSecrets(credentialSecrets)...).Build()
	var deleteAttempts []string
	testClient := interceptor.NewClient(baseClient, interceptor.Funcs{
		Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			deleteAttempts = append(deleteAttempts, obj.GetName())
			if obj.GetName() == credentialSecrets[0].Name {
				return apierrors.NewNotFound(corev1.Resource("secrets"), obj.GetName())
			}
			return baseClient.Delete(ctx, obj, opts...)
		},
	})

	platform := New("test-utilities-image", "test-capg-image", &semver.Version{Major: 4, Minor: 17, Patch: 0})
	err := platform.DeleteCredentials(context.Background(), testClient, nil, controlPlaneNamespace)

	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(deleteAttempts).To(HaveLen(len(credentialSecrets)))
	for _, secret := range credentialSecrets[1:] {
		getErr := baseClient.Get(context.Background(), client.ObjectKeyFromObject(secret), &corev1.Secret{})
		g.Expect(apierrors.IsNotFound(getErr)).To(BeTrue(), "credential Secret %q should be deleted, got %v", secret.Name, getErr)
	}
}

func TestValidateWorkloadIdentityConfiguration(t *testing.T) {
	g := NewWithT(t)

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
		{
			name: "missing network service account email",
			mutate: func(hc *hyperv1.HostedCluster) {
				hc.Spec.Platform.GCP.WorkloadIdentity.ServiceAccountsEmails.Network = ""
			},
			errorMsg: "network service account email is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hc := validHostedCluster()
			if tt.mutate != nil {
				tt.mutate(hc)
			}
			err := validateWorkloadIdentityConfiguration(hc)
			if tt.errorMsg != "" {
				g.Expect(err).ToNot(BeNil())
				g.Expect(err.Error()).To(ContainSubstring(tt.errorMsg))
			} else {
				g.Expect(err).To(BeNil())
			}
		})
	}
}

func TestReconcileGCPClusterPreservesServerDefaultedFields(t *testing.T) {
	g := NewWithT(t)

	platform := New("test-utilities-image", "test-capg-image", &semver.Version{Major: 4, Minor: 17, Patch: 0})

	hc := validHostedCluster()
	hc.Spec.Platform.GCP.NetworkConfig = hyperv1.GCPNetworkConfig{
		Network:                     hyperv1.GCPResourceReference{Name: "test-network"},
		PrivateServiceConnectSubnet: hyperv1.GCPResourceReference{Name: "test-subnet"},
	}

	// Simulate a GCPCluster that CAPG has already defaulted with Mtu, Purpose, StackType
	gcpCluster := &capigcp.GCPCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-control-plane-namespace",
		},
		Spec: capigcp.GCPClusterSpec{
			Project: "test-project",
			Region:  "us-central1",
			Network: capigcp.NetworkSpec{
				Name: ptr.To("test-network"),
				Mtu:  1460,
				Subnets: capigcp.Subnets{{
					Name:      "test-subnet",
					Region:    "us-central1",
					Purpose:   ptr.To("PRIVATE_RFC_1918"),
					StackType: "IPV4_ONLY",
				}},
			},
		},
	}

	err := platform.reconcileGCPCluster(gcpCluster, hc, hyperv1.APIEndpoint{Host: "example.com", Port: 443})
	g.Expect(err).To(BeNil())

	// Verify server-defaulted fields are preserved
	g.Expect(gcpCluster.Spec.Network.Mtu).To(Equal(int64(1460)), "Mtu should be preserved")
	g.Expect(gcpCluster.Spec.Network.Subnets[0].Purpose).To(Equal(ptr.To("PRIVATE_RFC_1918")), "Purpose should be preserved")
	g.Expect(gcpCluster.Spec.Network.Subnets[0].StackType).To(Equal("IPV4_ONLY"), "StackType should be preserved")

	// Verify that network name, subnet name, and subnet region are still correctly set
	g.Expect(gcpCluster.Spec.Network.Name).To(Equal(ptr.To("test-network")))
	g.Expect(gcpCluster.Spec.Network.Subnets[0].Name).To(Equal("test-subnet"))
	g.Expect(gcpCluster.Spec.Network.Subnets[0].Region).To(Equal("us-central1"))
}

func buildGCPHostedControlPlane(tlsProfile *configv1.TLSSecurityProfile) *hyperv1.HostedControlPlane {
	return &hyperv1.HostedControlPlane{
		Spec: hyperv1.HostedControlPlaneSpec{
			Configuration: &hyperv1.ClusterConfiguration{
				APIServer: &configv1.APIServerSpec{
					TLSSecurityProfile: tlsProfile,
				},
			},
		},
	}
}

func TestCAPIProviderDeploymentSpecWithTLS(t *testing.T) {
	defaultArgs := []string{
		"--namespace=$(MY_NAMESPACE)",
		"--leader-elect=true",
		"--feature-gates=MachinePool=false",
		"--v=2",
	}

	defaultUtilitiesImage := "test-utilities-image"
	defaultCapgImage := "test-capg-image"

	customTLSProfile := &configv1.TLSSecurityProfile{
		Type: configv1.TLSProfileCustomType,
		Custom: &configv1.CustomTLSProfile{
			TLSProfileSpec: configv1.TLSProfileSpec{
				MinTLSVersion: configv1.VersionTLS12,
				Ciphers: []string{
					"ECDHE-ECDSA-AES128-GCM-SHA256",
					"ECDHE-RSA-AES128-GCM-SHA256",
				},
			},
		},
	}

	testCases := []struct {
		name           string
		hcp            *hyperv1.HostedControlPlane
		payloadVersion *semver.Version
		expectedArgs   []string
	}{
		{
			name:           "When HostedControlPlane is nil it should not append TLS args",
			payloadVersion: ptr.To(semver.MustParse("4.23.0")),
			expectedArgs:   defaultArgs,
		},
		{
			name: "When version is 4.22 and HCP has TLS profile it should not append TLS args",
			hcp: buildGCPHostedControlPlane(&configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileModernType,
			}),
			payloadVersion: ptr.To(semver.MustParse("4.22.0")),
			expectedArgs:   defaultArgs,
		},
		{
			name: "When version is 4.23 and HCP has Modern TLS profile it should append min-version only",
			hcp: buildGCPHostedControlPlane(&configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileModernType,
			}),
			payloadVersion: ptr.To(semver.MustParse("4.23.0")),
			expectedArgs: append(defaultArgs,
				"--tls-min-version=VersionTLS13",
			),
		},
		{
			name:           "When version is 5.0 and HCP has custom TLS profile it should append custom TLS args",
			hcp:            buildGCPHostedControlPlane(customTLSProfile),
			payloadVersion: ptr.To(semver.MustParse("5.0.0")),
			expectedArgs: append(defaultArgs,
				"--tls-min-version=VersionTLS12",
				"--tls-cipher-suites=TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
			),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			platform := New(defaultUtilitiesImage, defaultCapgImage, tc.payloadVersion)
			spec, err := platform.CAPIProviderDeploymentSpec(
				&hyperv1.HostedCluster{
					Spec: hyperv1.HostedClusterSpec{
						Platform: hyperv1.PlatformSpec{
							Type: hyperv1.GCPPlatform,
							GCP:  &hyperv1.GCPPlatformSpec{},
						},
					},
				},
				tc.hcp,
			)

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if spec == nil {
				t.Fatal("expected deployment spec, got nil")
			}
			if len(spec.Template.Spec.Containers) == 0 {
				t.Fatal("expected at least 1 container, got 0")
			}

			if diff := cmp.Diff(spec.Template.Spec.Containers[0].Args, tc.expectedArgs); diff != "" {
				t.Errorf("args differ (-got +want):\n%s", diff)
			}
		})
	}
}

func TestDeleteOrphanedMachines(t *testing.T) {
	buildScheme := func(g Gomega) *runtime.Scheme {
		scheme := runtime.NewScheme()
		g.Expect(clientgoscheme.AddToScheme(scheme)).To(Succeed())
		g.Expect(hyperv1.AddToScheme(scheme)).To(Succeed())
		g.Expect(capigcp.AddToScheme(scheme)).To(Succeed())
		return scheme
	}

	gcpMachines := func() []client.Object {
		return []client.Object{
			&capigcp.GCPMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "test-xl-1",
					Namespace:         "test-control-plane-namespace",
					DeletionTimestamp: ptr.To(metav1.Now()),
					Finalizers:        []string{capigcp.MachineFinalizer, "other-controller-finalizer"},
				},
			},
			&capigcp.GCPMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-xl-2",
					Namespace:  "test-control-plane-namespace",
					Finalizers: []string{capigcp.MachineFinalizer, "other-controller-finalizer"},
				},
			},
		}
	}

	for _, version := range []string{"4.23.0", "5.0.0", "", "invalid", "5.1.0"} {
		for _, reason := range []string{hyperv1.InvalidIdentityProvider, hyperv1.InvalidConfigurationReason, hyperv1.ReconciliationErrorReason} {
			if version == "5.1.0" && reason == hyperv1.InvalidIdentityProvider {
				continue
			}
			t.Run("When version is "+version+" and failure reason is "+reason+", it should preserve finalizers without runtime evidence", func(t *testing.T) {
				g := NewWithT(t)
				hc := validHostedCluster()
				hc.Status.ControlPlaneVersion.Desired.Version = version
				hc.Status.Conditions = []metav1.Condition{
					{Type: string(hyperv1.ValidGCPWorkloadIdentity), Status: metav1.ConditionFalse, Reason: reason},
					{Type: string(hyperv1.ValidGCPCredentials), Status: metav1.ConditionFalse, Reason: reason},
				}
				c := fake.NewClientBuilder().WithScheme(buildScheme(g)).WithObjects(gcpMachines()...).Build()
				g.Expect((GCP{}).DeleteOrphanedMachines(t.Context(), c, hc, "test-control-plane-namespace")).To(Succeed())
				machines := &capigcp.GCPMachineList{}
				g.Expect(c.List(t.Context(), machines)).To(Succeed())
				g.Expect(machines.Items).To(HaveLen(2))
				for _, machine := range machines.Items {
					g.Expect(machine.Finalizers).To(Equal([]string{capigcp.MachineFinalizer, "other-controller-finalizer"}))
				}
			})
		}
	}

	t.Run("When credentials are invalid, it should strip the CAPG finalizer from deleting machines", func(t *testing.T) {
		g := NewWithT(t)
		platform := New("test-utilities-image", "test-capg-image", &semver.Version{Major: 4, Minor: 17, Patch: 0})

		hc := validHostedCluster()
		hc.Status.ControlPlaneVersion.Desired.Version = "5.1.0"
		hc.Status.Conditions = []metav1.Condition{
			{Type: string(hyperv1.ValidGCPWorkloadIdentity), Status: metav1.ConditionFalse, Reason: hyperv1.InvalidIdentityProvider},
			{Type: string(hyperv1.ValidGCPCredentials), Status: metav1.ConditionFalse, Reason: hyperv1.InvalidIdentityProvider},
		}

		fakeClient := fake.NewClientBuilder().
			WithObjects(gcpMachines()...).
			WithScheme(buildScheme(g)).
			Build()

		err := platform.DeleteOrphanedMachines(t.Context(), fakeClient, hc, "test-control-plane-namespace")
		g.Expect(err).To(BeNil())

		gcpMachineList := &capigcp.GCPMachineList{}
		g.Expect(fakeClient.List(t.Context(), gcpMachineList)).To(Succeed())

		for _, gcpMachine := range gcpMachineList.Items {
			if !gcpMachine.DeletionTimestamp.IsZero() {
				g.Expect(controllerutil.ContainsFinalizer(&gcpMachine, capigcp.MachineFinalizer)).To(BeFalse(), "CAPG finalizer should be removed")
				g.Expect(gcpMachine.Finalizers).To(Equal([]string{"other-controller-finalizer"}), "other finalizers should be preserved")
			} else {
				g.Expect(gcpMachine.Finalizers).To(Equal([]string{capigcp.MachineFinalizer, "other-controller-finalizer"}))
			}
		}
	})

	t.Run("When credentials are valid, it should leave finalizers unchanged", func(t *testing.T) {
		g := NewWithT(t)
		platform := New("test-utilities-image", "test-capg-image", &semver.Version{Major: 4, Minor: 17, Patch: 0})

		hc := validHostedCluster()
		hc.Status.ControlPlaneVersion.Desired.Version = "5.1.0"
		hc.Status.Conditions = []metav1.Condition{
			{Type: string(hyperv1.ValidGCPWorkloadIdentity), Status: metav1.ConditionTrue},
			{Type: string(hyperv1.ValidGCPCredentials), Status: metav1.ConditionTrue},
		}

		fakeClient := fake.NewClientBuilder().
			WithObjects(gcpMachines()...).
			WithScheme(buildScheme(g)).
			Build()

		err := platform.DeleteOrphanedMachines(t.Context(), fakeClient, hc, "test-control-plane-namespace")
		g.Expect(err).To(BeNil())

		gcpMachineList := &capigcp.GCPMachineList{}
		g.Expect(fakeClient.List(t.Context(), gcpMachineList)).To(Succeed())

		for _, gcpMachine := range gcpMachineList.Items {
			g.Expect(gcpMachine.Finalizers).To(Equal([]string{capigcp.MachineFinalizer, "other-controller-finalizer"}))
		}
	})

	t.Run("When credentials are unknown, it should leave finalizers unchanged", func(t *testing.T) {
		g := NewWithT(t)
		platform := New("test-utilities-image", "test-capg-image", &semver.Version{Major: 4, Minor: 17, Patch: 0})

		hc := validHostedCluster()
		hc.Status.ControlPlaneVersion.Desired.Version = "5.1.0"
		// No conditions set — GetCredentialStatus returns Unknown

		fakeClient := fake.NewClientBuilder().
			WithObjects(gcpMachines()...).
			WithScheme(buildScheme(g)).
			Build()

		err := platform.DeleteOrphanedMachines(t.Context(), fakeClient, hc, "test-control-plane-namespace")
		g.Expect(err).To(BeNil())

		gcpMachineList := &capigcp.GCPMachineList{}
		g.Expect(fakeClient.List(t.Context(), gcpMachineList)).To(Succeed())

		for _, gcpMachine := range gcpMachineList.Items {
			g.Expect(gcpMachine.Finalizers).To(Equal([]string{capigcp.MachineFinalizer, "other-controller-finalizer"}))
		}
	})

	t.Run("When c.List fails, it should return the error", func(t *testing.T) {
		g := NewWithT(t)
		platform := New("test-utilities-image", "test-capg-image", &semver.Version{Major: 4, Minor: 17, Patch: 0})

		hc := validHostedCluster()
		hc.Status.ControlPlaneVersion.Desired.Version = "5.1.0"
		hc.Status.Conditions = []metav1.Condition{
			{Type: string(hyperv1.ValidGCPWorkloadIdentity), Status: metav1.ConditionFalse, Reason: hyperv1.InvalidIdentityProvider},
			{Type: string(hyperv1.ValidGCPCredentials), Status: metav1.ConditionFalse, Reason: hyperv1.InvalidIdentityProvider},
		}

		listErr := fmt.Errorf("list failed")
		fakeClient := interceptor.NewClient(
			fake.NewClientBuilder().WithScheme(buildScheme(g)).Build(),
			interceptor.Funcs{
				List: func(_ context.Context, _ client.WithWatch, _ client.ObjectList, _ ...client.ListOption) error {
					return listErr
				},
			},
		)

		err := platform.DeleteOrphanedMachines(t.Context(), fakeClient, hc, "test-control-plane-namespace")
		g.Expect(err).To(MatchError(ContainSubstring("failed to list GCPMachines")), "expected list error to be wrapped")
	})

	t.Run("When c.Update fails for a machine, it should aggregate errors and continue", func(t *testing.T) {
		g := NewWithT(t)
		platform := New("test-utilities-image", "test-capg-image", &semver.Version{Major: 4, Minor: 17, Patch: 0})

		hc := validHostedCluster()
		hc.Status.ControlPlaneVersion.Desired.Version = "5.1.0"
		hc.Status.Conditions = []metav1.Condition{
			{Type: string(hyperv1.ValidGCPWorkloadIdentity), Status: metav1.ConditionFalse, Reason: hyperv1.InvalidIdentityProvider},
			{Type: string(hyperv1.ValidGCPCredentials), Status: metav1.ConditionFalse, Reason: hyperv1.InvalidIdentityProvider},
		}

		// Two deleted machines; Update fails for both.
		machines := []client.Object{
			&capigcp.GCPMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "test-m-1",
					Namespace:         "test-control-plane-namespace",
					DeletionTimestamp: ptr.To(metav1.Now()),
					Finalizers:        []string{capigcp.MachineFinalizer},
				},
			},
			&capigcp.GCPMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "test-m-2",
					Namespace:         "test-control-plane-namespace",
					DeletionTimestamp: ptr.To(metav1.Now()),
					Finalizers:        []string{capigcp.MachineFinalizer},
				},
			},
		}

		updateErr := fmt.Errorf("update failed")
		fakeClient := interceptor.NewClient(
			fake.NewClientBuilder().WithObjects(machines...).WithScheme(buildScheme(g)).Build(),
			interceptor.Funcs{
				Update: func(_ context.Context, _ client.WithWatch, _ client.Object, _ ...client.UpdateOption) error {
					return updateErr
				},
			},
		)

		err := platform.DeleteOrphanedMachines(t.Context(), fakeClient, hc, "test-control-plane-namespace")
		g.Expect(err).To(HaveOccurred(), "expected aggregated error")
		g.Expect(err.Error()).To(ContainSubstring("test-m-1"), "error should mention first machine")
		g.Expect(err.Error()).To(ContainSubstring("test-m-2"), "error should mention second machine")
	})

	t.Run("When GCPMachine has no CAPG finalizer, it should skip it without error", func(t *testing.T) {
		g := NewWithT(t)
		platform := New("test-utilities-image", "test-capg-image", &semver.Version{Major: 4, Minor: 17, Patch: 0})

		hc := validHostedCluster()
		hc.Status.ControlPlaneVersion.Desired.Version = "5.1.0"
		hc.Status.Conditions = []metav1.Condition{
			{Type: string(hyperv1.ValidGCPWorkloadIdentity), Status: metav1.ConditionFalse, Reason: hyperv1.InvalidIdentityProvider},
			{Type: string(hyperv1.ValidGCPCredentials), Status: metav1.ConditionFalse, Reason: hyperv1.InvalidIdentityProvider},
		}

		// Deleted machine, but without the CAPG finalizer (already removed by another path).
		machines := []client.Object{
			&capigcp.GCPMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "test-no-finalizer",
					Namespace:         "test-control-plane-namespace",
					DeletionTimestamp: ptr.To(metav1.Now()),
					Finalizers:        []string{"some-other-finalizer"},
				},
			},
		}

		fakeClient := fake.NewClientBuilder().
			WithObjects(machines...).
			WithScheme(buildScheme(g)).
			Build()

		err := platform.DeleteOrphanedMachines(t.Context(), fakeClient, hc, "test-control-plane-namespace")
		g.Expect(err).To(BeNil(), "machine without CAPG finalizer should be skipped without error")

		// Verify the other finalizer is untouched.
		gcpMachineList := &capigcp.GCPMachineList{}
		g.Expect(fakeClient.List(t.Context(), gcpMachineList)).To(Succeed())
		g.Expect(gcpMachineList.Items[0].Finalizers).To(Equal([]string{"some-other-finalizer"}), "other finalizers must not be modified")
	})
}
