package hostedcluster

import (
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func hostedClusterWithCredentialConditions(oidcStatus, awsIdpStatus metav1.ConditionStatus) *hyperv1.HostedCluster {
	hc := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "clusters"},
	}
	meta.SetStatusCondition(&hc.Status.Conditions, metav1.Condition{
		Type:   string(hyperv1.ValidOIDCConfiguration),
		Status: oidcStatus,
		Reason: "test",
	})
	meta.SetStatusCondition(&hc.Status.Conditions, metav1.Condition{
		Type:   string(hyperv1.ValidAWSIdentityProvider),
		Status: awsIdpStatus,
		Reason: "test",
	})
	return hc
}

func TestDeleteAWSEndpointServices(t *testing.T) {
	cpoFinalizer := "hypershift.openshift.io/control-plane-operator-finalizer"
	namespace := "clusters-test"

	tests := []struct {
		name                   string
		hc                     *hyperv1.HostedCluster
		endpoints              []hyperv1.AWSEndpointService
		expectErr              bool
		expectPending          bool
		expectFinalizerRemoved bool
	}{
		{
			name: "When endpoint is deleting with invalid creds, it should remove CPO finalizer",
			hc:   hostedClusterWithCredentialConditions(metav1.ConditionFalse, metav1.ConditionTrue),
			endpoints: []hyperv1.AWSEndpointService{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "ep-1",
						Namespace:         namespace,
						DeletionTimestamp: &metav1.Time{Time: time.Now().Add(-1 * time.Minute)},
						Finalizers:        []string{cpoFinalizer},
					},
				},
			},
			expectPending:          true,
			expectFinalizerRemoved: true,
		},
		{
			name: "When endpoint is deleting with valid creds past grace period, it should remove CPO finalizer",
			hc:   hostedClusterWithCredentialConditions(metav1.ConditionTrue, metav1.ConditionTrue),
			endpoints: []hyperv1.AWSEndpointService{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "ep-1",
						Namespace:         namespace,
						DeletionTimestamp: &metav1.Time{Time: time.Now().Add(-15 * time.Minute)},
						Finalizers:        []string{cpoFinalizer},
					},
				},
			},
			expectPending:          true,
			expectFinalizerRemoved: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			objects := []crclient.Object{}
			for i := range tc.endpoints {
				objects = append(objects, &tc.endpoints[i])
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithObjects(objects...).
				Build()

			pending, err := deleteAWSEndpointServices(t.Context(), fakeClient, tc.hc, namespace)

			if tc.expectErr {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}

			g.Expect(pending).To(Equal(tc.expectPending))

			if tc.expectFinalizerRemoved {
				for _, ep := range tc.endpoints {
					updatedEP := &hyperv1.AWSEndpointService{}
					err := fakeClient.Get(t.Context(), crclient.ObjectKey{
						Namespace: ep.Namespace,
						Name:      ep.Name,
					}, updatedEP)
					if apierrors.IsNotFound(err) {
						continue
					}
					g.Expect(err).ToNot(HaveOccurred())
					g.Expect(updatedEP.Finalizers).ToNot(ContainElement(cpoFinalizer))
				}
			}
		})
	}
}
