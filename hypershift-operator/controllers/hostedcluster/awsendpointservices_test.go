package hostedcluster

import (
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func invalidAWSCredentialConditions() []metav1.Condition {
	return []metav1.Condition{
		{
			Type:   string(hyperv1.ValidOIDCConfiguration),
			Status: metav1.ConditionTrue,
		},
		{
			Type:   string(hyperv1.ValidAWSIdentityProvider),
			Status: metav1.ConditionFalse,
		},
	}
}

func invalidOIDCCredentialConditions() []metav1.Condition {
	return []metav1.Condition{
		{
			Type:   string(hyperv1.ValidOIDCConfiguration),
			Status: metav1.ConditionFalse,
		},
		{
			Type:   string(hyperv1.ValidAWSIdentityProvider),
			Status: metav1.ConditionTrue,
		},
	}
}

func validAWSCredentialConditions() []metav1.Condition {
	return []metav1.Condition{
		{
			Type:   string(hyperv1.ValidOIDCConfiguration),
			Status: metav1.ConditionTrue,
		},
		{
			Type:   string(hyperv1.ValidAWSIdentityProvider),
			Status: metav1.ConditionTrue,
		},
	}
}

func TestDeleteAWSEndpointServicesFinalizerHandling(t *testing.T) {
	cpoFinalizer := "hypershift.openshift.io/control-plane-operator-finalizer"
	namespace := "test-ns"
	oldDeletionTimestamp := metav1.NewTime(time.Now().Add(-20 * time.Minute))
	recentDeletionTimestamp := metav1.NewTime(time.Now().Add(-1 * time.Minute))

	tests := []struct {
		name               string
		conditions         []metav1.Condition
		deletionTimestamp  metav1.Time
		expectFinalizerSet bool
	}{
		{
			name:               "When credentials are valid and within grace period it should keep the CPO finalizer",
			conditions:         validAWSCredentialConditions(),
			deletionTimestamp:  recentDeletionTimestamp,
			expectFinalizerSet: true,
		},
		{
			name:               "When credentials are valid but grace period expired it should remove the CPO finalizer",
			conditions:         validAWSCredentialConditions(),
			deletionTimestamp:  oldDeletionTimestamp,
			expectFinalizerSet: false,
		},
		{
			name:               "When credentials are invalid it should remove the CPO finalizer",
			conditions:         invalidAWSCredentialConditions(),
			deletionTimestamp:  recentDeletionTimestamp,
			expectFinalizerSet: false,
		},
		{
			name:               "When credentials are unknown and within grace period it should keep the CPO finalizer",
			conditions:         []metav1.Condition{},
			deletionTimestamp:  recentDeletionTimestamp,
			expectFinalizerSet: true,
		},
		{
			name:               "When credentials are unknown and past grace period it should remove the CPO finalizer",
			conditions:         []metav1.Condition{},
			deletionTimestamp:  oldDeletionTimestamp,
			expectFinalizerSet: false,
		},
		{
			name:               "When OIDC configuration is invalid it should remove the CPO finalizer",
			conditions:         invalidOIDCCredentialConditions(),
			deletionTimestamp:  recentDeletionTimestamp,
			expectFinalizerSet: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			ctx := ctrl.LoggerInto(t.Context(), ctrl.Log.WithName("test"))

			ep := &hyperv1.AWSEndpointService{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "test-ep",
					Namespace:         namespace,
					DeletionTimestamp: &tc.deletionTimestamp,
					Finalizers:        []string{cpoFinalizer},
				},
			}

			hc := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hc",
					Namespace: "clusters",
				},
				Status: hyperv1.HostedClusterStatus{
					Conditions: tc.conditions,
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithObjects(ep).
				Build()

			_, err := deleteAWSEndpointServices(ctx, fakeClient, hc, namespace)
			g.Expect(err).ToNot(HaveOccurred())

			updatedEp := &hyperv1.AWSEndpointService{}
			getErr := fakeClient.Get(ctx, crclient.ObjectKeyFromObject(ep), updatedEp)
			if tc.expectFinalizerSet {
				g.Expect(getErr).ToNot(HaveOccurred())
				g.Expect(controllerutil.ContainsFinalizer(updatedEp, cpoFinalizer)).To(BeTrue())
			} else {
				// When the finalizer is removed on an object with DeletionTimestamp,
				// the fake client garbage-collects the object.
				g.Expect(getErr).To(HaveOccurred(), "object should have been deleted after finalizer removal")
			}
		})
	}
}
