package storage

import (
	"context"
	"errors"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	oapiv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/oapi"
	"github.com/openshift/hypershift/support/api"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/support/upsert"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apiMeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
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

	t.Run("When the client returns an error, it should propagate the error and not enforce the precondition", func(t *testing.T) {
		g := NewWithT(t)

		getErr := errors.New("boom")
		fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithInterceptorFuncs(interceptor.Funcs{
			Get: func(_ context.Context, _ client.WithWatch, _ client.ObjectKey, _ client.Object, _ ...client.GetOption) error {
				return getErr
			},
		}).Build()
		cpContext := component.WorkloadContext{
			Context: t.Context(),
			Client:  fakeClient,
			HCP: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Namespace: namespace},
			},
		}

		enforce, err := enforceOrderingUntilCSOExists(cpContext)
		g.Expect(err).To(MatchError(getErr))
		g.Expect(enforce).To(BeFalse())
	})
}

func TestNewComponent(t *testing.T) {
	g := NewWithT(t)
	namespace := "test-namespace"

	// The CSO gates its initial rollout on KubeAPIServerAvailable so the
	// service-network-admin-kubeconfig secret (a KAS component manifest)
	// exists before the CSI driver operands start mounting it.
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace},
		Spec: hyperv1.HostedControlPlaneSpec{
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.AWSPlatform,
			},
		},
	}
	apiMeta.SetStatusCondition(&hcp.Status.Conditions, metav1.Condition{
		Type:   string(hyperv1.ConfigOperatorReconciliationSucceeded),
		Status: metav1.ConditionTrue,
		Reason: hyperv1.AsExpectedReason,
	})

	imageProvider := testutil.FakeImageProvider(testutil.WithVersion("4.22.0"))
	availableComponent := func(name string) *hyperv1.ControlPlaneComponent {
		return &hyperv1.ControlPlaneComponent{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
			},
			Status: hyperv1.ControlPlaneComponentStatus{
				Version: imageProvider.Version(),
				Conditions: []metav1.Condition{
					{Type: string(hyperv1.ControlPlaneComponentAvailable), Status: metav1.ConditionTrue},
					{Type: string(hyperv1.ControlPlaneComponentRolloutComplete), Status: metav1.ConditionTrue},
				},
			},
		}
	}
	// Satisfy the component's dependencies (openshift-apiserver plus the
	// always-implied kube-apiserver), but leave KubeAPIServerAvailable unset.
	fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(
		availableComponent("kube-apiserver"),
		availableComponent(oapiv2.ComponentName),
	).Build()
	cpContext := component.ControlPlaneContext{
		Context:                  t.Context(),
		Client:                   fakeClient,
		HCP:                      hcp,
		ApplyProvider:            upsert.NewApplyProvider(true),
		ReleaseImageProvider:     imageProvider,
		UserReleaseImageProvider: imageProvider,
	}

	g.Expect(NewComponent().Reconcile(cpContext)).To(Succeed())

	// The CSO workload must not be created while the precondition is unmet.
	csoDeployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: ComponentName, Namespace: namespace},
	}
	err := fakeClient.Get(t.Context(), client.ObjectKeyFromObject(csoDeployment), csoDeployment)
	g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "expected the CSO Deployment to not be created while KubeAPIServerAvailable is unmet")

	// The component reports that it is waiting on the precondition.
	cpoComponent := &hyperv1.ControlPlaneComponent{
		ObjectMeta: metav1.ObjectMeta{Name: ComponentName, Namespace: namespace},
	}
	g.Expect(fakeClient.Get(t.Context(), client.ObjectKeyFromObject(cpoComponent), cpoComponent)).To(Succeed())
	rollout := apiMeta.FindStatusCondition(cpoComponent.Status.Conditions, string(hyperv1.ControlPlaneComponentRolloutComplete))
	g.Expect(rollout).ToNot(BeNil(), "expected a RolloutComplete condition on the cluster-storage-operator component")
	g.Expect(rollout.Status).To(Equal(metav1.ConditionFalse))
	g.Expect(rollout.Reason).To(Equal(hyperv1.WaitingForPreconditionsReason))
	g.Expect(rollout.Message).To(ContainSubstring(string(hyperv1.KubeAPIServerAvailable)))
}
