package karpenter

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/upsert"

	awskarpenterv1 "github.com/aws/karpenter-provider-aws/pkg/apis/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	scheme.AddKnownTypeWithName(
		schema.GroupVersionKind{
			Group:   "karpenter.k8s.aws",
			Version: "v1",
			Kind:    "EC2NodeClass",
		}, &awskarpenterv1.EC2NodeClass{})

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
			got := &awskarpenterv1.EC2NodeClass{}
			err = fakeClient.Get(context.Background(), types.NamespacedName{Name: "default"}, got)
			if err != nil {
				t.Errorf("failed to get EC2NodeClass: %v", err)
				return
			}

			// Verify basic fields
			g.Expect(got.Spec.Role).To(Equal("KarpenterNodeRole-agl"), "role = %v, want KarpenterNodeRole-agl", got.Spec.Role)
			g.Expect(got.Spec.UserData).To(HaveValue(Equal("test-userdata")), "userData = %v, want test-userdata", got.Spec.UserData)
			g.Expect(got.Spec.AMIFamily).To(HaveValue(Equal("Custom")), "amiFamily = %v, want Custom", got.Spec.AMIFamily)

			// Verify amiSelectorTerms
			g.Expect(len(got.Spec.AMISelectorTerms)).To(Equal(1), "amiSelectorTerms should have exactly one element")
			g.Expect(got.Spec.AMISelectorTerms[0].ID).To(Equal("ami-123"), "unexpected amiSelectorTerms: %v", got.Spec.AMISelectorTerms)

			// Verify selector terms have correct tags
			expectedTags := map[string]string{
				"karpenter.sh/discovery": "test-infra",
			}

			g.Expect(len(got.Spec.SubnetSelectorTerms)).To(Equal(1), "SubnetSelectorTerms should have exactly one element for field")
			g.Expect(got.Spec.SubnetSelectorTerms[0].Tags).To(Equal(expectedTags), "SubnetSelectorTerms tags = %v, want %v", got.Spec.SubnetSelectorTerms[0].Tags, expectedTags)

			g.Expect(len(got.Spec.SecurityGroupSelectorTerms)).To(Equal(1), "SecurityGroupSelectorTerms should have exactly one element for field")
			g.Expect(got.Spec.SecurityGroupSelectorTerms[0].Tags).To(Equal(expectedTags), "SecurityGroupSelectorTerms tags = %v, want %v", got.Spec.SecurityGroupSelectorTerms[0].Tags, expectedTags)
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
