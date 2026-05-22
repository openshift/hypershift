package hcpstatus

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"

	configv1 "github.com/openshift/api/config/v1"

	"k8s.io/apimachinery/pkg/api/meta"
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
		validateConditions   func(g Gomega, hcp *hyperv1.HostedControlPlane)
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
		{
			name: "When ClusterVersion has conditions it should propagate them to HCP",
			hostedClusterObjects: []crclient.Object{
				&configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{Name: "version"},
					Status: configv1.ClusterVersionStatus{
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{
								Type:    configv1.OperatorAvailable,
								Status:  configv1.ConditionTrue,
								Reason:  "AsExpected",
								Message: "cluster is available",
							},
							{
								Type:    configv1.OperatorProgressing,
								Status:  configv1.ConditionFalse,
								Reason:  "AsExpected",
								Message: "cluster is not progressing",
							},
						},
					},
				},
				&configv1.Authentication{
					ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
					Status: configv1.AuthenticationStatus{
						IntegratedOAuthMetadata: configv1.ConfigMapNameReference{Name: expectedOAuthConfigMapName},
					},
				},
			},
			expectedOAuthName: expectedOAuthConfigMapName,
			validateConditions: func(g Gomega, hcp *hyperv1.HostedControlPlane) {
				availableCond := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.ClusterVersionAvailable))
				g.Expect(availableCond).NotTo(BeNil())
				g.Expect(availableCond.Status).To(Equal(metav1.ConditionTrue))
				g.Expect(availableCond.Reason).To(Equal("AsExpected"))

				progressingCond := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.ClusterVersionProgressing))
				g.Expect(progressingCond).NotTo(BeNil())
				g.Expect(progressingCond.Status).To(Equal(metav1.ConditionFalse))
			},
		},
		{
			name: "When ClusterVersion has no Upgradeable condition it should default to True",
			hostedClusterObjects: []crclient.Object{
				&configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{Name: "version"},
					Status: configv1.ClusterVersionStatus{
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{
								Type:   configv1.OperatorAvailable,
								Status: configv1.ConditionTrue,
								Reason: "AsExpected",
							},
						},
					},
				},
				&configv1.Authentication{
					ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
					Status: configv1.AuthenticationStatus{
						IntegratedOAuthMetadata: configv1.ConfigMapNameReference{Name: expectedOAuthConfigMapName},
					},
				},
			},
			expectedOAuthName: expectedOAuthConfigMapName,
			validateConditions: func(g Gomega, hcp *hyperv1.HostedControlPlane) {
				upgradeableCond := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.ClusterVersionUpgradeable))
				g.Expect(upgradeableCond).NotTo(BeNil())
				g.Expect(upgradeableCond.Status).To(Equal(metav1.ConditionTrue))
				g.Expect(upgradeableCond.Reason).To(Equal(hyperv1.FromClusterVersionReason))
			},
		},
		{
			name: "When CVO condition has empty Reason it should use FromClusterVersionReason",
			hostedClusterObjects: []crclient.Object{
				&configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{Name: "version"},
					Status: configv1.ClusterVersionStatus{
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{
								Type:    configv1.OperatorAvailable,
								Status:  configv1.ConditionTrue,
								Reason:  "",
								Message: "all good",
							},
						},
					},
				},
				&configv1.Authentication{
					ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
					Status: configv1.AuthenticationStatus{
						IntegratedOAuthMetadata: configv1.ConfigMapNameReference{Name: expectedOAuthConfigMapName},
					},
				},
			},
			expectedOAuthName: expectedOAuthConfigMapName,
			validateConditions: func(g Gomega, hcp *hyperv1.HostedControlPlane) {
				availableCond := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.ClusterVersionAvailable))
				g.Expect(availableCond).NotTo(BeNil())
				g.Expect(availableCond.Reason).To(Equal(hyperv1.FromClusterVersionReason))
			},
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

			if tt.validateConditions != nil {
				tt.validateConditions(g, updatedHCP)
			}
		})
	}
}
