package sleeper

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hccoapi "github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/support/upsert"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestNewComponent(t *testing.T) {
	t.Parallel()

	t.Run("When the HCP has no activation annotation, it should reconcile the sleeper component", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		const (
			namespace      = "test-namespace"
			hcpName        = "test-hcp"
			desiredVersion = "5.1.0"
			cliImage       = "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:1111111111111111111111111111111111111111111111111111111111111111"
		)

		hcp := &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:       hcpName,
				Namespace:  namespace,
				UID:        types.UID("test-hcp-uid"),
				Generation: 7,
			},
			Spec: hyperv1.HostedControlPlaneSpec{
				ReleaseImage: "quay.io/openshift-release-dev/ocp-release:5.1.0-x86_64",
			},
		}
		kubeAPIServer := &hyperv1.ControlPlaneComponent{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "kube-apiserver",
				Namespace: namespace,
			},
			Status: hyperv1.ControlPlaneComponentStatus{
				Version: desiredVersion,
				Conditions: []metav1.Condition{
					{Type: string(hyperv1.ControlPlaneComponentAvailable), Status: metav1.ConditionTrue},
					{Type: string(hyperv1.ControlPlaneComponentRolloutComplete), Status: metav1.ConditionTrue},
				},
			},
		}
		fakeClient := fake.NewClientBuilder().
			WithScheme(hccoapi.Scheme).
			WithObjects(kubeAPIServer).
			WithStatusSubresource(&hyperv1.ControlPlaneComponent{}).
			// Unlike an API server response, the fake client does not populate TypeMeta on Get.
			// Preserve the real API behavior so ApplyManifest can prove a true no-op.
			WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if err := c.Get(ctx, key, obj, opts...); err != nil {
						return err
					}
					if _, isDeployment := obj.(*appsv1.Deployment); isDeployment {
						obj.GetObjectKind().SetGroupVersionKind(appsv1.SchemeGroupVersion.WithKind("Deployment"))
					}
					return nil
				},
			}).
			Build()
		applyProvider := upsert.NewApplyProvider(true)
		cpContext := component.ControlPlaneContext{
			Context:       t.Context(),
			ApplyProvider: applyProvider,
			Client:        fakeClient,
			HCP:           hcp,
			ReleaseImageProvider: testutil.FakeImageProvider(
				testutil.WithVersion(desiredVersion),
				testutil.WithImages(map[string]string{"cli": cliImage}),
			),
		}

		controlPlaneComponent := NewComponent()
		g.Expect(controlPlaneComponent.Name()).To(Equal(ComponentName))
		g.Expect(controlPlaneComponent.Reconcile(cpContext)).To(Succeed())

		deployment := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: ComponentName, Namespace: namespace},
		}
		g.Expect(fakeClient.Get(t.Context(), client.ObjectKeyFromObject(deployment), deployment)).To(Succeed())
		g.Expect(deployment.Spec.Replicas).To(HaveValue(Equal(int32(1))))
		g.Expect(deployment.Spec.Template.Spec.Containers).To(HaveLen(1))
		g.Expect(deployment.Spec.Template.Spec.Containers[0].Image).To(Equal(cliImage))
		g.Expect(deployment.OwnerReferences).To(ContainElement(metav1.OwnerReference{
			APIVersion:         hyperv1.GroupVersion.String(),
			Kind:               "HostedControlPlane",
			Name:               hcpName,
			UID:                types.UID("test-hcp-uid"),
			Controller:         ptr.To(true),
			BlockOwnerDeletion: ptr.To(true),
		}))

		g.Expect(controlPlaneComponent.Reconcile(cpContext)).To(Succeed())
		g.Expect(applyProvider.ValidateUpdateEvents(0)).To(Succeed())

		reconciledComponent := &hyperv1.ControlPlaneComponent{
			ObjectMeta: metav1.ObjectMeta{Name: ComponentName, Namespace: namespace},
		}
		g.Expect(fakeClient.Get(t.Context(), client.ObjectKeyFromObject(reconciledComponent), reconciledComponent)).To(Succeed())
		g.Expect(reconciledComponent.Status.Resources).To(BeEmpty())
		g.Expect(reconciledComponent.Status.ObservedGeneration).To(Equal(hcp.Generation))
		g.Expect(reconciledComponent.Status.Version).To(BeEmpty())
		g.Expect(reconciledComponent.Status.Conditions).To(HaveLen(2))

		availableCondition := meta.FindStatusCondition(reconciledComponent.Status.Conditions, string(hyperv1.ControlPlaneComponentAvailable))
		g.Expect(availableCondition).NotTo(BeNil())
		g.Expect(availableCondition.Status).To(Equal(metav1.ConditionFalse))
		g.Expect(availableCondition.Reason).To(Equal(hyperv1.NotFoundReason))

		rolloutCompleteCondition := meta.FindStatusCondition(reconciledComponent.Status.Conditions, string(hyperv1.ControlPlaneComponentRolloutComplete))
		g.Expect(rolloutCompleteCondition).NotTo(BeNil())
		g.Expect(rolloutCompleteCondition.Status).To(Equal(metav1.ConditionFalse))
		g.Expect(rolloutCompleteCondition.Reason).To(Equal("WaitingForRolloutComplete"))
	})
}

func TestIsRequestServing(t *testing.T) {
	t.Parallel()

	t.Run("When request serving is evaluated, it should return false", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		g.Expect((&sleeper{}).IsRequestServing()).To(BeFalse())
	})
}

func TestMultiZoneSpread(t *testing.T) {
	t.Parallel()

	t.Run("When multi-zone spread is evaluated, it should return false", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		g.Expect((&sleeper{}).MultiZoneSpread()).To(BeFalse())
	})
}

func TestNeedsManagementKASAccess(t *testing.T) {
	t.Parallel()

	t.Run("When management KAS access is evaluated, it should return false", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		g.Expect((&sleeper{}).NeedsManagementKASAccess()).To(BeFalse())
	})
}
