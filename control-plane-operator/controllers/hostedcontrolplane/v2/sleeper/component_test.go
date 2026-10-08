package sleeper

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/support/upsert"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestPredicate(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name        string
		annotations map[string]string
		expected    bool
	}{
		{
			name:     "When HCP annotations are nil, it should return false",
			expected: false,
		},
		{
			name:        "When the enable annotation is absent, it should return false",
			annotations: map[string]string{"example.com/unrelated": "true"},
			expected:    false,
		},
		{
			name:        "When the enable annotation is empty, it should return false",
			annotations: map[string]string{EnableAnnotation: ""},
			expected:    false,
		},
		{
			name:        "When the enable annotation is false, it should return false",
			annotations: map[string]string{EnableAnnotation: "false"},
			expected:    false,
		},
		{
			name:        "When the enable annotation has another value, it should return false",
			annotations: map[string]string{EnableAnnotation: "enabled"},
			expected:    false,
		},
		{
			name:        "When the enable annotation is exactly true, it should return true",
			annotations: map[string]string{EnableAnnotation: "true"},
			expected:    true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			cpContext := component.WorkloadContext{
				HCP: &hyperv1.HostedControlPlane{
					ObjectMeta: metav1.ObjectMeta{Annotations: tc.annotations},
				},
			}

			enabled, err := predicate(cpContext)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(enabled).To(Equal(tc.expected))
		})
	}
}

func TestNewComponent(t *testing.T) {
	t.Parallel()

	const (
		namespace      = "test-namespace"
		hcpName        = "test-hcp"
		desiredVersion = "4.18.0"
		cliImage       = "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:cli-fake"
	)

	newHCP := func(annotations map[string]string) *hyperv1.HostedControlPlane {
		return &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:        hcpName,
				Namespace:   namespace,
				UID:         types.UID("test-hcp-uid"),
				Annotations: annotations,
			},
		}
	}

	ownerReference := metav1.OwnerReference{
		APIVersion:         hyperv1.GroupVersion.String(),
		Kind:               "HostedControlPlane",
		Name:               hcpName,
		UID:                types.UID("test-hcp-uid"),
		Controller:         ptr.To(true),
		BlockOwnerDeletion: ptr.To(true),
	}

	for _, tc := range []struct {
		name        string
		annotations map[string]string
	}{
		{
			name: "When the enable annotation is missing, it should clean up existing component resources",
		},
		{
			name:        "When the enable annotation is not true, it should clean up existing component resources",
			annotations: map[string]string{EnableAnnotation: "false"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			existingDeployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
				Name:            ComponentName,
				Namespace:       namespace,
				OwnerReferences: []metav1.OwnerReference{ownerReference},
			}}
			existingComponent := &hyperv1.ControlPlaneComponent{ObjectMeta: metav1.ObjectMeta{
				Name:            ComponentName,
				Namespace:       namespace,
				OwnerReferences: []metav1.OwnerReference{ownerReference},
			}}
			fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).
				WithObjects(existingDeployment, existingComponent).
				Build()

			err := NewComponent().Reconcile(component.ControlPlaneContext{
				Context:       t.Context(),
				ApplyProvider: upsert.NewApplyProvider(true),
				Client:        fakeClient,
				HCP:           newHCP(tc.annotations),
			})
			g.Expect(err).ToNot(HaveOccurred())
			expectNotFound(g, t, fakeClient, existingDeployment)
			expectNotFound(g, t, fakeClient, existingComponent)
		})
	}

	t.Run("When the exact enable annotation is added and removed, it should create idempotently and clean up the component", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		hcp := newHCP(map[string]string{EnableAnnotation: "true"})
		dependency := &hyperv1.ControlPlaneComponent{
			ObjectMeta: metav1.ObjectMeta{Name: "kube-apiserver", Namespace: namespace},
			Status: hyperv1.ControlPlaneComponentStatus{
				Version: desiredVersion,
				Conditions: []metav1.Condition{
					{Type: string(hyperv1.ControlPlaneComponentAvailable), Status: metav1.ConditionTrue},
					{Type: string(hyperv1.ControlPlaneComponentRolloutComplete), Status: metav1.ConditionTrue},
				},
			},
		}
		fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(dependency).Build()
		applyProvider := upsert.NewApplyProvider(true)
		cpContext := component.ControlPlaneContext{
			Context:                   t.Context(),
			ApplyProvider:             applyProvider,
			Client:                    fakeClient,
			HCP:                       hcp,
			ReleaseImageProvider:      testutil.FakeImageProvider(testutil.WithImages(map[string]string{"cli": cliImage})),
			SetDefaultSecurityContext: true,
			DefaultSecurityContextUID: 1001,
		}
		controlPlaneComponent := NewComponent()

		g.Expect(controlPlaneComponent.Reconcile(cpContext)).To(Succeed())

		deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: ComponentName, Namespace: namespace}}
		g.Expect(fakeClient.Get(t.Context(), client.ObjectKeyFromObject(deployment), deployment)).To(Succeed())
		g.Expect(deployment.Spec.Replicas).To(HaveValue(Equal(int32(1))))
		g.Expect(deployment.Spec.Template.Spec.SecurityContext.RunAsUser).To(HaveValue(Equal(int64(1001))))
		g.Expect(deployment.Spec.Template.Spec.Containers).To(HaveLen(1))
		container := deployment.Spec.Template.Spec.Containers[0]
		g.Expect(container.Name).To(Equal("sleeper"))
		g.Expect(container.Image).To(Equal(cliImage))
		g.Expect(container.Command).To(Equal([]string{"/bin/bash", "-c", "sleep infinity"}))
		g.Expect(container.SecurityContext).ToNot(BeNil())
		g.Expect(container.SecurityContext.RunAsNonRoot).To(HaveValue(BeTrue()))
		g.Expect(container.SecurityContext.SeccompProfile).ToNot(BeNil())
		g.Expect(container.SecurityContext.SeccompProfile.Type).To(Equal(corev1.SeccompProfileTypeRuntimeDefault))
		g.Expect(deployment.OwnerReferences).To(ContainElement(ownerReference))

		reconciledComponent := &hyperv1.ControlPlaneComponent{ObjectMeta: metav1.ObjectMeta{Name: ComponentName, Namespace: namespace}}
		g.Expect(fakeClient.Get(t.Context(), client.ObjectKeyFromObject(reconciledComponent), reconciledComponent)).To(Succeed())
		firstDeployment := deployment.DeepCopy()
		firstComponent := reconciledComponent.DeepCopy()

		g.Expect(controlPlaneComponent.Reconcile(cpContext)).To(Succeed())
		g.Expect(applyProvider.ValidateUpdateEvents(1)).To(Succeed())
		g.Expect(fakeClient.Get(t.Context(), client.ObjectKeyFromObject(deployment), deployment)).To(Succeed())
		g.Expect(fakeClient.Get(t.Context(), client.ObjectKeyFromObject(reconciledComponent), reconciledComponent)).To(Succeed())
		deployment.ResourceVersion = ""
		firstDeployment.ResourceVersion = ""
		reconciledComponent.ResourceVersion = ""
		firstComponent.ResourceVersion = ""
		g.Expect(deployment).To(Equal(firstDeployment))
		g.Expect(reconciledComponent).To(Equal(firstComponent))

		delete(hcp.Annotations, EnableAnnotation)
		g.Expect(controlPlaneComponent.Reconcile(cpContext)).To(Succeed())
		expectNotFound(g, t, fakeClient, deployment)
		expectNotFound(g, t, fakeClient, reconciledComponent)
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

func expectNotFound(g Gomega, t *testing.T, c client.Client, obj client.Object) {
	t.Helper()
	err := c.Get(t.Context(), client.ObjectKeyFromObject(obj), obj)
	g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "expected %T %s to be absent, got %v", obj, client.ObjectKeyFromObject(obj), err)
}
