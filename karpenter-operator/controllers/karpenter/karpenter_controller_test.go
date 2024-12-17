package karpenter

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/upsert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestReconcileEC2NodeClassDefault(t *testing.T) {
	scheme := runtime.NewScheme()
	// _ = corev1.AddToScheme(scheme)
	_ = hyperv1.AddToScheme(scheme)

	// Register the EC2NodeClass GVK in the scheme
	ec2NodeClassGVK := schema.GroupVersionKind{
		Group:   "karpenter.k8s.aws",
		Version: "v1",
		Kind:    "EC2NodeClass",
	}
	scheme.AddKnownTypeWithName(ec2NodeClassGVK, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(
		schema.GroupVersionKind{
			Group:   "karpenter.k8s.aws",
			Version: "v1",
			Kind:    "EC2NodeClassList",
		},
		&unstructured.UnstructuredList{},
	)

	testCases := []struct {
		name           string
		userDataSecret *corev1.Secret
		hcp            *hyperv1.HostedControlPlane
		wantErr        bool
	}{
		{
			name: "When no errors it should create the default EC2NodeClass",
			userDataSecret: &corev1.Secret{
				Data: map[string][]byte{
					"value": []byte("test-userdata"),
				},
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						userDataAMILabel: "ami-123",
					},
				},
			},
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					InfraID: "test-infra",
				},
			},
			wantErr: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				Build()

			r := &Reconciler{
				GuestClient:            fakeClient,
				CreateOrUpdateProvider: upsert.New(false),
			}

			err := r.reconcileEC2NodeClassDefault(context.Background(), tc.userDataSecret, tc.hcp)
			if (err != nil) != tc.wantErr {
				t.Errorf("reconcileEC2NodeClassDefault() error = %v, wantErr %v", err, tc.wantErr)
				return
			}

			// Verify the EC2NodeClass was created.
			got := &unstructured.Unstructured{}
			got.SetGroupVersionKind(ec2NodeClassGVK)

			err = fakeClient.Get(context.Background(), types.NamespacedName{Name: "default"}, got)
			if err != nil {
				t.Errorf("failed to get EC2NodeClass: %v", err)
				return
			}

			spec, ok := got.Object["spec"].(map[string]interface{})
			if !ok {
				t.Fatal("spec is not a map")
			}

			// Verify basic fields
			g.Expect(spec["role"]).To(Equal("KarpenterNodeRole-agl"), "role = %v, want KarpenterNodeRole-agl", spec["role"])
			g.Expect(spec["userData"]).To(Equal("test-userdata"), "userData = %v, want test-userdata", spec["userData"])
			g.Expect(spec["amiFamily"]).To(Equal("Custom"), "amiFamily = %v, want Custom", spec["amiFamily"])

			// Verify amiSelectorTerms
			amiTerms, ok := spec["amiSelectorTerms"].([]interface{})
			g.Expect(ok).To(BeTrue(), "amiSelectorTerms should be a slice")
			g.Expect(len(amiTerms)).To(Equal(1), "amiSelectorTerms should have exactly one element")

			amiTerm, ok := amiTerms[0].(map[string]interface{})
			g.Expect(ok).To(BeTrue(), "amiTerm should be a map")
			g.Expect(amiTerm["id"]).To(Equal("ami-123"), "unexpected amiSelectorTerms: %v", amiTerms)

			// Verify selector terms have correct tags
			expectedTag := map[string]interface{}{
				"karpenter.sh/discovery": "test-infra",
			}

			// Helper function to verify selector terms
			verifySelectorTerms := func(field string, expectedTags map[string]interface{}) {
				terms, ok := spec[field].([]interface{})
				g.Expect(ok).To(BeTrue(), "terms should be a slice for field %s", field)
				g.Expect(len(terms)).To(Equal(1), "terms should have exactly one element for field %s", field)

				term, ok := terms[0].(map[string]interface{})
				g.Expect(ok).To(BeTrue(), "term should be a map for field %s", field)

				tags, ok := term["tags"].(map[string]interface{})
				g.Expect(ok).To(BeTrue(), "tags should be a map for field %s", field)
				g.Expect(tags).To(Equal(expectedTags), "%s tags = %v, want %v", field, tags, expectedTags)
			}

			verifySelectorTerms("subnetSelectorTerms", expectedTag)
			verifySelectorTerms("securityGroupSelectorTerms", expectedTag)
		})
	}
}

func TestGetUserDataSecret(t *testing.T) {
	g := NewWithT(t)

	scheme := runtime.NewScheme()
	g.Expect(corev1.AddToScheme(scheme)).To(Succeed())

	testCases := []struct {
		name          string
		namespace     string
		hcp           *hyperv1.HostedControlPlane
		objects       []client.Object
		expectedError string
	}{
		{
			name:      "when multiple exist it should return newest secret",
			namespace: "test-namespace",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-hcp",
				},
			},
			objects: []client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "older-secret",
						Namespace:         "test-namespace",
						CreationTimestamp: metav1.Time{Time: time.Now().Add(-1 * time.Hour)},
						Labels: map[string]string{
							hyperv1.NodePoolLabel: "test-hcp-karpenter",
						},
					},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "newer-secret",
						Namespace:         "test-namespace",
						CreationTimestamp: metav1.Time{Time: time.Now()},
						Labels: map[string]string{
							hyperv1.NodePoolLabel: "test-hcp-karpenter",
						},
					},
				},
			},
		},
		{
			name:      "when no secrets exist it should return error",
			namespace: "test-namespace",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-hcp",
				},
			},
			objects:       []client.Object{},
			expectedError: "expected 1 secret, got 0",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tc.objects...).
				Build()

			r := &Reconciler{
				ManagementClient: fakeClient,
				Namespace:        tc.namespace,
			}

			secret, err := r.getUserDataSecret(context.Background(), tc.hcp)

			if tc.expectedError != "" {
				g.Expect(err).To(MatchError(tc.expectedError))
				g.Expect(secret).To(BeNil())
				return
			}

			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(secret).NotTo(BeNil())

			g.Expect(secret.Name).To(Equal("newer-secret"))
		})
	}
}
