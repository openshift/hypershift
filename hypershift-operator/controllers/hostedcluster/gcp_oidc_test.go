package hostedcluster

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/gcpapi"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type fakeGCSClient struct {
	uploaded    map[string][]byte
	deleted     map[string]bool
	uploadErr   error
	deleteErr   error
	uploadCalls int
	deleteCalls int
}

// gcsClientOrNil returns nil GCSAPI interface when client is nil, avoiding the
// non-nil interface wrapping a nil pointer pitfall.
func gcsClientOrNil(c *fakeGCSClient) gcpapi.GCSAPI {
	if c == nil {
		return nil
	}
	return c
}

func newFakeGCSClient() *fakeGCSClient {
	return &fakeGCSClient{
		uploaded: make(map[string][]byte),
		deleted:  make(map[string]bool),
	}
}

func (f *fakeGCSClient) UploadObject(_ context.Context, bucket, objectName string, content io.Reader) error {
	f.uploadCalls++
	if f.uploadErr != nil {
		return f.uploadErr
	}
	data, _ := io.ReadAll(content)
	f.uploaded[bucket+"/"+objectName] = data
	return nil
}

func (f *fakeGCSClient) DeleteObject(_ context.Context, bucket, objectName string) error {
	f.deleteCalls++
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleted[bucket+"/"+objectName] = true
	return nil
}

func testRSAPublicKeyPEM(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}
	pubKeyDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("failed to marshal public key: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PUBLIC KEY",
		Bytes: pubKeyDER,
	})
}

func TestReconcileGCPOIDCDocuments(t *testing.T) {
	pubKeyPEM := testRSAPublicKeyPEM(t)

	tests := []struct {
		name            string
		hcluster        *hyperv1.HostedCluster
		hcp             *hyperv1.HostedControlPlane
		secret          *corev1.Secret
		gcsClient       *fakeGCSClient
		bucketName      string
		expectErr       bool
		expectErrMsg    string
		expectUploads   int
		expectFinalizer bool
	}{
		{
			name: "When ServiceAccountSigningKey is set it should skip reconciliation",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "clusters"},
				Spec: hyperv1.HostedClusterSpec{
					InfraID:                  "test-infra",
					ServiceAccountSigningKey: &corev1.LocalObjectReference{Name: "custom-key"},
				},
			},
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "clusters-test"},
				Spec:       hyperv1.HostedControlPlaneSpec{IssuerURL: "https://example.com"},
			},
			gcsClient:       newFakeGCSClient(),
			bucketName:      "my-bucket",
			expectUploads:   0,
			expectFinalizer: false,
		},
		{
			name: "When finalizer already exists it should skip re-uploading documents",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test",
					Namespace:  "clusters",
					Finalizers: []string{gcpOIDCDocumentsFinalizer},
				},
				Spec: hyperv1.HostedClusterSpec{InfraID: "test-infra"},
			},
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "clusters-test"},
				Spec:       hyperv1.HostedControlPlaneSpec{IssuerURL: "https://example.com"},
			},
			gcsClient:       newFakeGCSClient(),
			bucketName:      "my-bucket",
			expectUploads:   0,
			expectFinalizer: true,
		},
		{
			name: "When GCS client is nil it should return an error",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "clusters"},
				Spec:       hyperv1.HostedClusterSpec{InfraID: "test-infra"},
			},
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "clusters-test"},
				Spec:       hyperv1.HostedControlPlaneSpec{IssuerURL: "https://example.com"},
			},
			gcsClient:    nil,
			bucketName:   "",
			expectErr:    true,
			expectErrMsg: "not configured with a GCS bucket or credentials",
		},
		{
			name: "When bucket name is empty it should return an error",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "clusters"},
				Spec:       hyperv1.HostedClusterSpec{InfraID: "test-infra"},
			},
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "clusters-test"},
				Spec:       hyperv1.HostedControlPlaneSpec{IssuerURL: "https://example.com"},
			},
			gcsClient:    newFakeGCSClient(),
			bucketName:   "",
			expectErr:    true,
			expectErrMsg: "not configured with a GCS bucket or credentials",
		},
		{
			name: "When sa-signing-key secret is not found it should return nil and retry later",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "clusters"},
				Spec:       hyperv1.HostedClusterSpec{InfraID: "test-infra"},
			},
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "clusters-test"},
				Spec:       hyperv1.HostedControlPlaneSpec{IssuerURL: "https://example.com"},
			},
			gcsClient:       newFakeGCSClient(),
			bucketName:      "my-bucket",
			expectUploads:   0,
			expectFinalizer: false,
		},
		{
			name: "When sa-signing-key secret is missing the public key it should return an error",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "clusters"},
				Spec:       hyperv1.HostedClusterSpec{InfraID: "test-infra"},
			},
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "clusters-test"},
				Spec:       hyperv1.HostedControlPlaneSpec{IssuerURL: "https://example.com"},
			},
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: serviceAccountSigningKeySecret, Namespace: "clusters-test"},
				Data:       map[string][]byte{"wrong-key": []byte("data")},
			},
			gcsClient:    newFakeGCSClient(),
			bucketName:   "my-bucket",
			expectErr:    true,
			expectErrMsg: "missing required key",
		},
		{
			name: "When all prerequisites are met it should upload OIDC documents and add finalizer",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "clusters"},
				Spec:       hyperv1.HostedClusterSpec{InfraID: "test-infra"},
			},
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "clusters-test"},
				Spec:       hyperv1.HostedControlPlaneSpec{IssuerURL: "https://example.com"},
			},
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: serviceAccountSigningKeySecret, Namespace: "clusters-test"},
				Data:       map[string][]byte{serviceSignerPublicKey: pubKeyPEM},
			},
			gcsClient:       newFakeGCSClient(),
			bucketName:      "my-bucket",
			expectUploads:   2,
			expectFinalizer: true,
		},
		{
			name: "When GCS upload fails it should return an error",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "clusters"},
				Spec:       hyperv1.HostedClusterSpec{InfraID: "test-infra"},
			},
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "clusters-test"},
				Spec:       hyperv1.HostedControlPlaneSpec{IssuerURL: "https://example.com"},
			},
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: serviceAccountSigningKeySecret, Namespace: "clusters-test"},
				Data:       map[string][]byte{serviceSignerPublicKey: pubKeyPEM},
			},
			gcsClient: func() *fakeGCSClient {
				c := newFakeGCSClient()
				c.uploadErr = fmt.Errorf("simulated GCS error")
				return c
			}(),
			bucketName:   "my-bucket",
			expectErr:    true,
			expectErrMsg: "failed to upload",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			objects := []crclient.Object{tc.hcluster}
			if tc.secret != nil {
				objects = append(objects, tc.secret)
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithObjects(objects...).
				Build()

			r := &HostedClusterReconciler{
				Client:                   fakeClient,
				GCSClient:                gcsClientOrNil(tc.gcsClient),
				GCPOIDCStorageBucketName: tc.bucketName,
			}

			err := r.reconcileGCPOIDCDocuments(t.Context(), ctrl.Log, tc.hcluster, tc.hcp)

			if tc.expectErr {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tc.expectErrMsg))
				return
			}

			g.Expect(err).ToNot(HaveOccurred())

			if tc.gcsClient != nil {
				g.Expect(tc.gcsClient.uploadCalls).To(Equal(tc.expectUploads))
			}

			updatedHC := &hyperv1.HostedCluster{}
			g.Expect(fakeClient.Get(t.Context(), crclient.ObjectKeyFromObject(tc.hcluster), updatedHC)).To(Succeed())

			if tc.expectFinalizer {
				g.Expect(updatedHC.Finalizers).To(ContainElement(gcpOIDCDocumentsFinalizer))
			} else {
				g.Expect(updatedHC.Finalizers).ToNot(ContainElement(gcpOIDCDocumentsFinalizer))
			}
		})
	}
}

func TestCleanupGCPOIDCBucketData(t *testing.T) {
	tests := []struct {
		name            string
		hcluster        *hyperv1.HostedCluster
		gcsClient       *fakeGCSClient
		bucketName      string
		expectErr       bool
		expectErrMsg    string
		expectDeletes   int
		expectFinalizer bool
	}{
		{
			name: "When finalizer is not present it should skip cleanup",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "clusters"},
				Spec:       hyperv1.HostedClusterSpec{InfraID: "test-infra"},
			},
			gcsClient:       newFakeGCSClient(),
			bucketName:      "my-bucket",
			expectDeletes:   0,
			expectFinalizer: false,
		},
		{
			name: "When GCS client is nil it should return an error",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test",
					Namespace:  "clusters",
					Finalizers: []string{gcpOIDCDocumentsFinalizer},
				},
				Spec: hyperv1.HostedClusterSpec{InfraID: "test-infra"},
			},
			gcsClient:       nil,
			bucketName:      "my-bucket",
			expectErr:       true,
			expectErrMsg:    "not configured with a GCS bucket",
			expectFinalizer: true,
		},
		{
			name: "When cleanup succeeds it should delete objects and remove finalizer",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test",
					Namespace:  "clusters",
					Finalizers: []string{gcpOIDCDocumentsFinalizer},
				},
				Spec: hyperv1.HostedClusterSpec{InfraID: "test-infra"},
			},
			gcsClient:       newFakeGCSClient(),
			bucketName:      "my-bucket",
			expectDeletes:   2,
			expectFinalizer: false,
		},
		{
			name: "When GCS delete fails it should return an error and keep finalizer",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test",
					Namespace:  "clusters",
					Finalizers: []string{gcpOIDCDocumentsFinalizer},
				},
				Spec: hyperv1.HostedClusterSpec{InfraID: "test-infra"},
			},
			gcsClient: func() *fakeGCSClient {
				c := newFakeGCSClient()
				c.deleteErr = fmt.Errorf("simulated delete error")
				return c
			}(),
			bucketName:      "my-bucket",
			expectErr:       true,
			expectErrMsg:    "failed to delete",
			expectDeletes:   2,
			expectFinalizer: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			fakeClient := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithObjects(tc.hcluster).
				Build()

			r := &HostedClusterReconciler{
				Client:                   fakeClient,
				GCSClient:                gcsClientOrNil(tc.gcsClient),
				GCPOIDCStorageBucketName: tc.bucketName,
			}

			err := r.cleanupGCPOIDCBucketData(t.Context(), ctrl.Log, tc.hcluster)

			if tc.expectErr {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tc.expectErrMsg))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}

			if tc.gcsClient != nil {
				g.Expect(tc.gcsClient.deleteCalls).To(Equal(tc.expectDeletes))
			}

			updatedHC := &hyperv1.HostedCluster{}
			g.Expect(fakeClient.Get(t.Context(), crclient.ObjectKeyFromObject(tc.hcluster), updatedHC)).To(Succeed())

			if tc.expectFinalizer {
				g.Expect(updatedHC.Finalizers).To(ContainElement(gcpOIDCDocumentsFinalizer))
			} else {
				g.Expect(updatedHC.Finalizers).ToNot(ContainElement(gcpOIDCDocumentsFinalizer))
			}
		})
	}
}
