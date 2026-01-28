package oci

import (
	"context"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/upsert"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestReconcileCredentials(t *testing.T) {
	ctx := context.Background()
	hcNamespace := "test-namespace"
	cpNamespace := "test-cp-namespace"
	secretName := "test-oci-creds"

	// Create test HostedCluster with OCI platform
	hc := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: hcNamespace,
		},
		Spec: hyperv1.HostedClusterSpec{
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.OCIPlatform,
				OCI: &hyperv1.OCIPlatformSpec{
					IdentityRef: hyperv1.OCIIdentityReference{
						Name: secretName,
					},
					Region:        "us-sanjose-1",
					CompartmentID: "ocid1.compartment.oc1..aaaaaaaazgovbe2qxduadk3bmj5dobvoe5wnengzavax5pwsfr3bqbdrrcqa",
				},
			},
		},
	}

	// Create source credentials secret
	srcSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: hcNamespace,
		},
		Data: map[string][]byte{
			credentialsConfigKey: []byte("[DEFAULT]\nuser=ocid1.user.oc1..test\n"),
			credentialsKeyKey:    []byte("-----BEGIN RSA PRIVATE KEY-----\ntest\n-----END RSA PRIVATE KEY-----\n"),
		},
	}

	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = hyperv1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(hc, srcSecret).
		Build()

	ociPlatform := New("")

	// Test ReconcileCredentials
	createOrUpdate := upsert.New(false)
	err := ociPlatform.ReconcileCredentials(ctx, fakeClient, createOrUpdate.CreateOrUpdate, hc, cpNamespace)
	if err != nil {
		t.Fatalf("ReconcileCredentials failed: %v", err)
	}

	// Verify destination secret was created
	destSecret := &corev1.Secret{}
	err = fakeClient.Get(ctx, client.ObjectKey{
		Namespace: cpNamespace,
		Name:      "oci-credentials",
	}, destSecret)
	if err != nil {
		t.Fatalf("Failed to get synced credentials secret: %v", err)
	}

	// Verify secret data was copied correctly
	if string(destSecret.Data[credentialsConfigKey]) != string(srcSecret.Data[credentialsConfigKey]) {
		t.Errorf("Config data mismatch. Expected %s, got %s",
			srcSecret.Data[credentialsConfigKey], destSecret.Data[credentialsConfigKey])
	}
	if string(destSecret.Data[credentialsKeyKey]) != string(srcSecret.Data[credentialsKeyKey]) {
		t.Errorf("Key data mismatch. Expected %s, got %s",
			srcSecret.Data[credentialsKeyKey], destSecret.Data[credentialsKeyKey])
	}
}

func TestReconcileCAPIInfraCR(t *testing.T) {
	ctx := context.Background()
	ociPlatform := New("")

	hc := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-namespace",
		},
		Spec: hyperv1.HostedClusterSpec{
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.OCIPlatform,
				OCI: &hyperv1.OCIPlatformSpec{
					IdentityRef: hyperv1.OCIIdentityReference{
						Name: "test-creds",
					},
					Region:        "us-sanjose-1",
					CompartmentID: "ocid1.compartment.oc1..aaaaaaaazgovbe2qxduadk3bmj5dobvoe5wnengzavax5pwsfr3bqbdrrcqa",
				},
			},
		},
	}

	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = hyperv1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	createOrUpdate := upsert.New(false)

	// ReconcileCAPIInfraCR should return nil for MVP (no CAPOCI integration)
	infraCR, err := ociPlatform.ReconcileCAPIInfraCR(ctx, fakeClient, createOrUpdate.CreateOrUpdate, hc, "test-cp", hyperv1.APIEndpoint{})
	if err != nil {
		t.Fatalf("ReconcileCAPIInfraCR should not error: %v", err)
	}
	if infraCR != nil {
		t.Errorf("ReconcileCAPIInfraCR should return nil for MVP, got %v", infraCR)
	}
}

func TestReconcileSecretEncryption(t *testing.T) {
	ctx := context.Background()
	ociPlatform := New("")

	hc := &hyperv1.HostedCluster{}
	scheme := runtime.NewScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	createOrUpdate := upsert.New(false)

	// ReconcileSecretEncryption should be no-op for MVP
	err := ociPlatform.ReconcileSecretEncryption(ctx, fakeClient, createOrUpdate.CreateOrUpdate, hc, "test-cp")
	if err != nil {
		t.Fatalf("ReconcileSecretEncryption should not error: %v", err)
	}
}

func TestCAPIProviderDeploymentSpec(t *testing.T) {
	ociPlatform := New("test-image")

	hc := &hyperv1.HostedCluster{}
	hcp := &hyperv1.HostedControlPlane{}

	// CAPIProviderDeploymentSpec should return nil for MVP
	spec, err := ociPlatform.CAPIProviderDeploymentSpec(hc, hcp)
	if err != nil {
		t.Fatalf("CAPIProviderDeploymentSpec should not error: %v", err)
	}
	if spec != nil {
		t.Errorf("CAPIProviderDeploymentSpec should return nil for MVP, got %v", spec)
	}
}

func TestCAPIProviderPolicyRules(t *testing.T) {
	ociPlatform := New("")

	// CAPIProviderPolicyRules should return nil for MVP
	rules := ociPlatform.CAPIProviderPolicyRules()
	if rules != nil {
		t.Errorf("CAPIProviderPolicyRules should return nil for MVP, got %v", rules)
	}
}

func TestDeleteCredentials(t *testing.T) {
	ctx := context.Background()
	cpNamespace := "test-cp-namespace"

	hc := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-namespace",
		},
	}

	// Create existing credentials secret
	existingSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "oci-credentials",
			Namespace: cpNamespace,
		},
	}

	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(existingSecret).
		Build()

	ociPlatform := New("")

	// Test DeleteCredentials
	err := ociPlatform.DeleteCredentials(ctx, fakeClient, hc, cpNamespace)
	if err != nil {
		t.Fatalf("DeleteCredentials failed: %v", err)
	}

	// Verify secret was deleted
	deletedSecret := &corev1.Secret{}
	err = fakeClient.Get(ctx, client.ObjectKey{
		Namespace: cpNamespace,
		Name:      "oci-credentials",
	}, deletedSecret)
	if err == nil {
		t.Error("Secret should have been deleted")
	}
}
