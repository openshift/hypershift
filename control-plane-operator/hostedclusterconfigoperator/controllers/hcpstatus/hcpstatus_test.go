package hcpstatus

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"

	configv1 "github.com/openshift/api/config/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestHCPStatusReconciler(t *testing.T) {
	t.Parallel()

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hcp",
			Namespace: "test-ns",
		},
	}

	expectedOAuthConfigMapName := "oauth-metadata-configmap"

	tests := []struct {
		name                 string
		hostedClusterObjects []crclient.Object
		expectError          bool
		expectedOAuthName    string
	}{
		{
			name: "When Authentication resource exists it should propagate status to HCP",
			hostedClusterObjects: []crclient.Object{
				&configv1.ClusterVersion{ObjectMeta: metav1.ObjectMeta{Name: "version"}},
				&configv1.Authentication{
					ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
					Status: configv1.AuthenticationStatus{
						IntegratedOAuthMetadata: configv1.ConfigMapNameReference{
							Name: expectedOAuthConfigMapName,
						},
					},
				},
			},
			expectedOAuthName: expectedOAuthConfigMapName,
		},
		{
			name: "When Authentication resource is missing it should return an error",
			hostedClusterObjects: []crclient.Object{
				&configv1.ClusterVersion{ObjectMeta: metav1.ObjectMeta{Name: "version"}},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			mgmtClient := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithObjects(hcp.DeepCopy()).
				WithStatusSubresource(&hyperv1.HostedControlPlane{}).
				Build()

			hostedClusterClient := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithObjects(tt.hostedClusterObjects...).
				Build()

			reconciler := &hcpStatusReconciler{
				mgtClusterClient:    mgmtClient,
				hostedClusterClient: hostedClusterClient,
			}

			_, err := reconciler.Reconcile(t.Context(), reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      hcp.Name,
					Namespace: hcp.Namespace,
				},
			})

			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("Authentication"),
					"error should be about missing Authentication resource, got: %v", err)
				return
			}

			g.Expect(err).NotTo(HaveOccurred())
			updatedHCP := &hyperv1.HostedControlPlane{}
			g.Expect(mgmtClient.Get(t.Context(), crclient.ObjectKeyFromObject(hcp), updatedHCP)).To(Succeed())
			g.Expect(updatedHCP.Status.Configuration).NotTo(BeNil())
			g.Expect(updatedHCP.Status.Configuration.Authentication.IntegratedOAuthMetadata.Name).To(Equal(tt.expectedOAuthName))
		})
	}
}
