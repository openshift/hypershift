package storage

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	component "github.com/openshift/hypershift/support/controlplane-component"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestEnforceOrderingUntilCSOExists(t *testing.T) {
	namespace := "test-namespace"

	t.Run("When the CSO workload does not exist, it should enforce the precondition", func(t *testing.T) {
		g := NewWithT(t)

		fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).Build()
		cpContext := component.WorkloadContext{
			Context: t.Context(),
			Client:  fakeClient,
			HCP: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Namespace: namespace},
			},
		}

		enforce, err := enforceOrderingUntilCSOExists(cpContext)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(enforce).To(BeTrue())
	})

	t.Run("When the CSO workload already exists, it should skip the precondition", func(t *testing.T) {
		g := NewWithT(t)

		existing := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      ComponentName,
				Namespace: namespace,
			},
		}
		fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(existing).Build()
		cpContext := component.WorkloadContext{
			Context: t.Context(),
			Client:  fakeClient,
			HCP: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Namespace: namespace},
			},
		}

		enforce, err := enforceOrderingUntilCSOExists(cpContext)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(enforce).To(BeFalse())
	})
}
