package hostedcluster

import (
	"context"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func TestDeleteAWSEndpointServicesFinalizerHandling(t *testing.T) {
	t.Parallel()

	namespace := "control-plane-namespace"
	cpoFinalizer := "hypershift.openshift.io/control-plane-operator-finalizer"
	now := time.Now()
	oldDeletionTimestamp := metav1.NewTime(now.Add(-(awsEndpointDeletionGracePeriod + time.Minute)))
	recentDeletionTimestamp := metav1.NewTime(now.Add(-1 * time.Minute))

	tests := []struct {
		name               string
		conditions         []metav1.Condition
		deletionTimestamp  metav1.Time
		expectFinalizerSet bool
	}{
		{
			name:               "When credentials are valid it should keep the CPO finalizer even after grace period",
			conditions:         validAWSCredentialConditions(),
			deletionTimestamp:  oldDeletionTimestamp,
			expectFinalizerSet: true,
		},
		{
			name:               "When credentials are invalid it should keep the CPO finalizer before grace period",
			conditions:         invalidAWSCredentialConditions(),
			deletionTimestamp:  recentDeletionTimestamp,
			expectFinalizerSet: true,
		},
		{
			name:               "When credentials are invalid it should remove the CPO finalizer after grace period",
			conditions:         invalidAWSCredentialConditions(),
			deletionTimestamp:  oldDeletionTimestamp,
			expectFinalizerSet: false,
		},
		{
			name:               "When credentials are unknown it should keep the CPO finalizer even after grace period",
			conditions:         unknownAWSCredentialConditions(),
			deletionTimestamp:  oldDeletionTimestamp,
			expectFinalizerSet: true,
		},
		{
			name:               "When credential conditions are missing it should keep the CPO finalizer even after grace period",
			conditions:         []metav1.Condition{},
			deletionTimestamp:  oldDeletionTimestamp,
			expectFinalizerSet: true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ep := &hyperv1.AWSEndpointService{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "private-router",
					Namespace:         namespace,
					Finalizers:        []string{cpoFinalizer},
					DeletionTimestamp: &tc.deletionTimestamp,
				},
				Status: hyperv1.AWSEndpointServiceStatus{
					EndpointID: "vpce-12345",
				},
			}

			hc := &hyperv1.HostedCluster{
				Status: hyperv1.HostedClusterStatus{
					Conditions: tc.conditions,
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithObjects(ep).
				Build()

			exists, err := deleteAWSEndpointServices(context.Background(), fakeClient, hc, namespace)
			if err != nil {
				t.Fatalf("deleteAWSEndpointServices() returned unexpected error: %v", err)
			}
			if !exists {
				t.Fatalf("deleteAWSEndpointServices() expected exists=true while endpoint service still exists")
			}

			current := &hyperv1.AWSEndpointService{}
			if err := fakeClient.Get(context.Background(), ctrlclient.ObjectKeyFromObject(ep), current); err != nil {
				if tc.expectFinalizerSet || !apierrors.IsNotFound(err) {
					t.Fatalf("failed getting updated awsendpointservice: %v", err)
				}
				// The object may be deleted once the finalizer is removed.
				return
			}

			gotFinalizerSet := controllerutil.ContainsFinalizer(current, cpoFinalizer)
			if gotFinalizerSet != tc.expectFinalizerSet {
				t.Fatalf("expected finalizer present=%t, got %t", tc.expectFinalizerSet, gotFinalizerSet)
			}
		})
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

func unknownAWSCredentialConditions() []metav1.Condition {
	return []metav1.Condition{
		{
			Type:   string(hyperv1.ValidOIDCConfiguration),
			Status: metav1.ConditionTrue,
		},
		{
			Type:   string(hyperv1.ValidAWSIdentityProvider),
			Status: metav1.ConditionUnknown,
		},
	}
}
